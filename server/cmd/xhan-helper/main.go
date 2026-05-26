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
	"xhan-helper/internal/agent/retrieval"
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
	"xhan-helper/internal/util"
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

	// 注入 panic 采集回调
	util.PanicHandler = monitor.RecordPanic

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
		&model.Document{},
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
	docRepo := service.NewDocumentRepo(db)

	// Tool Registry + Tools
	reg := tool.NewRegistry()
	reg.Register(tool.NewLoggingWrapper(tool.NewBookkeepingTool(billRepo), log.Default()))
	reg.Register(tool.NewLoggingWrapper(tool.NewDatetimeTool(), log.Default()))
	reg.Register(tool.NewLoggingWrapper(tool.NewCalculatorTool(), log.Default()))
	reg.Register(tool.NewLoggingWrapper(tool.NewDocumentTool(docRepo), log.Default()))

	// Vector Store
	embedder := llm.NewEmbedder(cfg.Embedding.APIKey, cfg.Embedding.BaseURL, cfg.Embedding.Model)
	vectorStore := agent.NewVectorStore()
	vsAdapter := agent.NewVectorSearchAdapter(vectorStore)

	// 核心依赖非 nil 校验：启动时 fail-fast
	if vectorStore == nil {
		log.Fatal("vectorStore must not be nil")
	}
	if embeddingRepo == nil {
		log.Fatal("embeddingRepo must not be nil")
	}

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
	themeAnalyst := agent.NewThemeAnalyst(llmClient, retrievalTools, themeStore, traceRepo, cfg.Reflection.AnalystMaxIterations)
	scorerTool := agent.NewScorer(llmClient, traceRepo)
	themeEmbedder := agent.NewThemeEmbedder(infra, conceptEmbedRepo, embeddingRepo)
	themeDiscoverer := agent.NewThemeDiscoverer(llmClients, infra, retrievalTools, themeStore, themeEmbedder, traceRepo, cfg.Reflection.DiscoverMaxIterations, cfg.Reflection.ClusterThreshold)
	mailboxTool := tool.NewMailboxTool(mailboxRepo)
	themeMgmtTool := tool.NewThemeTool(themeStateRepo, themeEmbedder)

	reflectionReg := tool.NewRegistry()
	reflectionReg.Register(tool.NewLoggingWrapper(themeAnalyst, log.Default()))
	reflectionReg.Register(tool.NewLoggingWrapper(scorerTool, log.Default()))
	reflectionReg.Register(tool.NewLoggingWrapper(agent.NewSecretary(llmClient, traceRepo), log.Default()))
	reflectionReg.Register(tool.NewLoggingWrapper(themeDiscoverer, log.Default()))
	reflectionReg.Register(tool.NewLoggingWrapper(mailboxTool, log.Default()))
	reflectionReg.Register(tool.NewLoggingWrapper(themeMgmtTool, log.Default()))

	// Memory Extractor（合并到对话后处理流程）
	memExtractor := agent.NewMemoryExtractor(&agent.MemoryExtractorConfig{
		LLMClient:     llmClient,
		MemRepo:       memRepo,
		VectorStore:   infra.VectorStore,
		Embedder:      embedder,
		MsgRepo:       msgRepo,
		ConvRepo:      convRepo,
		EmbeddingRepo: embeddingRepo,
		DupeThreshold: cfg.Server.MemoryDupeThreshold,
		ChunkBuilder:  chunkBuilder,
		ConceptEmbed:  conceptEmbedService,
	})

	commitmentTimer := agent.NewCommitmentTimer(commitmentRepo, mailboxRepo)

	// 注册提醒工具到主 Registry（对话中 LLM 直接调用）
	reg.Register(tool.NewLoggingWrapper(tool.NewMemorySearchTool(infra.VectorStore, memRepo, embedder, cfg.Retrieval.FreshnessDecay), log.Default()))
	reg.Register(tool.NewLoggingWrapper(tool.NewMessageSearchTool(msgRepo, convRepo), log.Default()))
	reg.Register(tool.NewLoggingWrapper(tool.NewReminderTool(commitmentRepo, commitmentTimer), log.Default()))

	// ===== Phase 6: ReflectionOrchestrator + CronRunner =====
	reflectionOrch := agent.NewReflectionOrchestrator(&agent.ReflectionOrchestratorConfig{
		FlashClient:        llmClient,
		ReflectionReg:      reflectionReg,
		TraceRepo:          traceRepo,
		ThemeStore:         themeStore,
		MailboxRepo:        mailboxRepo,
		MsgRepo:            msgRepo,
		NoteRepo:           noteRepo,
		MemRepo:            memRepo,
		PendingFindingRepo: pendingFindingRepo,
		MaxIterations:      cfg.Reflection.LaunchMaxIterations,
		MaxDeliveryRetries: cfg.Reflection.MaxDeliveryRetries,
		DraftExpireDays:    cfg.Reflection.DraftExpireDays,
		ActiveDurationDays: cfg.Reflection.ActiveDurationDays,
	})
	stateUpdater := agent.NewStateUpdater(llmClient, msgRepo, convRepo, themeStore, pendingFindingRepo)
	cronRunner := agent.NewCronRunner(&agent.CronRunnerConfig{
		Orch:              reflectionOrch,
		StateUpdater:      stateUpdater,
		UserRepo:          userRepo,
		MemExtractor:      memExtractor,
		CommitTimer:       commitmentTimer,
		ConvRepo:          convRepo,
		DelayedTaskRepo:   delayedTaskRepo,
		DiscoverSchedules: cfg.Reflection.DailySchedules,
		DeliverInterval:   time.Duration(cfg.Reflection.DeliverIntervalMinutes) * time.Minute,
		PostChatDelay:     time.Duration(cfg.Reflection.PostChatDelaySeconds) * time.Second,
		MemoryAutoExtract: cfg.Server.MemoryAutoExtract,
	})
	themeDiscoverer.SetUpgradeThresholds(cfg.Reflection.DraftNominateCountThreshold, cfg.Reflection.DraftSpanDaysThreshold)
	// 后台任务共享 context，关闭时统一取消
	bgCtx, bgCancel := context.WithCancel(context.Background())

	// Context Sources
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

	memSource := retrieval.NewMemorySource(vectorStore, memRepo, embeddingRepo, memRanker)
	noteSource := retrieval.NewNoteSource(vectorStore, noteRepo, embeddingRepo, noteRanker)
	historySource := retrieval.NewHistorySource(vectorStore, msgRepo, embeddingRepo, historyRanker)

	// Warmup 必须完成后再启动服务
	memSource.Warmup(bgCtx, embedder)
	noteSource.Warmup(bgCtx, embedder)
	historySource.Warmup(bgCtx, embedder)
	// Theme embedding warmup：从 embedding 表加载到向量库，确保去重重启后有效
	warmupThemeEmbeddings(bgCtx, embeddingRepo, vectorStore, conceptEmbedRepo, themeStateRepo)
	warmupChunkEmbeddings(bgCtx, embeddingRepo, vectorStore)
	warmupConceptEmbeddings(bgCtx, embeddingRepo, vectorStore)

	// 补全缺失向量的旧主题（ThemeEmbedder 上线前创建的主题）
	backfillThemeVectors(bgCtx, themeStateRepo, conceptEmbedRepo, themeEmbedder)

	// Orchestrator
	retriever := retrieval.NewRetriever(memSource, noteSource)
	pb := agent.NewSystemPromptBuilder(
		agent.DefaultPromptConfig(),
		agent.BaseSystemPrompt,
		reg, retriever, themeStateRepo, convRepo, promptRepo, vectorStore,
	)
	orch := agent.New(&agent.OrchestratorConfig{
		FlashClient:   llmClient,
		Registry:      reg,
		ProClient:     strongClient,
		PromptBuilder: pb,
		MsgRepo:       msgRepo,
		ConvRepo:      convRepo,
		Embedder:      embedder,
		VectorStore:   vectorStore,
		EmbeddingRepo: embeddingRepo,
		MaxIterations: cfg.LLM.MaxIterations,
		Temperature:   cfg.LLM.DefaultTemperature,
		MaxTokens:     cfg.LLM.DefaultMaxTokens,
		OnChatEnd:     func(userID, convID string) { cronRunner.NotifyChatEnd(userID, convID) },
	})

	// 启动反思调度器
	go cronRunner.Start(bgCtx)

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
	monitor.Register("themes", snapshots.Themes)

	// MCP idle reaper
	mcpManager.StartIdleReaper(bgCtx)

	// Handlers
	billHandler := controller.NewBillHandler(billRepo, memExtractor)
	noteHandler := controller.NewNoteHandler(noteRepo)
	memHandler := controller.NewMemoryHandler(memRepo, vsAdapter)
	agentHandler := controller.NewAgentHandler(agentRepo, llmClient)
	toolHandler := controller.NewToolHandler(toolRepo)
	promptHandler := controller.NewPromptHandler(promptRepo)
	docHandler := controller.NewDocumentHandler(docRepo)
	mailboxService := service.NewMailboxService(mailboxRepo, themeStateRepo)
	mailboxHandler := controller.NewMailboxHandler(mailboxService, mailboxRepo, convRepo, themeStateRepo, msgRepo)
	themeHandler := controller.NewThemeHandler(themeStateRepo)
	reflectionAdminHandler := controller.NewReflectionAdminHandler(traceRepo)
	messageTraceHandler := controller.NewMessageTraceHandler(msgRepo)

	// Router
	router := controller.NewRouter(&controller.RouterConfig{
		Cfg:             cfg,
		Orch:            orch,
		UserRepo:        userRepo,
		ConvRepo:        convRepo,
		MsgRepo:         msgRepo,
		AgentRepo:       agentRepo,
		BillHandler:     billHandler,
		NoteHandler:     noteHandler,
		MemHandler:      memHandler,
		AgentHandler:    agentHandler,
		ToolHandler:     toolHandler,
		PromptHandler:   promptHandler,
		DocumentHandler: docHandler,
		MemExtr:         memExtractor,
		ExtraMiddleware: []gin.HandlerFunc{middleware.RequestLogger(log.Default())},
	})

	// 注册 Phase 6 反思系统路由
	controller.RegisterReflectionRoutes(&controller.ReflectionRoutesConfig{
		R:                      router,
		UserRepo:               userRepo,
		MailboxHandler:         mailboxHandler,
		ThemeHandler:           themeHandler,
		ReflectionAdminHandler: reflectionAdminHandler,
		MessageTraceHandler:    messageTraceHandler,
		PendingFindingRepo:     pendingFindingRepo,
		ReflectionOrch:         reflectionOrch,
		CronRunner:             cronRunner,
		AuthMiddleware:         middleware.Auth(userRepo),
		AdminMiddleware:        monitor.AdminOnly(cfg.Server.AdminUsers),
	})

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
func warmupThemeEmbeddings(ctx context.Context, embeddingRepo *service.EmbeddingRepo, vectorStore *agent.VectorStore, conceptEmbedRepo *service.ConceptEmbeddingRepo, themeStateRepo *service.ThemeStateRepo) {
	const pageSize = 100
	loaded := 0

	// 预加载：concept_embedding ID→Value 映射
	conceptIDToValue := make(map[string]string)
	if conceptEmbedRepo != nil {
		allConcepts, cerr := conceptEmbedRepo.ListByTypeAll(ctx, "theme")
		if cerr != nil {
			log.Printf("[warmup] concept list error: %v", cerr)
		} else {
			for _, c := range allConcepts {
				conceptIDToValue[c.ID] = c.Value
			}
		}
	}

	// 第一遍：收集所有唯一 userID + 按 userID 加载活跃主题
	userThemeMap := make(map[string]map[string]string) // userID → name → themeID
	seenUsers := make(map[string]bool)
	{
		for offset := 0; ; offset += pageSize {
			embeddings, err := embeddingRepo.ListByType(ctx, model.EntityTypeTheme, pageSize, offset)
			if err != nil || len(embeddings) == 0 {
				break
			}
			for _, emb := range embeddings {
				if emb.UserID != "" && !seenUsers[emb.UserID] {
					seenUsers[emb.UserID] = true
					themes, terr := themeStateRepo.ListActive(ctx, emb.UserID)
					if terr == nil {
						nameMap := make(map[string]string)
						for _, t := range themes {
							nameMap[t.Name] = t.ID
						}
						userThemeMap[emb.UserID] = nameMap
					}
				}
			}
			if len(embeddings) < pageSize {
				break
			}
		}
	}

	// 第二遍：加载向量到 VectorStore，补上 theme_id
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
					meta := map[string]string{
						"type":    "theme",
						"user_id": emb.UserID,
					}
					// 通过 concept_embedding.value 反查 theme_state，补上 theme_id
					if conceptValue, ok := conceptIDToValue[emb.EntityID]; ok {
						meta["concept_value"] = conceptValue
						nameMap := userThemeMap[emb.UserID]
						if tid, ok := nameMap[conceptValue]; ok {
							meta["theme_id"] = tid
						} else {
							for name, tid := range nameMap {
								if strings.HasPrefix(conceptValue, name+" ") || conceptValue == name {
									meta["theme_id"] = tid
									break
								}
							}
						}
					}
					vectorStore.Upsert("theme:"+emb.EntityID, vec, meta)
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

// backfillThemeVectors 补全缺失向量的旧主题（ThemeEmbedder 上线前创建的主题，无向量）
func backfillThemeVectors(ctx context.Context, themeStateRepo *service.ThemeStateRepo, conceptEmbedRepo *service.ConceptEmbeddingRepo, themeEmbedder *agent.ThemeEmbedder) {
	activeThemes, err := themeStateRepo.ListAllActive(ctx)
	if err != nil {
		log.Printf("[backfill] list active themes error: %v", err)
		return
	}
	missing := 0
	for _, t := range activeThemes {
		exists, err := conceptEmbedRepo.Exists(ctx, "theme", t.Name+" "+t.Description, t.UserID)
		if err != nil {
			log.Printf("[backfill] Exists error for theme %q: %v", t.Name, err)
			continue
		}
		if exists {
			continue
		}
		log.Printf("[backfill] embedding missing theme %q (id=%s) for user %s", t.Name, t.ID[:8], t.UserID[:8])
		themeEmbedder.EnsureEmbedded(ctx, t.UserID, t.ID, t.Name, t.Description)
		missing++
	}
	if missing > 0 {
		log.Printf("[backfill] %d themes backfilled, waiting for async embed...", missing)
		time.Sleep(2 * time.Second)
	} else {
		log.Printf("[backfill] all %d active themes have vectors", len(activeThemes))
	}
}
