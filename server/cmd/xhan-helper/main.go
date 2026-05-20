package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"xhan-helper/internal/agent"
	"xhan-helper/internal/app"
	"xhan-helper/internal/config"
	"xhan-helper/internal/controller"
	"xhan-helper/internal/idgen"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/mcp"
	"xhan-helper/internal/middleware"
	"xhan-helper/internal/model"
	"xhan-helper/internal/monitor"
	"xhan-helper/internal/service"
	"xhan-helper/internal/tool"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	cfg, err := config.Load()
	if err != nil {
		log.Fatal("load config:", err)
	}

	// 初始化 ID 生成器
	if err := idgen.Init(cfg.Snowflake.NodeID); err != nil {
		log.Fatal("init idgen:", err)
	}

	log.Printf("[config] env=%s server.port=%d server.mode=%s",
		os.Getenv("APP_ENV"), cfg.Server.Port, cfg.Server.Mode)
	log.Printf("[config] db.driver=%s db.dsn=%s", cfg.DB.Driver, cfg.DB.DSN())
	log.Printf("[config] llm.provider=%s llm.model=%s llm.strong_model=%s llm.key=%s...",
		cfg.LLM.Provider, cfg.LLM.Model, cfg.LLM.StrongModel, cfg.LLM.APIKey[:min(8, len(cfg.LLM.APIKey))])
	log.Printf("[config] embedding.model=%s embedding.key=%s... embedding.base_url=%s",
		cfg.Embedding.Model, cfg.Embedding.APIKey[:min(8, len(cfg.Embedding.APIKey))], cfg.Embedding.BaseURL)

	gin.SetMode(cfg.Server.Mode)

	db := service.NewDB(cfg.DB)
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	if err := db.AutoMigrate(
		&model.User{},
		&model.UserToken{},
		&model.Conversation{},
		&model.Message{},
		&model.Bill{},
		&model.UserMemory{},
		&model.ScheduledTask{},
		&model.Note{},
		&model.Agent{},
		&model.ToolRegistry{},
		&model.ToolData{},
		&model.UserPrompt{},
		&model.Embedding{},
		&model.MCPServerRecord{},
		&model.MailboxItem{},
		&model.ThemeState{},
		&model.ReflectionTrace{},
		&model.ReflectionLLMCall{},
		&model.ReflectionDecision{},
		&model.EmbeddingChunk{},
		&model.ConceptEmbedding{},
		&model.PendingFinding{},
		&model.Commitment{},
		&model.DelayedTask{},
	); err != nil {
		log.Printf("[db] AutoMigrate error: %v", err)
	}

	// LLM 客户端
	llmClient, err := llm.NewClient(cfg.LLM)
	if err != nil {
		log.Fatal("create llm client:", err)
	}
	llmClient = monitor.WrapLLMClient(llmClient, "orchestrator/flash")
	strongClient, _ := llm.NewStrongClient(cfg.LLM)
	if strongClient != nil {
		log.Printf("[llm] strong model available: %s", cfg.LLM.StrongModel)
		strongClient = monitor.WrapLLMClient(strongClient, "orchestrator/pro")
	}

	// Repos
	userRepo := service.NewUserRepo(db, cfg.Server.TokenTTLHours, cfg.Server.MaxTokensPerUser)
	convRepo := service.NewConversationRepo(db)
	msgRepo := service.NewMessageRepo(db)
	billRepo := service.NewBillRepo(db)
	memRepo := service.NewUserMemoryRepo(db)
	noteRepo := service.NewNoteRepo(db)
	agentRepo := service.NewAgentRepo(db)
	promptRepo := service.NewUserPromptRepo(db)
	toolRepo := service.NewToolRepo(db)
	embeddingRepo := service.NewEmbeddingRepo(db)
	mailboxRepo := service.NewMailboxRepo(db)
	themeStateRepo := service.NewThemeStateRepo(db)
	traceRepo := service.NewReflectionTraceRepo(db)
	chunkRepo := service.NewEmbeddingChunkRepo(db)
	conceptEmbedRepo := service.NewConceptEmbeddingRepo(db)
	pendingFindingRepo := service.NewPendingFindingRepo(db)
	commitmentRepo := service.NewCommitmentRepo(db)
	delayedTaskRepo := service.NewDelayedTaskRepo(db)

	// Tool Registry + Tools
	reg := tool.NewRegistry()
	reg.Register(tool.NewLoggingWrapper(tool.NewBookkeepingTool(billRepo), log.Default()))
	reg.Register(tool.NewLoggingWrapper(tool.NewDatetimeTool(), log.Default()))
	reg.Register(tool.NewLoggingWrapper(tool.NewCalculatorTool(), log.Default()))
	reg.Register(tool.NewLoggingWrapper(tool.NewMarkdownTool("data/markdown"), log.Default()))

	// Vector Store
	embedder := llm.NewEmbedder(cfg.Embedding.APIKey, cfg.Embedding.BaseURL, cfg.Embedding.Model)
	vectorStore := agent.NewVectorStore()
	vsAdapter := agent.NewVectorSearchAdapter(vectorStore)

	// Infrastructure + AI Clients
	infra := &app.Infra{DB: db, Embedder: embedder, VectorStore: vsAdapter}
	llmClients := &app.LLMClients{Flash: llmClient, Strong: strongClient}

	// Seed 内置工具到 tool_registries 表（幂等）
	if err := tool.SeedBuiltinTools(context.Background(), toolRepo, embedder, embeddingRepo); err != nil {
		log.Printf("[seed] SeedBuiltinTools error: %v", err)
	}

	// tool_retriever: 用关键词策略检索工具 + Registry 内存搜索（MCP 占位工具等）
	toolRetriever := tool.NewToolRetriever(tool.NewSimpleKeywordStrategy(toolRepo))
	toolRetriever.SetRegistrySearcher(reg)
	reg.Register(tool.NewLoggingWrapper(toolRetriever, log.Default()))
	reg.HideTool("tool_retriever")

	reg.Register(tool.NewLoggingWrapper(tool.NewAgentTool(agentRepo), log.Default()))

	// 从 DB 加载动态工具
	reg.SetDynamicLoader(toolRepo, cfg.Server.SidecarURL, toolRepo, toolRepo)

	// MCP Manager
	mcpCfg := mcp.Config{
		MaxConcurrent: cfg.MCP.MaxConcurrent,
	}
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
	defer mcpManager.Shutdown()

	// 为每个 MCP server 注册 Proxy 工具（发现 + 懒启动 + 转发）
	for _, s := range mcpCfg.Servers {
		proxy := tool.NewMCPProxyTool(s, mcpManager)
		reg.Register(tool.NewLoggingWrapper(proxy, log.Default()))
		reg.HideTool(s.Name)
		log.Printf("[mcp] proxy tool registered (hidden): %s", s.Name)
	}

	// ===== Phase 6: 检索工具（独立 Registry，不注册到主 reg） =====

	conceptEmbedService := agent.NewConceptEmbedService(infra, conceptEmbedRepo, embeddingRepo)

	// Chunk Builder (替换旧的整篇 embedding)
	chunkBuilder := agent.NewChunkBuilder(infra, chunkRepo, embeddingRepo)

	// NoteTool with chunk embedding
	reg.Register(tool.NewLoggingWrapper(tool.NewNoteTool(noteRepo, infra.VectorStore, embedder, embeddingRepo, chunkBuilder, conceptEmbedService), log.Default()))

	topicStatsTool := tool.NewLoggingWrapper(tool.NewGetTopicStatsTool(conceptEmbedRepo, noteRepo, memRepo, embedder, infra.VectorStore, conceptEmbedService, cfg.Reflection.ClusterThreshold), log.Default())
	topicDetailTool := tool.NewLoggingWrapper(tool.NewGetTopicDetailTool(noteRepo, memRepo), log.Default())
	billStatsTool := tool.NewLoggingWrapper(tool.NewGetBillStatsTool(billRepo), log.Default())

	retrievalTools := map[string]tool.Tool{
		"get_topic_stats":  topicStatsTool,
		"get_topic_detail": topicDetailTool,
		"get_bill_stats":   billStatsTool,
	}

	// ===== Phase 6: 反思工具（独立 Registry） =====
	themeStore := agent.NewThemeStore(themeStateRepo)
	themeAnalyst := agent.NewThemeAnalyst(llmClient, retrievalTools, themeStore, cfg.Reflection.AnalystMaxIterations)
	scorerTool := agent.NewScorer(llmClient)
	themeEmbedder := agent.NewThemeEmbedder(infra, conceptEmbedRepo, embeddingRepo)
	themeDiscoverer := agent.NewThemeDiscoverer(llmClients, infra, retrievalTools, themeStore, themeEmbedder, cfg.Reflection.DiscoverMaxIterations, cfg.Reflection.ClusterThreshold)
	mailboxTool := tool.NewMailboxTool(mailboxRepo)
	themeMgmtTool := tool.NewThemeTool(themeStateRepo, themeEmbedder)

	reflectionReg := tool.NewRegistry()
	reflectionReg.Register(tool.NewLoggingWrapper(themeAnalyst, log.Default()))
	reflectionReg.Register(tool.NewLoggingWrapper(scorerTool, log.Default()))
	reflectionReg.Register(tool.NewLoggingWrapper(agent.NewSecretary(llmClient), log.Default()))
	reflectionReg.Register(tool.NewLoggingWrapper(themeDiscoverer, log.Default()))
	reflectionReg.Register(tool.NewLoggingWrapper(mailboxTool, log.Default()))
	reflectionReg.Register(tool.NewLoggingWrapper(themeMgmtTool, log.Default()))

	// Memory Extractor（合并到对话后处理流程）
	memExtractor := agent.NewMemoryExtractor(llmClient, memRepo, infra.VectorStore, embedder, msgRepo, convRepo, embeddingRepo, cfg.Server.MemoryDupeThreshold, chunkBuilder, conceptEmbedService)

	commitmentTimer := agent.NewCommitmentTimer(commitmentRepo, mailboxRepo)

	// 注册提醒工具到主 Registry（对话中 LLM 直接调用）
	reg.Register(tool.NewLoggingWrapper(tool.NewReminderTool(commitmentRepo, commitmentTimer), log.Default()))

	// ===== Phase 6: ReflectionOrchestrator + CronRunner =====
	reflectionOrch := agent.NewReflectionOrchestrator(
		llmClient, reflectionReg, traceRepo, themeStore,
		mailboxRepo, msgRepo, noteRepo, memRepo,
		pendingFindingRepo,
		cfg.Reflection.LaunchMaxIterations,
	)
	reflectionOrch.SetMaxDeliveryRetries(cfg.Reflection.MaxDeliveryRetries)
	stateUpdater := agent.NewStateUpdater(llmClient, msgRepo, convRepo, themeStore, pendingFindingRepo)
	cronRunner := agent.NewCronRunner(reflectionOrch, stateUpdater, userRepo, memExtractor, commitmentTimer, convRepo, delayedTaskRepo)
	cronRunner.SetPostChatDelay(time.Duration(cfg.Reflection.PostChatDelaySeconds) * time.Second)
	cronRunner.SetDiscoverSchedules(cfg.Reflection.DailySchedules)
	cronRunner.SetDeliverInterval(time.Duration(cfg.Reflection.DeliverIntervalMinutes) * time.Minute)
	cronRunner.SetMemoryAutoExtract(cfg.Server.MemoryAutoExtract)
	themeDiscoverer.SetUpgradeThresholds(cfg.Reflection.DraftNominateCountThreshold, cfg.Reflection.DraftSpanDaysThreshold)
	reflectionOrch.SetDraftExpireDays(cfg.Reflection.DraftExpireDays)
	reflectionOrch.SetActiveDurationDays(cfg.Reflection.ActiveDurationDays)
	// 后台任务共享 context，关闭时统一取消
	bgCtx, bgCancel := context.WithCancel(context.Background())

	// Context Sources
	memSource := agent.NewMemorySource(vectorStore, memRepo, embeddingRepo)
	noteSource := agent.NewNoteSource(vectorStore, noteRepo, embeddingRepo)

	// Warmup 必须完成后再启动服务
	memSource.Warmup(bgCtx, embedder)
	noteSource.Warmup(bgCtx, embedder)
	// Theme embedding warmup：从 embedding 表加载到向量库，确保去重重启后有效
	warmupThemeEmbeddings(bgCtx, embeddingRepo, vectorStore)
	warmupChunkEmbeddings(bgCtx, embeddingRepo, vectorStore)
	warmupConceptEmbeddings(bgCtx, embeddingRepo, vectorStore)

	// Orchestrator
	retriever := agent.NewRetriever(memSource, noteSource)
	orch := agent.New(llmClient, reg, strongClient, retriever, msgRepo, convRepo, embedder, vectorStore, embeddingRepo, themeStateRepo, cfg.LLM.MaxIterations, cfg.LLM.DefaultTemperature, cfg.LLM.DefaultMaxTokens)

	// Markdown 文件定期清理（每小时清理超过 1h 的文件）
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				tool.CleanOldMarkdownFiles("data/markdown", 1*time.Hour)
			case <-bgCtx.Done():
				return
			}
		}
	}()

	orch.SetPromptResolver(promptRepo)

	// 启动反思调度器 + 聊天结束通知
	go cronRunner.Start(bgCtx)
	orch.SetOnChatEnd(func(userID, convID string) {
		cronRunner.NotifyChatEnd(userID, convID)
	})

	// Monitor snapshots
	snapshots := &SnapshotProvider{
		vectorStore:    vectorStore,
		mcpManager:     mcpManager,
		convRepo:       convRepo,
		themeStateRepo: themeStateRepo,
	}
	monitor.Register("vector_store", snapshots.VectorStore)
	monitor.Register("mcp", snapshots.MCP)
	monitor.Register("active_conversations", snapshots.ActiveConversations)
	monitor.Register("markdown_files", snapshots.MarkdownFiles)
	monitor.Register("themes", snapshots.Themes)

	// MCP idle reaper
	mcpManager.StartIdleReaper(bgCtx)

	// Handlers
	billHandler := controller.NewBillHandler(billRepo, memExtractor)
	noteHandler := controller.NewNoteHandler(noteRepo)
	memHandler := controller.NewMemoryHandler(memRepo, vectorStore)
	agentHandler := controller.NewAgentHandler(agentRepo, llmClient)
	toolHandler := controller.NewToolHandler(toolRepo)
	promptHandler := controller.NewPromptHandler(promptRepo)
	mailboxService := service.NewMailboxService(mailboxRepo, themeStateRepo)
	mailboxHandler := controller.NewMailboxHandler(mailboxService, mailboxRepo, convRepo, themeStateRepo, msgRepo)
	themeHandler := controller.NewThemeHandler(themeStateRepo)
	reflectionAdminHandler := controller.NewReflectionAdminHandler(traceRepo)

	// Router
	router := controller.NewRouter(cfg, orch, userRepo, convRepo, msgRepo, agentRepo, billHandler, noteHandler, memHandler, agentHandler, toolHandler, promptHandler, memExtractor, middleware.RequestLogger(log.Default()))

	// 注册 Phase 6 反思系统路由
	controller.RegisterReflectionRoutes(router, userRepo, mailboxHandler, themeHandler, reflectionAdminHandler, reflectionOrch, cronRunner,
		middleware.Auth(userRepo), monitor.AdminOnly(cfg.Server.AdminUsers))

	addr := ":" + strconv.Itoa(cfg.Server.Port)
	srv := &http.Server{
		Addr:    addr,
		Handler: router,
	}

	go func() {
		log.Printf("server starting on %s (mode: %s)", addr, cfg.Server.Mode)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("server error:", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("shutting down...")

	// 先取消后台 goroutine
	bgCancel()
	// 再关闭 HTTP server
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
	log.Println("server stopped")
}

// warmupThemeEmbeddings 从 embedding 表加载主题向量到内存向量库
func warmupThemeEmbeddings(ctx context.Context, embeddingRepo *service.EmbeddingRepo, vectorStore *agent.VectorStore) {
	const pageSize = 100
	loaded := 0
	for offset := 0; ; offset += pageSize {
		embeddings, err := embeddingRepo.ListByType(ctx, model.EntityTypeTheme, pageSize, offset)
		if err != nil {
			log.Printf("[warmup] theme embeddings ListByType error: %v", err)
			break
		}
		if len(embeddings) == 0 {
			break
		}
		for _, emb := range embeddings {
			if emb.Embedding != nil {
				vec, err := llm.StringToEmbedding(*emb.Embedding)
				if err == nil && len(vec) > 0 {
					vectorStore.Upsert("theme:"+emb.EntityID, vec, map[string]string{
						"type":    "theme",
						"user_id": emb.UserID,
					})
					loaded++
				}
			}
		}
		if len(embeddings) < pageSize {
			break
		}
	}
	log.Printf("[warmup] theme embeddings: %d loaded into vector store", loaded)
}

// warmupConceptEmbeddings 从 embedding 表加载概念向量到内存
func warmupConceptEmbeddings(ctx context.Context, embeddingRepo *service.EmbeddingRepo, vectorStore *agent.VectorStore) {
	const pageSize = 200
	loaded := 0
	for offset := 0; ; offset += pageSize {
		embeddings, err := embeddingRepo.ListByType(ctx, model.EntityTypeConcept, pageSize, offset)
		if err != nil {
			log.Printf("[warmup] concept embeddings ListByType error: %v", err)
			break
		}
		if len(embeddings) == 0 {
			break
		}
		for _, emb := range embeddings {
			if emb.Embedding == nil {
				continue
			}
			vec, err := llm.StringToEmbedding(*emb.Embedding)
			if err != nil || len(vec) == 0 {
				continue
			}
			// concept 没有额外 metadata，只设 type
			vectorStore.Upsert("concept:"+emb.EntityID, vec, map[string]string{
				"type":    "concept",
				"user_id": emb.UserID,
			})
			loaded++
		}
		if len(embeddings) < pageSize {
			break
		}
	}
	log.Printf("[warmup] concept embeddings: %d loaded into vector store", loaded)
}

// warmupChunkEmbeddings 从 embedding 表加载 chunk 向量到内存
func warmupChunkEmbeddings(ctx context.Context, embeddingRepo *service.EmbeddingRepo, vectorStore *agent.VectorStore) {
	const pageSize = 200
	loaded := 0
	for offset := 0; ; offset += pageSize {
		embeddings, err := embeddingRepo.ListByType(ctx, "chunk", pageSize, offset)
		if err != nil {
			log.Printf("[warmup] chunk embeddings ListByType error: %v", err)
			break
		}
		if len(embeddings) == 0 {
			break
		}
		for _, emb := range embeddings {
			if emb.Embedding == nil {
				continue
			}
			vec, err := llm.StringToEmbedding(*emb.Embedding)
			if err != nil || len(vec) == 0 {
				continue
			}
			// 从 EntityID 解析 parent_type 和 parent_id
			// 格式: chunk:<parentType>:<parentID>/<index>
			parentType, parentID := parseChunkEntityID(emb.EntityID)
			vectorStore.Upsert(emb.EntityID, vec, map[string]string{
				"type":        "chunk",
				"parent_type": parentType,
				"parent_id":   parentID,
				"user_id":     emb.UserID,
			})
			loaded++
		}
		if len(embeddings) < pageSize {
			break
		}
	}
	log.Printf("[warmup] chunk embeddings: %d loaded into vector store", loaded)
}

// parseChunkEntityID 解析 chunk EntityID，返回 parentType 和 parentID
// 格式: chunk:<parentType>:<parentID>/<index>
func parseChunkEntityID(entityID string) (parentType, parentID string) {
	rest := entityID
	if idx := strings.Index(rest, ":"); idx > 0 {
		rest = rest[idx+1:] // "note:abc123/0"
	}
	if idx := strings.Index(rest, ":"); idx > 0 {
		parentType = rest[:idx] // "note"
		rest = rest[idx+1:]     // "abc123/0"
	}
	if idx := strings.LastIndex(rest, "/"); idx > 0 {
		parentID = rest[:idx]
	}
	return
}
