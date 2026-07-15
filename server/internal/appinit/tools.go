package appinit

import (
	"context"
	"log"
	"time"

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
	reg.Register(tool.NewLoggingWrapper(tool.NewBookkeepingTool(repos.Bill), log.Default()))
	reg.Register(tool.NewLoggingWrapper(tool.NewDatetimeTool(), log.Default()))
	reg.Register(tool.NewLoggingWrapper(tool.NewCalculatorTool(), log.Default()))
	reg.Register(tool.NewLoggingWrapper(tool.NewDocumentTool(repos.Document, repos.Version), log.Default()))
	if cfg.Search.Provider == "bocha" {
		reg.Register(tool.NewLoggingWrapper(tool.NewBochaSearchTool(cfg.Search, repos.SearchUsage), log.Default()))
	} else {
		reg.Register(tool.NewLoggingWrapper(tool.NewSearchTool(cfg.Search, repos.SearchUsage), log.Default()))
	}

	// 初始化搜索用量 DB 记录，确保监控看板启动即显示正确数据
	expiresAt, _ := time.Parse("2006-01-02", cfg.Search.QuotaExpiresAt)
	if err := repos.SearchUsage.Ensure(context.Background(), cfg.Search.Provider, cfg.Search.TotalQuota, expiresAt, cfg.Search.UsedCount); err != nil {
		log.Printf("[init] search usage ensure error: %v", err)
	}

	// 种子内置工具到 tool_registries 表（幂等）
	if err := tool.SeedBuiltinTools(context.Background(), repos.Tool, infra.Embedder, repos.Embedding); err != nil {
		log.Printf("[seed] SeedBuiltinTools error: %v", err)
	}

	// tool_retriever（hybrid: 向量检索 + 关键词检索）
	keywordStrategy := tool.NewSimpleKeywordStrategy(repos.Tool)
	vectorStrategy := tool.NewVectorRetrievalStrategy(infra.Embedder, infra.VectorStore, repos.Tool)
	hybridStrategy := tool.NewHybridRetrievalStrategy(keywordStrategy, vectorStrategy, 0.7, 0.3)
	toolRetriever := tool.NewToolRetriever(hybridStrategy)
	toolRetriever.SetRegistrySearcher(reg)
	reg.Register(tool.NewLoggingWrapper(toolRetriever, log.Default()))
	reg.HideTool("tool_retriever")

	reg.Register(tool.NewLoggingWrapper(tool.NewAgentTool(repos.Agent, repos.Version, repos.Conversation), log.Default()))

	// 从 DB 加载动态工具
	reg.SetDynamicLoader(repos.Tool, cfg.Server.SidecarURL, repos.Tool, repos.Tool)

	// --- MCP Manager ---
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

	// 为每个 MCP server 注册 Proxy 工具
	for _, s := range mcpCfg.Servers {
		proxy := tool.NewMCPProxyTool(s, mcpManager)
		reg.Register(tool.NewLoggingWrapper(proxy, log.Default()))
		reg.HideTool(s.Name)
		log.Printf("[mcp] proxy tool registered (hidden): %s", s.Name)
	}

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
		agent.NewThemeAnalyst(llmClients.Strong, retrievalTools, themeStore, repos.Trace, cfg.Reflection.AnalystMaxIterations, chunkBuilder), log.Default()))
	reflectionReg.Register(tool.NewLoggingWrapper(
		agent.NewScorer(llmClients.Flash, repos.Trace), log.Default()))
	reflectionReg.Register(tool.NewLoggingWrapper(
		agent.NewSecretary(llmClients.Flash, repos.Trace), log.Default()))

	themeEmbedder := agent.NewThemeEmbedder(infra, repos.ConceptEmbed, repos.Embedding)
	themeDiscoverer := agent.NewThemeDiscoverer(llmClients, infra, retrievalTools, themeStore, themeEmbedder, conceptEmbedService, repos.Trace,
		cfg.Reflection.DiscoverMaxIterations, cfg.Reflection.ClusterThreshold)
	reflectionReg.Register(tool.NewLoggingWrapper(themeDiscoverer, log.Default()))

	mailboxTool := tool.NewMailboxTool(repos.Mailbox)
	reflectionReg.Register(tool.NewLoggingWrapper(mailboxTool, log.Default()))

	themeMgmtTool := tool.NewThemeTool(repos.ThemeState, themeEmbedder)
	reflectionReg.Register(tool.NewLoggingWrapper(themeMgmtTool, log.Default()))

	// --- 内存提取器 ---
	memExtractor := agent.NewMemoryExtractor(&agent.MemoryExtractorConfig{
		LLMClient:     llmClients.Flash,
		MemRepo:       repos.Mem,
		VectorStore:   infra.VectorStore,
		Embedder:      infra.Embedder,
		MsgRepo:       repos.Message,
		ConvRepo:      repos.Conversation,
		EmbeddingRepo: repos.Embedding,
		DupeThreshold: cfg.Server.MemoryDupeThreshold,
		ChunkBuilder:  chunkBuilder,
		ConceptEmbed:  conceptEmbedService,
	})

	// --- Commitment Timer ---
	commitmentTimer := agent.NewCommitmentTimer(repos.Commitment, repos.Mailbox)

	// --- 搜索工具注册到主 Registry ---
	reg.Register(tool.NewLoggingWrapper(
		tool.NewMemorySearchTool(infra.VectorStore, repos.Mem, infra.Embedder), log.Default()))
	reg.Register(tool.NewLoggingWrapper(
		tool.NewMessageSearchTool(repos.Message, repos.Conversation, infra.Embedder, infra.VectorStore, cfg.Chat.HistoryRounds, cfg.Chat.MaxSummaryRounds), log.Default()))
	reg.Register(tool.NewLoggingWrapper(
		tool.NewThemeSearchTool(repos.ThemeState, infra.Embedder, infra.VectorStore), log.Default()))
	reg.Register(tool.NewLoggingWrapper(
		tool.NewGetSummaryTool(repos.ConversationSummary, infra.VectorStore, infra.Embedder), log.Default()))

	missionEmbedder := agent.NewMissionEmbedder(infra, repos.Embedding)

	reg.Register(tool.NewLoggingWrapper(
		tool.NewReminderTool(repos.Reminder, llmClients.Flash), log.Default()))
	reg.Register(tool.NewLoggingWrapper(
		tool.NewMissionTool(repos.Mission, missionEmbedder), log.Default()))

	reg.ValidateNativeFC()

	return &ToolsBundle{
		Registry:            reg,
		ReflectionReg:       reflectionReg,
		MCPManager:          mcpManager,
		CommitmentTimer:     commitmentTimer,
		MemExtractor:        memExtractor,
		MissionEmbedder:     missionEmbedder,
		ConceptEmbedService: conceptEmbedService,
	}
}
