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
)

// InitAgents 创建 Retrieval Sources、Orchestrator、ReflectionOrchestrator、CronRunner
func InitAgents(cfg *config.Config, repos *app.Repos, infra *app.Infra, llmClients *app.LLMClients, tools *ToolsBundle, vs *agent.VectorStore) *AgentBundle {
	// --- Retrieval Sources ---
	memRanker := &retrieval.Ranker{
		VectorWeight:    cfg.Retrieval.Memory.VectorWeight,
		KeywordWeight:   cfg.Retrieval.Memory.KeywordWeight,
		FreshnessWeight: cfg.Retrieval.Memory.FreshnessWeight,
		FreshnessDecay:  cfg.Retrieval.Memory.FreshnessDecay,
	}
	noteRanker := &retrieval.Ranker{
		VectorWeight:    cfg.Retrieval.Note.VectorWeight,
		KeywordWeight:   cfg.Retrieval.Note.KeywordWeight,
		FreshnessWeight: cfg.Retrieval.Note.FreshnessWeight,
		FreshnessDecay:  cfg.Retrieval.Note.FreshnessDecay,
	}
	historyRanker := &retrieval.Ranker{
		VectorWeight:    cfg.Retrieval.History.VectorWeight,
		KeywordWeight:   cfg.Retrieval.History.KeywordWeight,
		FreshnessWeight: cfg.Retrieval.History.FreshnessWeight,
		FreshnessDecay:  cfg.Retrieval.History.FreshnessDecay,
	}

	chunkBuilder := agent.NewChunkBuilder(infra, repos.Chunk, repos.Embedding)

	memSource := retrieval.NewMemorySource(vs, repos.Mem, repos.Embedding, repos.Chunk, chunkBuilder, memRanker)
	noteSource := retrieval.NewNoteSource(vs, repos.Note, repos.Embedding, repos.Chunk, chunkBuilder, noteRanker)
	historySource := retrieval.NewHistorySource(vs, repos.Message, repos.Embedding, historyRanker)
	themeSource := retrieval.NewThemeSource(repos.ThemeState, vs)

	bgCtx, bgCancel := context.WithCancel(context.Background())

	memSource.Warmup(bgCtx, infra.Embedder)
	noteSource.Warmup(bgCtx, infra.Embedder)
	historySource.Warmup(bgCtx, infra.Embedder)

	// 从 embedding 表加载向量到内存 store
	agent.WarmupAll(bgCtx, infra.VectorStore, repos.Embedding, repos.ConceptEmbed, repos.ThemeState)

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
		DraftExpireDays:    cfg.Reflection.DraftExpireDays,
		ActiveDurationDays: cfg.Reflection.ActiveDurationDays,
	})
	stateUpdater := agent.NewStateUpdater(llmClients.Flash, repos.Message, repos.Conversation,
		agent.NewThemeStore(repos.ThemeState), repos.PendingFinding)
	cronRunner := agent.NewCronRunner(&agent.CronRunnerConfig{
		Orch:              reflectionOrch,
		StateUpdater:      stateUpdater,
		UserRepo:          repos.User,
		MemExtractor:      tools.MemExtractor,
		CommitTimer:       tools.CommitmentTimer,
		ConvRepo:          repos.Conversation,
		DelayedTaskRepo:   repos.DelayedTask,
		DiscoverSchedules: cfg.Reflection.DailySchedules,
		DeliverInterval:   time.Duration(cfg.Reflection.DeliverIntervalMinutes) * time.Minute,
		PostChatDelay:     time.Duration(cfg.Reflection.PostChatDelaySeconds) * time.Second,
		MemoryAutoExtract: cfg.Server.MemoryAutoExtract,
	})

	// --- Orchestrator ---
	promptCfg := promptbuilder.DefaultPromptConfig()
	applyRetrievalConfig(&promptCfg, cfg.Retrieval)
	pb := promptbuilder.NewSystemPromptBuilder(
		agent.BaseSystemPrompt,
		tools.Registry,
		repos.Prompt,
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
		OnChatEnd:          func(userID, convID string) { cronRunner.NotifyChatEnd(userID, convID) },
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
		Orch:           orch,
		CronRunner:     cronRunner,
		ReflectionOrch: reflectionOrch,
		ReplayRunner:   replayRunner,
		BGCtx:          bgCtx,
		BGCancel:       bgCancel,
	}
}

// applyRetrievalConfig 将 yaml 配置中非零的 threshold/limit 覆盖到 PromptConfig
func applyRetrievalConfig(pc *promptbuilder.PromptConfig, rc config.RetrievalConfig) {
	if rc.Memory.Threshold > 0 {
		pc.MemoryThreshold = rc.Memory.Threshold
	}
	if rc.Memory.Limit > 0 {
		pc.MemoryLimit = rc.Memory.Limit
	}
	if rc.Note.Threshold > 0 {
		pc.NoteThreshold = rc.Note.Threshold
	}
	if rc.Note.Limit > 0 {
		pc.NoteLimit = rc.Note.Limit
	}
	if rc.History.Threshold > 0 {
		pc.HistoryThreshold = rc.History.Threshold
	}
	if rc.History.Limit > 0 {
		pc.HistoryLimit = rc.History.Limit
	}
}
