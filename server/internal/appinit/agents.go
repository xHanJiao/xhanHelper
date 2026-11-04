package appinit

import (
	"context"
	"log"
	"sync/atomic"
	"time"

	"xhan-helper/internal/activity"
	"xhan-helper/internal/agent"
	"xhan-helper/internal/agent/ctxflow"
	"xhan-helper/internal/agent/promptbuilder"
	"xhan-helper/internal/agent/replay"
	"xhan-helper/internal/agent/retrieval"
	"xhan-helper/internal/app"
	"xhan-helper/internal/config"
	"xhan-helper/internal/model"
	"xhan-helper/internal/tool"
)

// InitAgents 创建 Retrieval Sources、Orchestrator、ReflectionOrchestrator、CronRunner
func InitAgents(cfg *config.Config, repos *app.Repos, infra *app.Infra, llmClients *app.LLMClients, tools *ToolsBundle, vs *agent.VectorStore, activityStore *activity.Store) *AgentBundle {
	// 活动埋点：工具调度器统一收口 tool.call.*（store 为 nil 时不采集）
	tools.Registry.SetActivityStore(activityStore)
	tools.ReflectionReg.SetActivityStore(activityStore)

	// --- Retrieval Sources ---
	memDecayer := retrieval.NewTimeDecay(cfg.Retrieval.Memory.FreshnessDecay)
	noteDecayer := retrieval.NewTimeDecay(cfg.Retrieval.Note.FreshnessDecay)
	historyDecayer := retrieval.NewTimeDecay(cfg.Retrieval.History.FreshnessDecay)
	summaryDecayer := retrieval.NewTimeDecay(cfg.Retrieval.Summary.FreshnessDecay)

	chunkBuilder := agent.NewChunkBuilder(infra, repos.Chunk, repos.Embedding, cfg.Vector.ChunkDisabledParents)

	memSource := retrieval.NewMemorySource(vs, repos.Mem, repos.Embedding, repos.Chunk, chunkBuilder, memDecayer)
	noteSource := retrieval.NewNoteSource(vs, repos.Note, repos.Embedding, repos.Chunk, chunkBuilder, noteDecayer)
	eventSource := retrieval.NewEventSource(vs, repos.Mem, repos.Event, retrieval.EventSourceConfig{
		QuotaFull:        cfg.Retrieval.ResourceZone.QuotaEventFull,
		QuotaCompact:     cfg.Retrieval.ResourceZone.QuotaEventCompact,
		QuotaSummary:     cfg.Retrieval.ResourceZone.QuotaEventSummary,
		QuotaIndex:       cfg.Retrieval.ResourceZone.QuotaEventIndex,
		PinnedMax:        cfg.Retrieval.ResourceZone.QuotaEventPinned,
		MemoriesPerEvent: cfg.Retrieval.ResourceZone.EventMemories,
	}, retrieval.NewTimeDecay(cfg.Retrieval.Event.FreshnessDecay))
	historySource := retrieval.NewHistorySource(vs, repos.Message, repos.Embedding, historyDecayer, cfg.Vector.MessageRetentionDays)
	simpleHistorySource := retrieval.NewSimpleHistorySource(vs, repos.Message)
	themeSource := retrieval.NewThemeSource(repos.ThemeState, vs)
	summarySource := retrieval.NewSummarySource(vs, repos.ConversationSummary, repos.Embedding, repos.Chunk, chunkBuilder, summaryDecayer, cfg.Vector.SummaryRetentionDays)

	bgCtx, bgCancel := context.WithCancel(context.Background())

	// 向量预热放后台：HTTP 先就绪（窗口秒开），预热完成后再置 ready。
	// 顺序保持：先清理废弃向量（chunk 幂等），再 warmup。
	// 空库 / 已全量嵌入的库预热很快；ready 前的检索会缺内存向量（已接受）。
	ready := &atomic.Bool{}
	go func() {
		runVectorStartupCleanup(bgCtx, cfg, repos)

		memSource.Warmup(bgCtx, infra.Embedder)
		noteSource.Warmup(bgCtx, infra.Embedder)
		historySource.Warmup(bgCtx, infra.Embedder)
		summarySource.Warmup(bgCtx, infra.Embedder)

		// 从 embedding 表加载向量到内存 store
		agent.WarmupAll(bgCtx, infra.VectorStore, repos.Embedding, repos.ConceptEmbed, repos.ThemeState, cfg.Vector.ChunkDisabledParents)
		ready.Store(true)
	}()

	// 统一搜索工具（语义搜索走 ContextSource，关键词搜索走 repo）。
	// 检索统一入口：主对话 + 内部子系统（StateUpdater/ThemeAnalyst）共用。
	unifiedSearch := tool.NewUnifiedSearchTool(
		map[string]retrieval.ContextSource{
			"memory":  memSource,
			"note":    noteSource,
			"summary": summarySource,
			"event":   eventSource,
			"history": simpleHistorySource,
		},
		infra.Embedder,
		repos.Message,
		repos.Mem,
		repos.Note,
		repos.ConversationSummary,
		repos.Event,
	)
	tools.Registry.Register(tool.NewLoggingWrapper(unifiedSearch, log.Default()))

	// 事件工具组：查询 / 钉住 / 改名 / 删除 / 合并（设计 §7.1）。
	eventTool := tool.NewEventTool(
		repos.Event, repos.Mem, repos.Conversation, infra.Embedder, eventSource,
		cfg.Retrieval.ResourceZone.QuotaEventPinned,
	)
	tools.Registry.Register(tool.NewLoggingWrapper(eventTool, log.Default()))

	// 事件拼装只读注册表：只放 unified_search + event.get（设计 §7.3）。
	// 循环只读是结构保证，不依赖 prompt 自觉。
	eventAssemblyReg := tool.NewRegistry()
	eventAssemblyReg.Register(tool.NewLoggingWrapper(unifiedSearch, log.Default()))
	eventAssemblyReg.Register(tool.NewLoggingWrapper(eventTool.ReadOnly(), log.Default()))
	eventAssemblyReg.ValidateNativeFC()
	eventAssemblyReg.ValidateReadOnlyFuncs()
	eventAssemblyReg.ValidateCatalog()

	// --- ReflectionOrchestrator + CronRunner (先于 orch 创建) ---
	reflectionOrch := agent.NewReflectionOrchestrator(&agent.ReflectionOrchestratorConfig{
		FlashClient:        llmClients.BgFlash,
		ReflectionReg:      tools.ReflectionReg,
		TraceRepo:          repos.Trace,
		ThemeStore:         agent.NewThemeStore(repos.ThemeState),
		MsgRepo:            repos.Message,
		NoteRepo:           repos.Note,
		MemRepo:            repos.Mem,
		MaxIterations:      cfg.Reflection.LaunchMaxIterations,
		ActiveDurationDays: cfg.Reflection.ActiveDurationDays,
	})
	// StateUpdater 检索工具集（与 ThemeAnalyst 共用同一套）
	retrievalTools := map[string]tool.Tool{
		"get_topic_detail": tool.NewGetTopicDetailTool(repos.Note, repos.Mem),
		"get_bill_stats":   tool.NewGetBillStatsTool(repos.Bill),
		"unified_search":   unifiedSearch,
	}
	stateUpdater := agent.NewStateUpdater(llmClients.BgFlash, repos.Message, repos.Conversation,
		agent.NewThemeStore(repos.ThemeState), infra.VectorStore, tools.ConceptEmbedService, chunkBuilder,
		retrievalTools, repos.Trace)
	// --- Summary 子系统 ---
	summaryWorker := agent.NewSummaryWorker(&agent.SummaryWorkerConfig{
		FlashClient:   llmClients.BgFlash,
		SummaryRepo:   repos.ConversationSummary,
		Embedder:      infra.Embedder,
		ChunkBuilder:  chunkBuilder,
		EmbeddingRepo: repos.Embedding,
		VectorStore:   infra.VectorStore,
		PersistDebug:  cfg.LLM.DebugPersist(),
	})

	// --- 对话后处理组（session_end）：摘要 + 记忆提取 + 主题状态更新 + 用户画像 ---
	profileWorker := agent.NewUserProfileWorker(&agent.UserProfileWorkerConfig{
		FlashClient:  llmClients.BgFlash,
		StrongClient: llmClients.BgFlash,
		Registry:     tools.Registry,
		ProfileRepo:  repos.UserProfile,
		FactRepo:     repos.UserProfileFact,
		UserRepo:     repos.User,
	})
	// 记录循环（记忆两段式的第二段）：逐候选决策循环。
	// 检索源复用上面已建的 memSource / noteSource（与统一检索同源）。
	recordLoop := agent.NewRecordDecisionLoop(&agent.RecordDeps{
		FlashLLM:      llmClients.Flash,
		LLMBG:         llmClients.BgFlash,
		MemRepo:       repos.Mem,
		NoteRepo:      repos.Note,
		MsgRepo:       repos.Message,
		VectorStore:   vs,
		Embedder:      infra.Embedder,
		EmbeddingRepo: repos.Embedding,
		ChunkRepo:     repos.Chunk,
		ChunkBuilder:  chunkBuilder,
		ConceptEmbed:  tools.ConceptEmbedService,
		EntityPurger:  tools.EntityPurger,
		MemSource:     memSource,
		NoteSource:    noteSource,
	}, agent.RecordLoopConfigFromServer(cfg), model.MemorySourcePostChat)
	// inline 入口：把同一个决策循环回填给主循环的 record_memory 工具。
	inlineLoop := agent.NewRecordDecisionLoop(&agent.RecordDeps{
		FlashLLM:      llmClients.Flash,
		LLMBG:         llmClients.BgFlash,
		MemRepo:       repos.Mem,
		NoteRepo:      repos.Note,
		MsgRepo:       repos.Message,
		VectorStore:   vs,
		Embedder:      infra.Embedder,
		EmbeddingRepo: repos.Embedding,
		ChunkRepo:     repos.Chunk,
		ChunkBuilder:  chunkBuilder,
		ConceptEmbed:  tools.ConceptEmbedService,
		EntityPurger:  tools.EntityPurger,
		MemSource:     memSource,
		NoteSource:    noteSource,
	}, agent.RecordLoopConfigFromServer(cfg), model.MemorySourceInline)
	tools.RecordMemoryTool.SetRunner(inlineLoop)
	// 事件拼装：session 结束后把本 session 新增/变更的记忆归到事件（等记忆写完才跑）。
	eventAssembler := agent.NewEventAssembler(&agent.EventAssemblerConfig{
		FlashClient:   llmClients.BgFlash,
		Registry:      eventAssemblyReg,
		EventRepo:     repos.Event,
		MemRepo:       repos.Mem,
		EmbeddingRepo: repos.Embedding,
		Embedder:      infra.Embedder,
		CandidateTop:  cfg.EventAssembly.CandidateTop,
		DedupeSim:     cfg.EventAssembly.DedupeSim,
		MaxIterations: cfg.EventAssembly.MaxIterations,
		BatchSize:     cfg.EventAssembly.BatchSize,
	})
	// 无痕会话销毁器：Stage A 由后处理/在线删除触发，Stage B 由 cron 轮询执行
	ephemeralDestroyer := agent.NewEphemeralDestroyer(repos.Conversation, vs, repos.DelayedTask)
	sessionEndGroup := agent.NewPostChatWorkerGroup([]agent.PostChatWorker{
		summaryWorker,
		agent.NewMemoryWorker(&agent.MemoryWorkerConfig{
			Extractor:         tools.MemExtractor,
			Loop:              recordLoop,
			MemoryAutoExtract: cfg.Server.AutoExtractMemory(),
		}),
		agent.NewEventWorker(&agent.EventWorkerConfig{
			MemRepo:   repos.Mem,
			Assembler: eventAssembler,
		}),
		agent.NewStateWorker(&agent.StateWorkerConfig{
			Updater: stateUpdater,
		}),
		profileWorker,
	}, repos.Message, ephemeralDestroyer)

	cronRunner := agent.NewCronRunner(&agent.CronRunnerConfig{
		Orch:              reflectionOrch,
		UserRepo:          repos.User,
		DelayedTaskRepo:   repos.DelayedTask,
		SessionEndGroup:   sessionEndGroup,
		EphemeralPurger:   ephemeralDestroyer,
		SessionGapMinutes: cfg.Chat.SessionGapMinutes,
		DiscoverSchedules: cfg.Reflection.DailySchedules,
		MinActiveDays:     cfg.Reflection.MinActiveDays,
		ConvRepo:          repos.Conversation,
		ArchiveAfterDays:  cfg.Archive.AfterDays,
		ArchiveInterval:   time.Duration(cfg.Archive.IntervalMinutes) * time.Minute,
		ImageStore:        infra.ImageStore,
		ImageTTLDays:      cfg.Storage.ImageTTLDays,
		ImageOrphanDays:   cfg.Storage.ImageOrphanTTLDays,
		ImageCleanupEvery: time.Duration(cfg.Storage.ImageCleanupIntervalMin) * time.Minute,
		EmbeddingRepo:     repos.Embedding,
		VectorStore:       vs,
		MsgRetentionDays:  cfg.Vector.MessageRetentionDays,
		SumRetentionDays:  cfg.Vector.SummaryRetentionDays,
		EvictInterval:     time.Duration(cfg.Vector.EvictIntervalHours) * time.Hour,
		DB:                repos.DB,
		VacuumThreshold:   int64(cfg.Vector.VacuumFreeThresholdMB) << 20,
		DocRepo:           repos.Document,
		Activity:          activityRecomputer(activityStore),
	})

	// --- Orchestrator ---
	pb := promptbuilder.NewPromptBuilder(
		agent.BaseSystemPrompt,
		tools.Registry,
		repos.Prompt,
		cfg.SubAgent.PromptUserContext(),
	)

	// ContextRetriever
	retrieveCfg := ctxflow.DefaultRetrieveConfig()
	applyRetrieveConfig(&retrieveCfg, cfg.Retrieval)
	retriever := ctxflow.NewContextRetriever(
		[]retrieval.ContextSource{themeSource, memSource, eventSource},
		&retrieveCfg,
	).WithModifier("theme", ctxflow.BoostFactor(1.3))

	// ContextFormatter
	formatter := ctxflow.NewXMLFormatter()

	// 客户端工具桥（Mac 本地 MCP 工具的 pending 路由）
	clientBridge := agent.NewClientToolBridge()

	// 调试 JSON 异步持久化（限频队列 + 保留清理）
	debugJSONSink := InitDebugJSON(cfg, repos.Message, bgCtx)
	// message.extra 检索 trace 滚动清理（独立于 debug_persist_json）
	InitExtraTraceCleanup(cfg, repos.Message, bgCtx)

	orch := agent.New(&agent.OrchestratorConfig{
		FlashClient:         llmClients.Flash,
		Registry:            tools.Registry,
		ProClient:           llmClients.Strong,
		PromptBuilder:       pb,
		MsgRepo:             repos.Message,
		ConvRepo:            repos.Conversation,
		Embedder:            infra.Embedder,
		VectorStore:         vs,
		EmbeddingRepo:       repos.Embedding,
		MaxIterations:       cfg.LLM.MaxIterations,
		Temperature:         cfg.LLM.DefaultTemperature,
		MaxTokens:           cfg.LLM.DefaultMaxTokens,
		ContextWindow:       cfg.LLM.ContextWindow,
		StrongContextWindow: cfg.LLM.StrongContextWindow,
		MaxToolResultChars:  cfg.LLM.MaxToolResultChars,
		ToolMaxConcurrency:  cfg.LLM.ToolMaxConcurrency,
		OnChatEnd: func(userID, convID, userMsgID string) {
			if cfg.Server.BackgroundTasksDisabled() {
				return
			}
			cronRunner.NotifySessionEnd(userID, convID, userMsgID)
		},
		VerifyEnabled:             cfg.LLM.Verification.Enabled,
		VerifyMaxRetries:          cfg.LLM.Verification.MaxRetries,
		VerifyModel:               cfg.LLM.Verification.Model,
		ToolExcludes:              cfg.Chat.ToolExcludes,
		SubAgentOnlyClientTools:   cfg.Chat.SubAgentOnlyClientTools,
		DebugPersistJSON:          cfg.LLM.DebugPersist(),
		DebugJSONSink:             debugJSONSink,
		ClientBridge:              clientBridge,
		ClientToolTimeoutSecs:     cfg.LLM.ClientToolTimeoutSecs,
		SubAgentMaxIterations:     cfg.SubAgent.ExploreMaxIterations,
		SubAgentTimeoutSecs:       cfg.SubAgent.ExploreTimeoutSeconds,
		SummaryRepo:               repos.ConversationSummary,
		MemRepo:                   repos.Mem,
		ContextRetriever:          retriever,
		ContextFormatter:          formatter,
		SummaryAttachCount:        cfg.Chat.SummaryAttachCount,
		SummaryAttachMaxDays:      cfg.Chat.SummaryAttachMaxDays,
		SummaryRelevanceThreshold: cfg.Chat.SummaryRelevanceThreshold,
		SummaryRelevanceHistoryN:  cfg.Chat.SummaryRelevanceHistoryN,
		SummaryDedupThreshold:     cfg.Chat.SummaryDedupThreshold,
		ResourceZoneEnabled:       cfg.Retrieval.ResourceZone.Enabled,
		ResourceZoneBudget:        cfg.Retrieval.ResourceZone.BudgetChars,
		ResourceZoneBudgets: ctxflow.ZoneBudgets{
			Profile: cfg.Retrieval.ResourceZone.BudgetProfile,
			Summary: cfg.Retrieval.ResourceZone.BudgetSummary,
			Memory:  cfg.Retrieval.ResourceZone.BudgetMemory,
			Note:    cfg.Retrieval.ResourceZone.BudgetNote,
			Topic:   cfg.Retrieval.ResourceZone.BudgetTopic,
			Event:   cfg.Retrieval.ResourceZone.BudgetEvent,
		},
		ResourceZoneQuotas: ctxflow.ZoneQuotas{
			Summary: cfg.Retrieval.ResourceZone.QuotaSummary,
			Memory:  cfg.Retrieval.ResourceZone.QuotaMemory,
			Note:    cfg.Retrieval.ResourceZone.QuotaNote,
			Topic:   cfg.Retrieval.ResourceZone.QuotaTopic,
		},
		ProfileFactRepo: repos.UserProfileFact,
		ImageStore:      infra.ImageStore,
		Activity:        activityStore,
	})

	if !cfg.Server.BackgroundTasksDisabled() {
		go cronRunner.Start(bgCtx)
	} else {
		log.Printf("[appinit] background tasks disabled, skip cronRunner")
	}

	// 启动时异步迁移旧画像（开关控制，facts 空才迁；迁移完成后可关）
	if cfg.Server.ProfileStartupMigration && !cfg.Server.BackgroundTasksDisabled() {
		go profileWorker.RunStartupMigration(bgCtx)
	} else {
		log.Printf("[appinit] profile startup migration disabled")
	}

	// 二次注册 task 工具（依赖 Orchestrator 实现 SubAgentRunner，避免 tool → agent 循环依赖）
	// 传入子 agent 预算：描述动态注入具体数字，主 LLM 据此判断任务拆解粒度
	tools.Registry.Register(tool.NewLoggingWrapper(
		tool.NewTaskTool(orch, cfg.SubAgent.ExploreMaxIterations, cfg.SubAgent.ExploreTimeoutSeconds), log.Default()))

	replayRunner := replay.NewRunner(&replay.RunnerConfig{
		FlashClient:        llmClients.Flash,
		ProClient:          llmClients.Strong,
		Registry:           tools.Registry,
		PromptBuilder:      pb,
		Embedder:           infra.Embedder,
		ContextRetriever:   retriever,
		ContextFormatter:   formatter,
		MaxIterations:      cfg.LLM.MaxIterations,
		MaxTokens:          cfg.LLM.DefaultMaxTokens,
		ToolMaxConcurrency: cfg.LLM.ToolMaxConcurrency,
	})

	return &AgentBundle{
		Orch:           orch,
		CronRunner:     cronRunner,
		ReflectionOrch: reflectionOrch,
		ReplayRunner:   replayRunner,
		ClientBridge:   clientBridge,
		Destroyer:      ephemeralDestroyer,
		BGCtx:          bgCtx,
		BGCancel:       bgCancel,
		Ready:          ready,
	}
}

