package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"xhan-helper/internal/agent"
	"xhan-helper/internal/config"
	"xhan-helper/internal/controller"
	"xhan-helper/internal/idgen"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/mcp"
	"xhan-helper/internal/middleware"
	"xhan-helper/internal/model"
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

	if cfg.Server.Mode == "debug" {
		db.AutoMigrate(
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
		)
	}

	// LLM 客户端
	llmClient, err := llm.NewClient(cfg.LLM)
	if err != nil {
		log.Fatal("create llm client:", err)
	}
	strongClient, _ := llm.NewStrongClient(cfg.LLM)
	if strongClient != nil {
		log.Printf("[llm] strong model available: %s", cfg.LLM.StrongModel)
	}

	// Repos
	userRepo := service.NewUserRepo(db, cfg.Server.TokenTTLHours)
	convRepo := service.NewConversationRepo(db)
	msgRepo := service.NewMessageRepo(db)
	billRepo := service.NewBillRepo(db)
	memRepo := service.NewUserMemoryRepo(db)
	noteRepo := service.NewNoteRepo(db)
	agentRepo := service.NewAgentRepo(db)
	promptRepo := service.NewUserPromptRepo(db)
	toolRepo := service.NewToolRepo(db)
	embeddingRepo := service.NewEmbeddingRepo(db)

	// Tool Registry + Tools
	reg := tool.NewRegistry()
	reg.Register(tool.NewLoggingWrapper(tool.NewBookkeepingTool(billRepo), log.Default()))
	reg.Register(tool.NewLoggingWrapper(tool.NewDatetimeTool(), log.Default()))
	reg.Register(tool.NewLoggingWrapper(tool.NewCalculatorTool(), log.Default()))

	// Vector Store
	embedder := llm.NewEmbedder(cfg.Embedding.APIKey, cfg.Embedding.BaseURL, cfg.Embedding.Model)
	vectorStore := agent.NewVectorStore()

	// Seed 内置工具到 tool_registries 表（幂等）
	if err := tool.SeedBuiltinTools(context.Background(), toolRepo, embedder, embeddingRepo); err != nil {
		log.Printf("[seed] SeedBuiltinTools error: %v", err)
	}

	// tool_retriever: 用关键词策略检索工具 + Registry 内存搜索（MCP 占位工具等）
	toolRetriever := tool.NewToolRetriever(tool.NewSimpleKeywordStrategy(toolRepo))
	toolRetriever.SetRegistrySearcher(reg)
	reg.Register(tool.NewLoggingWrapper(toolRetriever, log.Default()))

	reg.Register(tool.NewLoggingWrapper(tool.NewNoteTool(noteRepo, vectorStore, embedder, embeddingRepo), log.Default()))
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
		log.Printf("[mcp] proxy tool registered: %s", s.Name)
	}

	// 后台任务共享 context，关闭时统一取消
	bgCtx, bgCancel := context.WithCancel(context.Background())

	// Context Sources
	memSource := agent.NewMemorySource(vectorStore, memRepo, embeddingRepo)
	noteSource := agent.NewNoteSource(vectorStore, noteRepo, embeddingRepo)

	// Warmup 必须完成后再启动服务
	memSource.Warmup(bgCtx, embedder)
	noteSource.Warmup(bgCtx, embedder)

	// Orchestrator
	retriever := agent.NewRetriever(memSource, noteSource)
	orch := agent.New(llmClient, reg, strongClient, retriever, msgRepo, convRepo, embedder, vectorStore, embeddingRepo, cfg.LLM.MaxIterations)

	// Memory Extractor (后台异步)
	memExtractor := agent.NewMemoryExtractor(llmClient, memRepo, vectorStore, embedder, msgRepo, convRepo, embeddingRepo, cfg.Server.MemoryDupeThreshold)
	memScheduler := agent.NewMemoryScheduler(memExtractor, convRepo)
	go memScheduler.Start(bgCtx, cfg.Server.MemoryAutoExtract)
	orch.SetPromptResolver(promptRepo)

	// MCP idle reaper
	mcpManager.StartIdleReaper(bgCtx)

	// Handlers
	billHandler := controller.NewBillHandler(billRepo, memExtractor)
	noteHandler := controller.NewNoteHandler(noteRepo)
	memHandler := controller.NewMemoryHandler(memRepo, vectorStore)
	agentHandler := controller.NewAgentHandler(agentRepo, llmClient)
	toolHandler := controller.NewToolHandler(toolRepo)
	promptHandler := controller.NewPromptHandler(promptRepo)

	// Router
	router := controller.NewRouter(cfg, orch, userRepo, convRepo, msgRepo, agentRepo, billHandler, noteHandler, memHandler, agentHandler, toolHandler, promptHandler, memExtractor, middleware.RequestLogger(log.Default()))

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
