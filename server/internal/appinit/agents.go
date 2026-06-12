package appinit

import (
	"context"
	"time"

	"xhan-helper/internal/agent"
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

	chunkBuilder := agent.NewChunkBuilder(infra, repos.Chunk, repos.Embedding)

	memSource := retrieval.NewMemorySource(vs, repos.Mem, repos.Embedding, repos.Chunk, chunkBuilder, memDecayer)
	noteSource := retrieval.NewNoteSource(vs, repos.Note, repos.Embedding, repos.Chunk, chunkBuilder, noteDecayer)
	historySource := retrieval.NewHistorySource(vs, repos.Message, repos.Embedding, historyDecayer)
	themeSource := retrieval.NewThemeSource(repos.ThemeState, vs)

	bgCtx, bgCancel := context.WithCancel(context.Background())

	memSource.Warmup(bgCtx, infra.Embedder)
	noteSource.Warmup(bgCtx, infra.Embedder)
	historySource.Warmup(bgCtx, infra.Embedder)

	// 从 embedding 表加载向量到内存 store
	agent.WarmupAll(bgCtx, infra.VectorStore, repos.Embedding, repos.ConceptEmbed, repos.ThemeState)

	// Phase 15: history_state_text → state_facts JSONL 迁移
	agent.MigrateThemeFacts(bgCtx, repos.ThemeState, chunkBuilder, llmClients.Flash)

	// --- Mission 子系统 ---
	missionEmbedder := tools.MissionEmbedder
	missionDiscoverer := agent.NewMissionDiscoverer(llmClients.Strong, agent.NewThemeStore(repos.ThemeState), repos.Mission, missionEmbedder)

	// MissionRunner 的检索工具集
	missionRetrievalTools := map[string]tool.Tool{
		"search_notes":    tool.NewSearchNotesV2Tool(infra.VectorStore, repos.Note, repos.Chunk, infra.Embedder),
		"search_memories": tool.NewSearchMemoriesV2Tool(infra.VectorStore, repos.Mem, repos.Chunk, infra.Embedder),
		"search_messages": tool.NewMessageSearchTool(repos.Message, repos.Conversation),
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
	cronRunner := agent.NewCronRunner(&agent.CronRunnerConfig{
		Orch:              reflectionOrch,
		UserRepo:          repos.User,
		MemExtractor:      tools.MemExtractor,
		CommitTimer:       tools.CommitmentTimer,
		ConvRepo:          repos.Conversation,
		DelayedTaskRepo:   repos.DelayedTask,
		MissionRunner:     missionRunner,
		MissionRepo:       repos.Mission,
		DiscoverSchedules: cfg.Reflection.DailySchedules,
		DeliverInterval:   time.Duration(cfg.Reflection.DeliverIntervalMinutes) * time.Minute,
		MissionInterval:   time.Duration(cfg.Reflection.MissionCheckIntervalMinutes) * time.Minute,
		PostChatDelay:     time.Duration(cfg.Reflection.PostChatDelaySeconds) * time.Second,
		MemoryAutoExtract: cfg.Server.MemoryAutoExtract,
	})

	// --- Orchestrator ---
	promptCfg := promptbuilder.DefaultPromptConfig()
	applyRetrievalConfig(&promptCfg, cfg.Retrieval)
	pb := promptbuilder.NewSystemPromptBuilder(
		agent.BaseSystemPrompt,
		tools.Registry,
		repos.Prompt, cfg.LLM.UseNativeFC,
		promptbuilder.WithScoreModifier(
			promptbuilder.NewThemeProvider(themeSource, repos.ThemeState, repos.Conversation, &promptCfg),
			promptbuilder.BoostFactor(1.3),
		),
		promptbuilder.NewMemoryProvider(memSource, &promptCfg),
		promptbuilder.NewNoteProvider(noteSource, &promptCfg),
		promptbuilder.NewHistoryProvider(historySource, &promptCfg),
	)
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
		OnChatEnd:          func(userID, convID string) { cronRunner.NotifyChatEnd(userID, convID) },
		VerifyEnabled:      cfg.LLM.Verification.Enabled,
		VerifyMaxRetries:   cfg.LLM.Verification.MaxRetries,
		VerifyModel:        cfg.LLM.Verification.Model,
		UseNativeFC:        cfg.LLM.UseNativeFC,
	})

	go cronRunner.Start(bgCtx)

	replayRunner := replay.NewRunner(&replay.RunnerConfig{
		FlashClient:   llmClients.Flash,
		ProClient:     llmClients.Strong,
		Registry:      tools.Registry,
		PromptBuilder: pb,
		Embedder:      infra.Embedder,
		MaxIterations: cfg.LLM.MaxIterations,
		MaxTokens:     cfg.LLM.DefaultMaxTokens,
	})

	return &AgentBundle{
		Orch:              orch,
		CronRunner:        cronRunner,
		ReflectionOrch:    reflectionOrch,
		MissionRunner:     missionRunner,
		MissionDiscoverer: missionDiscoverer,
		ReplayRunner:      replayRunner,
		BGCtx:             bgCtx,
		BGCancel:          bgCancel,
	}
}

// applyRetrievalConfig 将 yaml 配置中非零的 threshold/limit 覆盖到 PromptConfig
func applyRetrievalConfig(pc *promptbuilder.PromptConfig, rc config.RetrievalConfig) {
	if rc.Memory.Threshold > 0 {
		pc.Memory.Threshold = rc.Memory.Threshold
	}
	if rc.Memory.Limit > 0 {
		pc.Memory.Limit = rc.Memory.Limit
	}
	if rc.Note.Threshold > 0 {
		pc.Note.Threshold = rc.Note.Threshold
	}
	if rc.Note.Limit > 0 {
		pc.Note.Limit = rc.Note.Limit
	}
	if rc.History.Threshold > 0 {
		pc.History.Threshold = rc.History.Threshold
	}
	if rc.History.Limit > 0 {
		pc.History.Limit = rc.History.Limit
	}
}