// runVectorStartupCleanup 启动时清理废弃向量：短文本 chunk（幂等，每次启动）。
// 必须在 warmup 之前执行，避免旧数据被加载进内存。
func runVectorStartupCleanup(ctx context.Context, cfg *config.Config, repos *app.Repos) {
	for _, parent := range cfg.Vector.ChunkDisabledParents {
		if n, err := repos.Chunk.DeleteAllByType(ctx, parent); err != nil {
			log.Printf("[vector-cleanup] delete %s chunks error: %v", parent, err)
		} else if n > 0 {
			log.Printf("[vector-cleanup] deleted %d %s chunk rows", n, parent)
		}
		if n, err := repos.Embedding.DeleteChunksByParentType(ctx, parent); err != nil {
			log.Printf("[vector-cleanup] delete %s chunk embeddings error: %v", parent, err)
		} else if n > 0 {
			log.Printf("[vector-cleanup] deleted %d %s chunk embedding rows", n, parent)
		}
	}
	// tag 的 concept/embedding 已无消费者，一次性（幂等）清掉
	if ids, err := repos.ConceptEmbed.DeleteByType(ctx, "tag"); err != nil {
		log.Printf("[vector-cleanup] delete tag concepts error: %v", err)
	} else if len(ids) > 0 {
		if n, err := repos.Embedding.DeleteByEntities(ctx, model.EntityTypeConcept, ids); err != nil {
			log.Printf("[vector-cleanup] delete tag concept embeddings error: %v", err)
		} else {
			log.Printf("[vector-cleanup] deleted %d tag concepts + %d embeddings", len(ids), n)
		}
	}
}

