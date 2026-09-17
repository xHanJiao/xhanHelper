package appinit

import (
	"context"
	"log"

	"xhan-helper/internal/agent"
	"xhan-helper/internal/app"
	"xhan-helper/internal/config"
	"xhan-helper/internal/mcp"
	"xhan-helper/internal/tool"
)

// InitTools 创建工具注册表、注册内置工具、MCP 代理、Phase 6 检索/反思工具
func InitTools(cfg *config.Config, repos *app.Repos, infra *app.Infra, llmClients *app.LLMClients) *ToolsBundle {
	// --- 主 Registry + 内置工具 ---
	reg := tool.NewRegistry()
	// 渐进式暴露元工具（唯一常驻请求体）：注册必须在最前，GetNativeFCTools/目录生成会跳过它
	reg.Register(tool.NewLoggingWrapper(tool.NewExposeToolsTool(), log.Default()))
	reg.Register(tool.NewLoggingWrapper(tool.NewBookkeepingTool(repos.Bill), log.Default()))
	reg.Register(tool.NewLoggingWrapper(tool.NewDatetimeTool(), log.Default()))
	reg.Register(tool.NewLoggingWrapper(tool.NewCalculatorTool(), log.Default()))
	// webfetch：仅 subAgent（explore）可见，对主循环隐藏（HideTool 模式，见 docs/design/phase-18-subagent-explore.md 1.2）
	// 由配置 webfetch.enabled 控制是否启用（默认关闭，token 消耗大）
	if cfg.WebFetch.Enabled {
		reg.Register(tool.NewLoggingWrapper(tool.NewWebFetchTool(cfg.WebFetch.TimeoutSeconds, cfg.WebFetch.MaxBytes), log.Default()))
		reg.HideTool("webfetch")
	}
	// profile：仅 subAgent（user_context）可见，对主循环隐藏（HideTool 模式，同 webfetch）。
	// 画像已由 resource zone 每轮注入主循环，暴露查询给主循环是冗余且膨胀 catalog。
	reg.Register(tool.NewLoggingWrapper(tool.NewProfileTool(repos.UserProfileFact), log.Default()))
	reg.HideTool("profile")
	reg.Register(tool.NewLoggingWrapper(tool.NewDocumentTool(repos.Document), log.Default()))

	// 种子内置工具到 tool_registries 表（幂等）
	if err := tool.SeedBuiltinTools(context.Background(), repos.Tool, infra.Embedder, repos.Embedding); err != nil {
		log.Printf("[seed] SeedBuiltinTools error: %v", err)
	}

	reg.Register(tool.NewLoggingWrapper(tool.NewAgentTool(repos.Agent, repos.Version, repos.Conversation), log.Default()))

	// --- MCP Manager（保留：monitor 看板统计用；MCP proxy 工具已废弃下掉） ---
	mcpCfg := mcp.Config{MaxConcurrent: cfg.MCP.MaxConcurrent}
	for _, s := range cfg.MCP.Servers {
		if s.Name == "" || s.Command == "" {
			continue
		}
		mcpCfg.Servers = append(mcpCfg.Servers, mcp.ServerConfig{
			Name:            s.Name,
			Command:         s.Command,
			Args:            s.Args,
			Env:             s.Env,
			IdleTimeoutSecs: s.IdleTimeoutSecs,
			Description:     s.Description,
		})
	}
	mcpManager := mcp.NewManager(mcpCfg)

	// --- Phase 6: 检索工具 ---
	conceptEmbedService := agent.NewConceptEmbedService(infra, repos.ConceptEmbed, repos.Embedding)
	chunkBuilder := agent.NewChunkBuilder(infra, repos.Chunk, repos.Embedding)

	reg.Register(tool.NewLoggingWrapper(tool.NewNoteTool(repos.Note, repos.Version, infra.VectorStore, infra.Embedder, repos.Embedding, repos.Chunk, chunkBuilder, conceptEmbedService), log.Default()))

	topicStatsTool := tool.NewLoggingWrapper(tool.NewGetTopicStatsTool(repos.ConceptEmbed, repos.Note, repos.Mem, infra.Embedder, infra.VectorStore, conceptEmbedService, cfg.Reflection.ClusterThreshold), log.Default())
	topicDetailTool := tool.NewLoggingWrapper(tool.NewGetTopicDetailTool(repos.Note, repos.Mem), log.Default())
	billStatsTool := tool.NewLoggingWrapper(tool.NewGetBillStatsTool(repos.Bill), log.Default())

	retrievalTools := map[string]tool.Tool{
		"get_topic_stats":  topicStatsTool,
		"get_topic_detail": topicDetailTool,
		"get_bill_stats":   billStatsTool,
		"search_messages":  tool.NewMessageSearchTool(repos.Message, repos.Conversation, infra.Embedder, infra.VectorStore, cfg.Chat.HistoryRounds, cfg.Chat.MaxSummaryRounds),
	}

	// --- Phase 6: 反思系统工具 ---
	themeStore := agent.NewThemeStore(repos.ThemeState)
	reflectionReg := tool.NewRegistry()
	reflectionReg.Register(tool.NewLoggingWrapper(
		agent.NewThemeAnalyst(llmClients.BgFlash, retrievalTools, themeStore, repos.Trace, cfg.Reflection.AnalystMaxIterations, chunkBuilder), log.Default()))
	reflectionReg.Register(tool.NewLoggingWrapper(
		agent.NewScorer(llmClients.BgFlash, repos.Trace), log.Default()))
	reflectionReg.Register(tool.NewLoggingWrapper(
		agent.NewSecretary(llmClients.BgFlash, repos.Trace), log.Default()))

	themeEmbedder := agent.NewThemeEmbedder(infra, repos.ConceptEmbed, repos.Embedding)
	bgClients := &app.LLMClients{Flash: llmClients.BgFlash, Strong: llmClients.BgFlash}
	themeDiscoverer := agent.NewThemeDiscoverer(bgClients, infra, retrievalTools, themeStore, themeEmbedder, conceptEmbedService, repos.Trace,
		cfg.Reflection.DiscoverMaxIterations, cfg.Reflection.ClusterThreshold)
	reflectionReg.Register(tool.NewLoggingWrapper(themeDiscoverer, log.Default()))

	mailboxTool := tool.NewMailboxTool(repos.Mailbox)
	reflectionReg.Register(tool.NewLoggingWrapper(mailboxTool, log.Default()))

	themeMgmtTool := tool.NewThemeTool(repos.ThemeState, themeEmbedder)
	reflectionReg.Register(tool.NewLoggingWrapper(themeMgmtTool, log.Default()))

	// --- 内存提取器 ---
	memExtractor := agent.NewMemoryExtractor(&agent.MemoryExtractorConfig{
		LLMClient:     llmClients.BgFlash,
		MemRepo:       repos.Mem,
		VectorStore:   infra.VectorStore,
		Embedder:      infra.Embedder,
		EmbeddingRepo: repos.Embedding,
		DupeThreshold: cfg.Server.MemoryDupeThreshold,
		ChunkBuilder:  chunkBuilder,
		ConceptEmbed:  conceptEmbedService,
	})

	// --- 搜索工具注册到主 Registry ---
	reg.Register(tool.NewLoggingWrapper(
		tool.NewMessageSearchTool(repos.Message, repos.Conversation, infra.Embedder, infra.VectorStore, cfg.Chat.HistoryRounds, cfg.Chat.MaxSummaryRounds), log.Default()))
	reg.Register(tool.NewLoggingWrapper(
		tool.NewThemeSearchTool(repos.ThemeState, infra.Embedder, infra.VectorStore), log.Default()))
	reg.Register(tool.NewLoggingWrapper(themeMgmtTool, log.Default()))
	reg.Register(tool.NewLoggingWrapper(
		tool.NewGetSummaryTool(repos.ConversationSummary, infra.VectorStore, infra.Embedder), log.Default()))

	missionEmbedder := agent.NewMissionEmbedder(infra, repos.Embedding)

	reg.Register(tool.NewLoggingWrapper(
		tool.NewReminderTool(repos.Reminder, llmClients.BgFlash), log.Default()))
	reg.Register(tool.NewLoggingWrapper(
		tool.NewPlanTool(repos.ConversationPlan), log.Default()))

	reg.ValidateNativeFC()
	// 校验 ReadOnlyFuncs 声明与 Functions() 一致（plan 模式只读拦截依赖，拼错直接 panic）
	reg.ValidateReadOnlyFuncs()
	// 校验所有可见工具实现了 CatalogProvider（渐进式目录依赖，缺省直接 panic）
	reg.ValidateCatalog()

	return &ToolsBundle{
		Registry:            reg,
		ReflectionReg:       reflectionReg,
		MCPManager:          mcpManager,
		MemExtractor:        memExtractor,
		MissionEmbedder:     missionEmbedder,
		ConceptEmbedService: conceptEmbedService,
	}
}
