package appinit

import (
	"context"
	"log"
	"time"

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
func InitAgents(cfg *config.Config, repos *app.Repos, infra *app.Infra, llmClients *app.LLMClients, tools *ToolsBundle, vs *agent.VectorStore) *AgentBundle {
	// --- Retrieval Sources ---
	memDecayer := retrieval.NewTimeDecay(cfg.Retrieval.Memory.FreshnessDecay)
	noteDecayer := retrieval.NewTimeDecay(cfg.Retrieval.Note.FreshnessDecay)
	historyDecayer := retrieval.NewTimeDecay(cfg.Retrieval.History.FreshnessDecay)
	summaryDecayer := retrieval.NewTimeDecay(cfg.Retrieval.Summary.FreshnessDecay)

	chunkBuilder := agent.NewChunkBuilder(infra, repos.Chunk, repos.Embedding, cfg.Vector.ChunkDisabledParents)

	memSource := retrieval.NewMemorySource(vs, repos.Mem, repos.Embedding, repos.Chunk, chunkBuilder, memDecayer)
	noteSource := retrieval.NewNoteSource(vs, repos.Note, repos.Embedding, repos.Chunk, chunkBuilder, noteDecayer)
	historySource := retrieval.NewHistorySource(vs, repos.Message, repos.Embedding, historyDecayer, cfg.Vector.MessageRetentionDays)
	simpleHistorySource := retrieval.NewSimpleHistorySource(vs, repos.Message)
	themeSource := retrieval.NewThemeSource(repos.ThemeState, vs)
	summarySource := retrieval.NewSummarySource(vs, repos.ConversationSummary, repos.Embedding, repos.Chunk, chunkBuilder, summaryDecayer, cfg.Vector.SummaryRetentionDays)

	bgCtx, bgCancel := context.WithCancel(context.Background())

	// 清理废弃向量（chunk 幂等 + mission 一次性），必须在 warmup 之前
	runVectorStartupCleanup(bgCtx, cfg, repos)

	memSource.Warmup(bgCtx, infra.Embedder)
	noteSource.Warmup(bgCtx, infra.Embedder)
	historySource.Warmup(bgCtx, infra.Embedder)
	summarySource.Warmup(bgCtx, infra.Embedder)

	// 从 embedding 表加载向量到内存 store
	agent.WarmupAll(bgCtx, infra.VectorStore, repos.Embedding, repos.ConceptEmbed, repos.ThemeState, cfg.Vector.ChunkDisabledParents)

	// 统一搜索工具（语义搜索走 ContextSource，关键词搜索走 repo）。
	// 检索统一入口：主对话 + 内部子系统（MissionRunner/StateUpdater/ThemeAnalyst）共用。
	unifiedSearch := tool.NewUnifiedSearchTool(
		map[string]retrieval.ContextSource{
			"memory":  memSource,
			"note":    noteSource,
			"summary": summarySource,
			"theme":   themeSource,
			"history": simpleHistorySource,
		},
		infra.Embedder,
		repos.Message,
		repos.Mem,
		repos.Note,
		repos.ConversationSummary,
		repos.ThemeState,
	)
	tools.Registry.Register(tool.NewLoggingWrapper(unifiedSearch, log.Default()))

	// --- Mission 子系统：发现器已废弃（从未被调度调用）；仅保留 MissionRunner 供手动检查 ---
	missionRetrievalTools := map[string]tool.Tool{
		"unified_search": unifiedSearch,
		"get_bill_stats": tool.NewGetBillStatsTool(repos.Bill),
	}
	missionRunner := agent.NewMissionRunner(llmClients.BgFlash, repos.Mission, repos.Mailbox, missionRetrievalTools, repos.Trace)

	// --- ReflectionOrchestrator + CronRunner (先于 orch 创建) ---
	reflectionOrch := agent.NewReflectionOrchestrator(&agent.ReflectionOrchestratorConfig{
		FlashClient:        llmClients.BgFlash,
		ReflectionReg:      tools.ReflectionReg,
		TraceRepo:          repos.Trace,
		ThemeStore:         agent.NewThemeStore(repos.ThemeState),
		MailboxRepo:        repos.Mailbox,
		MsgRepo:            repos.Message,
		NoteRepo:           repos.Note,
		MemRepo:            repos.Mem,
		PendingFindingRepo: repos.PendingFinding,
		MaxIterations:      cfg.Reflection.LaunchMaxIterations,
		MaxDeliveryRetries: cfg.Reflection.MaxDeliveryRetries,
		ActiveDurationDays: cfg.Reflection.ActiveDurationDays,
	})
	// StateUpdater 检索工具集（与 ThemeAnalyst 共用同一套）
	retrievalTools := map[string]tool.Tool{
		"get_topic_detail": tool.NewGetTopicDetailTool(repos.Note, repos.Mem),
		"get_bill_stats":   tool.NewGetBillStatsTool(repos.Bill),
		"unified_search":   unifiedSearch,
	}
	stateUpdater := agent.NewStateUpdater(llmClients.BgFlash, repos.Message, repos.Conversation,
		agent.NewThemeStore(repos.ThemeState), repos.PendingFinding, infra.VectorStore, tools.ConceptEmbedService, chunkBuilder,
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
	// 无痕会话销毁器：Stage A 由后处理/在线删除触发，Stage B 由 cron 轮询执行
	ephemeralDestroyer := agent.NewEphemeralDestroyer(repos.Conversation, vs, repos.DelayedTask)
	sessionEndGroup := agent.NewPostChatWorkerGroup([]agent.PostChatWorker{
		summaryWorker,
		agent.NewMemoryWorker(&agent.MemoryWorkerConfig{
			Extractor:         tools.MemExtractor,
			MemoryAutoExtract: cfg.Server.MemoryAutoExtract,
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
		MissionRunner:     missionRunner,
		MissionRepo:       repos.Mission,
		SessionEndGroup:   sessionEndGroup,
		EphemeralPurger:   ephemeralDestroyer,
		SessionGapMinutes: cfg.Chat.SessionGapMinutes,
		DiscoverSchedules: cfg.Reflection.DailySchedules,
		DeliverInterval:   time.Duration(cfg.Reflection.DeliverIntervalMinutes) * time.Minute,
		MissionInterval:   time.Duration(cfg.Reflection.MissionCheckIntervalMinutes) * time.Minute,
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
		DocRepo:           repos.Document,
	})

	// --- Orchestrator ---
	pb := promptbuilder.NewPromptBuilder(
		agent.BaseSystemPrompt,
		tools.Registry,
		repos.Prompt, cfg.LLM.UseNativeFC,
		cfg.SubAgent.UserContextEnabled,
	)

	// ContextRetriever
	retrieveCfg := ctxflow.DefaultRetrieveConfig()
	applyRetrieveConfig(&retrieveCfg, cfg.Retrieval)
	retriever := ctxflow.NewContextRetriever(
		[]retrieval.ContextSource{themeSource, memSource, noteSource},
		&retrieveCfg,
	).WithModifier("theme", ctxflow.BoostFactor(1.3))

	// ContextFormatter
	formatter := ctxflow.NewXMLFormatter()

	// 客户端工具桥（Mac 本地 MCP 工具的 pending 路由）
	clientBridge := agent.NewClientToolBridge()

	// 调试 JSON 异步持久化（限频队列 + 保留清理）
	debugJSONSink := InitDebugJSON(cfg, repos.Message, bgCtx)

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
			if cfg.Server.DisableBackgroundTasks {
				return
			}
			cronRunner.NotifySessionEnd(userID, convID, userMsgID)
		},
		VerifyEnabled:             cfg.LLM.Verification.Enabled,
		VerifyMaxRetries:          cfg.LLM.Verification.MaxRetries,
		VerifyModel:               cfg.LLM.Verification.Model,
		UseNativeFC:               cfg.LLM.UseNativeFC,
		ToolExcludes:              cfg.Chat.ToolExcludes,
		SubAgentOnlyClientTools:   cfg.Chat.SubAgentOnlyClientTools,
		DebugPersistJSON:          cfg.LLM.DebugPersist(),
		DebugJSONSink:             debugJSONSink,
		ClientBridge:              clientBridge,
		ClientToolTimeoutSecs:     cfg.LLM.ClientToolTimeoutSecs,
		SubAgentMaxIterations:     cfg.SubAgent.ExploreMaxIterations,
		SubAgentTimeoutSecs:       cfg.SubAgent.ExploreTimeoutSeconds,
		SummaryRepo:               repos.ConversationSummary,
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
		},
		ResourceZoneQuotas: ctxflow.ZoneQuotas{
			Summary: cfg.Retrieval.ResourceZone.QuotaSummary,
			Memory:  cfg.Retrieval.ResourceZone.QuotaMemory,
			Note:    cfg.Retrieval.ResourceZone.QuotaNote,
			Topic:   cfg.Retrieval.ResourceZone.QuotaTopic,
		},
		ReminderRepo:    repos.Reminder,
		ProfileFactRepo: repos.UserProfileFact,
		ImageStore:      infra.ImageStore,
	})

	if !cfg.Server.DisableBackgroundTasks {
		go cronRunner.Start(bgCtx)
	} else {
		log.Printf("[appinit] background tasks disabled, skip cronRunner")
	}

	// 启动时异步迁移旧画像（开关控制，facts 空才迁；迁移完成后可关）
	if cfg.Server.ProfileStartupMigration && !cfg.Server.DisableBackgroundTasks {
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
		UseNativeFC:        cfg.LLM.UseNativeFC,
	})

	return &AgentBundle{
		Orch:           orch,
		CronRunner:     cronRunner,
		ReflectionOrch: reflectionOrch,
		MissionRunner:  missionRunner,
		ReplayRunner:   replayRunner,
		ClientBridge:   clientBridge,
		Destroyer:      ephemeralDestroyer,
		BGCtx:          bgCtx,
		BGCancel:       bgCancel,
	}
}

// runVectorStartupCleanup 启动时清理废弃向量：短文本 chunk（幂等，每次启动）+ mission 数据（一次性）。
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
	if cfg.Vector.PurgeMissionsOnce {
		if n, err := repos.Embedding.DeleteByType(ctx, "mission"); err != nil {
			log.Printf("[vector-cleanup] delete mission embeddings error: %v", err)
		} else {
			log.Printf("[vector-cleanup] deleted %d mission embedding rows", n)
		}
		if n, err := repos.Mission.DeleteAll(ctx); err != nil {
			log.Printf("[vector-cleanup] delete missions error: %v", err)
		} else {
			log.Printf("[vector-cleanup] deleted %d mission rows", n)
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
}