// applyRetrieveConfig 将 yaml 配置中非零的 threshold/limit 覆盖到 RetrieveConfig
func applyRetrieveConfig(cfg *ctxflow.RetrieveConfig, rc config.RetrievalConfig) {
	if rc.Memory.Threshold > 0 {
		cfg.Memory.Threshold = rc.Memory.Threshold
	}
	if rc.Memory.Limit > 0 {
		cfg.Memory.Limit = rc.Memory.Limit
	}
	if rc.Note.Threshold > 0 {
		cfg.Note.Threshold = rc.Note.Threshold
	}
	if rc.Note.Limit > 0 {
		cfg.Note.Limit = rc.Note.Limit
	}
	if rc.History.Threshold > 0 {
		cfg.History.Threshold = rc.History.Threshold
	}
	if rc.History.Limit > 0 {
		cfg.History.Limit = rc.History.Limit
	}
	if rc.Theme.Threshold > 0 {
		cfg.Theme.Threshold = rc.Theme.Threshold
	}
	if rc.Theme.Limit > 0 {
		cfg.Theme.Limit = rc.Theme.Limit
	}
	if rc.Summary.Threshold > 0 {
		cfg.Summary.Threshold = rc.Summary.Threshold
	}
	if rc.Summary.Limit > 0 {
		cfg.Summary.Limit = rc.Summary.Limit
	}
	if rc.Event.Threshold > 0 {
		cfg.Event.Threshold = rc.Event.Threshold
	}
	if rc.Event.Limit > 0 {
		cfg.Event.Limit = rc.Event.Limit
	}
}

// activityRecomputer 把 *activity.Store 转成调度器要的接口；未启用（typed nil）时返回 nil，
// 否则 typed-nil 进接口后 != nil，"未启用"判定会失效（ticker/倒计时照跑）。
func activityRecomputer(s *activity.Store) agent.ActivityRecomputer {
	if s == nil {
		return nil
	}
	return s
}
