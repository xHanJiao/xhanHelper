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
	"xhan-helper/internal/tool"
)

// InitAgents 创建 Retrieval Sources、Orchestrator、ReflectionOrchestrator、CronRunner
func InitAgents(cfg *config.Config, repos *app.Repos, infra *app.Infra, llmClients *app.LLMClients, tools *ToolsBundle, vs *agent.VectorStore) *AgentBundle {
	// --- Retrieval Sources ---
	memDecayer := retrieval.NewTimeDecay(cfg.Retrieval.Memory.FreshnessDecay)
	noteDecayer := retrieval.NewTimeDecay(cfg.Retrieval.Note.FreshnessDecay)
	historyDecayer := retrieval.NewTimeDecay(cfg.Retrieval.History.FreshnessDecay)
	summaryDecayer := retrieval.NewTimeDecay(cfg.Retrieval.Summary.FreshnessDecay)

	chunkBuilder := agent.NewChunkBuilder(infra, repos.Chunk, repos.Embedding)

	memSource := retrieval.NewMemorySource(vs, repos.Mem, repos.Embedding, repos.Chunk, chunkBuilder, memDecayer)
	noteSource := retrieval.NewNoteSource(vs, repos.Note, repos.Embedding, repos.Chunk, chunkBuilder, noteDecayer)
	historySource := retrieval.NewHistorySource(vs, repos.Message, repos.Embedding, historyDecayer)
	simpleHistorySource := retrieval.NewSimpleHistorySource(vs, repos.Message)
	themeSource := retrieval.NewThemeSource(repos.ThemeState, vs)
	summarySource := retrieval.NewSummarySource(vs, repos.ConversationSummary, repos.Embedding, repos.Chunk, chunkBuilder, summaryDecayer)

	bgCtx, bgCancel := context.WithCancel(context.Background())

	memSource.Warmup(bgCtx, infra.Embedder)
	noteSource.Warmup(bgCtx, infra.Embedder)
	historySource.Warmup(bgCtx, infra.Embedder)
	summarySource.Warmup(bgCtx, infra.Embedder)

	// 从 embedding 表加载向量到内存 store
	agent.WarmupAll(bgCtx, infra.VectorStore, repos.Embedding, repos.ConceptEmbed, repos.ThemeState)

	// --- Mission 子系统 ---
	missionEmbedder := tools.MissionEmbedder
	missionDiscoverer := agent.NewMissionDiscoverer(llmClients.Strong, agent.NewThemeStore(repos.ThemeState), repos.Mission, missionEmbedder)

	// MissionRunner 的检索工具集
	missionRetrievalTools := map[string]tool.Tool{
		"search_notes":    tool.NewSearchNotesV2Tool(infra.VectorStore, repos.Note, repos.Chunk, infra.Embedder),
		"search_messages": tool.NewMessageSearchTool(repos.Message, repos.Conversation, infra.Embedder, infra.VectorStore, cfg.Chat.HistoryRounds, cfg.Chat.MaxSummaryRounds),
		"get_bill_stats":  tool.NewGetBillStatsTool(repos.Bill),
	}
	missionRunner := agent.NewMissionRunner(llmClients.Flash, repos.Mission, repos.Mailbox, missionRetrievalTools, repos.Trace)

	// --- ReflectionOrchestrator + CronRunner (先于 orch 创建) ---
	reflectionOrch := agent.NewReflectionOrchestrator(&agent.ReflectionOrchestratorConfig{
		FlashClient:        llmClients.Flash,
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
		"search_notes_v2":  tool.NewSearchNotesV2Tool(infra.VectorStore, repos.Note, repos.Chunk, infra.Embedder),
		"message_search":   tool.NewMessageSearchTool(repos.Message, repos.Conversation, infra.Embedder, infra.VectorStore, cfg.Chat.HistoryRounds, cfg.Chat.MaxSummaryRounds),
	}
	stateUpdater := agent.NewStateUpdater(llmClients.Flash, repos.Message, repos.Conversation,
		agent.NewThemeStore(repos.ThemeState), repos.PendingFinding, infra.VectorStore, tools.ConceptEmbedService, chunkBuilder,
		retrievalTools, repos.Trace)
	// --- Summary 子系统 ---
	summaryWorker := agent.NewSummaryWorker(&agent.SummaryWorkerConfig{
		FlashClient:   llmClients.Flash,
		SummaryRepo:   repos.ConversationSummary,
		Embedder:      infra.Embedder,
		ChunkBuilder:  chunkBuilder,
		EmbeddingRepo: repos.Embedding,
		VectorStore:   infra.VectorStore,
		PersistDebug:  cfg.LLM.DebugPersistJSON,
	})

	// --- 对话后处理组（session_end）：摘要 + 记忆提取 + 主题状态更新 + 用户画像 ---
	sessionEndGroup := agent.NewPostChatWorkerGroup([]agent.PostChatWorker{
		summaryWorker,
		agent.NewMemoryWorker(&agent.MemoryWorkerConfig{
			Extractor:         tools.MemExtractor,
			MemoryAutoExtract: cfg.Server.MemoryAutoExtract,
		}),
		agent.NewStateWorker(&agent.StateWorkerConfig{
			Updater: stateUpdater,
		}),
		agent.NewUserProfileWorker(&agent.UserProfileWorkerConfig{
			FlashClient:  llmClients.Flash,
			StrongClient: llmClients.Strong,
			Registry:     tools.Registry,
			ProfileRepo:  repos.UserProfile,
		}),
	}, repos.Message)

	cronRunner := agent.NewCronRunner(&agent.CronRunnerConfig{
		Orch:              reflectionOrch,
		UserRepo:          repos.User,
		DelayedTaskRepo:   repos.DelayedTask,
		MissionRunner:     missionRunner,
		MissionRepo:       repos.Mission,
		SessionEndGroup:   sessionEndGroup,
		SessionGapMinutes: cfg.Chat.SessionGapMinutes,
		DiscoverSchedules: cfg.Reflection.DailySchedules,
		DeliverInterval:   time.Duration(cfg.Reflection.DeliverIntervalMinutes) * time.Minute,
		MissionInterval:   time.Duration(cfg.Reflection.MissionCheckIntervalMinutes) * time.Minute,
		MinActiveDays:     cfg.Reflection.MinActiveDays,
	})

	// --- Orchestrator ---
	pb := promptbuilder.NewPromptBuilder(
		agent.BaseSystemPrompt,
		tools.Registry,
		repos.Prompt, cfg.LLM.UseNativeFC,
	)

	// ContextRetriever
	retrieveCfg := ctxflow.DefaultRetrieveConfig()
	applyRetrieveConfig(&retrieveCfg, cfg.Retrieval)
	retriever := ctxflow.NewContextRetriever(
		[]retrieval.ContextSource{themeSource, memSource, noteSource, historySource, summarySource},
		&retrieveCfg,
	).WithModifier("theme", ctxflow.BoostFactor(1.3))

	// ContextFormatter
	formatter := ctxflow.NewXMLFormatter()

	// 客户端工具桥（Mac 本地 MCP 工具的 pending 路由）
	clientBridge := agent.NewClientToolBridge()

	// 调试 JSON 异步持久化（限频队列 + 保留清理）
	debugJSONSink := InitDebugJSON(cfg, repos.Message, bgCtx)

	orch := agent.New(&agent.OrchestratorConfig{
		FlashClient:        llmClients.Flash,
		Registry:           tools.Registry,
		ProClient:          llmClients.Strong,
		PromptBuilder:      pb,
		MsgRepo:            repos.Message,
		ConvRepo:           repos.Conversation,
		Embedder:           infra.Embedder,
		VectorStore:        vs,
		EmbeddingRepo:      repos.Embedding,
		MaxIterations:      cfg.LLM.MaxIterations,
		Temperature:        cfg.LLM.DefaultTemperature,
		MaxTokens:          cfg.LLM.DefaultMaxTokens,
		MaxToolResultChars: cfg.LLM.MaxToolResultChars,
		ToolMaxConcurrency: cfg.LLM.ToolMaxConcurrency,
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
		DebugPersistJSON:          cfg.LLM.DebugPersistJSON,
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
		ReminderRepo:              repos.Reminder,
		ProfileRepo:               repos.UserProfile,
	})

	if !cfg.Server.DisableBackgroundTasks {
		go cronRunner.Start(bgCtx)
	} else {
		log.Printf("[appinit] background tasks disabled, skip cronRunner")
	}

	// 统一搜索工具（语义搜索走 ContextSource，关键词搜索走 repo）
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

	// v2 检索工具注册到主 Registry（explore 子 agent 白名单依赖，chunk 级语义检索）
	tools.Registry.Register(tool.NewLoggingWrapper(
		tool.NewSearchMemoriesV2Tool(infra.VectorStore, repos.Mem, repos.Chunk, infra.Embedder), log.Default()))
	tools.Registry.Register(tool.NewLoggingWrapper(
		tool.NewSearchNotesV2Tool(infra.VectorStore, repos.Note, repos.Chunk, infra.Embedder), log.Default()))

	// 二次注册 task 工具（依赖 Orchestrator 实现 SubAgentRunner，避免 tool → agent 循环依赖）
	tools.Registry.Register(tool.NewLoggingWrapper(tool.NewTaskTool(orch), log.Default()))

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
		Orch:              orch,
		CronRunner:        cronRunner,
		ReflectionOrch:    reflectionOrch,
		MissionRunner:     missionRunner,
		MissionDiscoverer: missionDiscoverer,
		ReplayRunner:      replayRunner,
		ClientBridge:      clientBridge,
		BGCtx:             bgCtx,
		BGCancel:          bgCancel,
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
