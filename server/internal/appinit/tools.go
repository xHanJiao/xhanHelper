package appinit

import (
	"context"
	"log"

	"xhan-helper/internal/activity"
	"xhan-helper/internal/agent"
	"xhan-helper/internal/app"
	"xhan-helper/internal/config"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/mcp"
	"xhan-helper/internal/service"
	"xhan-helper/internal/tool"
)

// InitTools 创建工具注册表、注册内置工具、MCP 代理、Phase 6 检索/反思工具
func InitTools(cfg *config.Config, repos *app.Repos, infra *app.Infra, llmClients *app.LLMClients, activityStore *activity.Store) *ToolsBundle {
	// --- 主 Registry + 内置工具 ---
	reg := tool.NewRegistry()
	// 渐进式暴露元工具（唯一常驻请求体）：注册必须在最前，GetNativeFCTools/目录生成会跳过它
	reg.Register(tool.NewLoggingWrapper(tool.NewExposeToolsTool(), log.Default()))
	reg.Register(tool.NewLoggingWrapper(tool.NewBookkeepingTool(repos.Bill), log.Default()))
	reg.Register(tool.NewLoggingWrapper(tool.NewDatetimeTool(), log.Default()))
	reg.Register(tool.NewLoggingWrapper(tool.NewCalculatorTool(), log.Default()))
	// web：DeepSeek 官方 Search/Fetch，主循环可见（经 expose_tools 按需暴露），见 docs/design/web-tools/design.md。
	// enabled 但没有可用 key（provider 非 deepseek 且未单独配 web_tools.api_key）→ 跳过注册并说明原因。
	if cfg.WebTools.Enabled {
		if cfg.WebTools.APIKey == "" {
			log.Printf("[init] web_tools.enabled=true 但没有可用 API Key（provider 非 deepseek 时需单独配 web_tools.api_key），跳过 web 工具注册")
		} else {
			reg.Register(tool.NewLoggingWrapper(tool.NewWebTool(cfg.WebTools, repos.SearchUsage), log.Default()))
		}
	}
	// profile：仅 subAgent（user_context）可见，对主循环隐藏（HideTool 模式）。
	// 画像已由 resource zone 每轮注入主循环，暴露查询给主循环是冗余且膨胀 catalog。
	reg.Register(tool.NewLoggingWrapper(tool.NewProfileTool(repos.UserProfileFact), log.Default()))
	reg.HideTool("profile")
	reg.Register(tool.NewLoggingWrapper(tool.NewDocumentTool(repos.Document, repos.DocumentAnnotation, repos.DocumentAnnotationReply, infra.ImageStore.Repo()), log.Default()))
	reg.Register(tool.NewLoggingWrapper(tool.NewImageTool(infra.ImageStore.Repo(), infra.ImageStore), log.Default()))

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
	chunkBuilder := agent.NewChunkBuilder(infra, repos.Chunk, repos.Embedding, cfg.Vector.ChunkDisabledParents)
	// 实体派生数据清理器：笔记/记忆删除时统一清向量行 + chunk + 版本快照（见 docs/bugs/017）
	entityPurger := service.NewEntityPurger(repos.Embedding, repos.Chunk, repos.Version, infra.VectorStore)

	reg.Register(tool.NewLoggingWrapper(tool.NewNoteTool(repos.Note, repos.Version, infra.VectorStore, infra.Embedder, repos.Embedding, chunkBuilder, conceptEmbedService, entityPurger), log.Default()))

	// 主循环写入工具 record_memory。runner（决策循环）在 InitAgents 建好后回填。
	recordMemoryTool := tool.NewRecordMemoryTool(nil)
	reg.Register(tool.NewLoggingWrapper(recordMemoryTool, log.Default()))

	topicStatsTool := tool.NewLoggingWrapper(tool.NewGetTopicStatsTool(repos.ConceptEmbed, repos.Note, repos.Mem, infra.Embedder, repos.Embedding, cfg.Reflection.ClusterThreshold), log.Default())
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

	themeEmbedder := agent.NewThemeEmbedder(infra, repos.ConceptEmbed, repos.Embedding)
	bgClients := &app.LLMClients{Flash: llmClients.BgFlash, Strong: llmClients.BgFlash}
	themeDiscoverer := agent.NewThemeDiscoverer(bgClients, infra, retrievalTools, themeStore, themeEmbedder, conceptEmbedService, repos.Trace,
		cfg.Reflection.DiscoverMaxIterations, cfg.Reflection.ClusterThreshold)
	reflectionReg.Register(tool.NewLoggingWrapper(themeDiscoverer, log.Default()))

	themeMgmtTool := tool.NewThemeTool(repos.ThemeState, themeEmbedder)
	reflectionReg.Register(tool.NewLoggingWrapper(themeMgmtTool, log.Default()))

	// --- 内存提取器 ---
	// 抽取段的采样温度单独设：抽取的产物是「用户原话」，越贪心越少改写（见
	// docs/design/memory-facts-verbatim）。只套在这一处，BgFlash 的其他消费者不受影响。
	extractLLM := llmClients.BgFlash
	if t := cfg.Server.MemoryExtractTemperature; t != nil {
		extractLLM = llm.WrapTemperature(extractLLM, *t)
	}
	memExtractor := agent.NewMemoryExtractor(&agent.MemoryExtractorConfig{
		LLMClient:     extractLLM,
		MemRepo:       repos.Mem,
		VectorStore:   infra.VectorStore,
		Embedder:      infra.Embedder,
		EmbeddingRepo: repos.Embedding,
		DupeThreshold: cfg.Server.MemoryDupeThreshold,
		MaxCandidates: cfg.Server.MemoryMaxPerSession,
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

	reg.Register(tool.NewLoggingWrapper(
		tool.NewReminderTool(repos.Reminder, llmClients.BgFlash), log.Default()))
	// HTML 产物：生成可在右栏打开的页面（见 docs/design/html-artifact）
	reg.Register(tool.NewLoggingWrapper(
		tool.NewHTMLArtifactTool(repos.Artifact, infra.ImageStore), log.Default()))
	reg.Register(tool.NewLoggingWrapper(
		tool.NewPlanTool(repos.ConversationPlan), log.Default()))

	// 用户行为日志只读 tool（骨架级）：仅在 events.db 启用时注册（云端显式关闭 → 不注册，
	// 避免常驻目录里挂一个必然返回「未启用」的组）。
	if activityStore != nil {
		reg.Register(tool.NewLoggingWrapper(
			tool.NewUserActionLogTool(activityStore, repos.Document.GetTitlesByIDs), log.Default()))
	}
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
		ConceptEmbedService: conceptEmbedService,
		EntityPurger:        entityPurger,
		RecordMemoryTool:    recordMemoryTool,
	}
}
