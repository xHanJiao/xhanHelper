package appinit

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
	"xhan-helper/internal/app"
	"xhan-helper/internal/config"
	"xhan-helper/internal/controller"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/mcp"
	"xhan-helper/internal/middleware"
	"xhan-helper/internal/monitor"
	"xhan-helper/internal/service"
)

// RegisterMonitorSnapshots 注册监控快照
func RegisterMonitorSnapshots(vs *agent.VectorStore, mcpManager *mcp.Manager, convRepo *service.ConversationRepo, themeStateRepo *service.ThemeStateRepo, cfg *config.Config, searchUsageRepo *service.SearchUsageRepo) {
	snapshots := &snapshotProvider{
		vectorStore:     vs,
		mcpManager:      mcpManager,
		convRepo:        convRepo,
		themeStateRepo:  themeStateRepo,
		searchUsageRepo: searchUsageRepo,
		searchProvider:  cfg.Search.Provider,
	}
	monitor.Register("vector_store", snapshots.VectorStore)
	monitor.Register("mcp", snapshots.MCP)
	monitor.Register("active_conversations", snapshots.ActiveConversations)
	monitor.Register("themes", snapshots.Themes)
	monitor.Register("search_usage", snapshots.SearchUsage)
}

// InitRouter 创建路由、注册所有 handler
func InitRouter(cfg *config.Config, repos *app.Repos, infra *app.Infra, llmClients llm.LLMClient, agents *AgentBundle, tools *ToolsBundle, mcpManager *mcp.Manager, vs *agent.VectorStore) *gin.Engine {
	billHandler := controller.NewBillHandler(repos.Bill, tools.MemExtractor)
	noteHandler := controller.NewNoteHandler(repos.Note)
	memHandler := controller.NewMemoryHandler(repos.Mem, infra.VectorStore)
	agentHandler := controller.NewAgentHandler(repos.Agent, llmClients)
	toolHandler := controller.NewToolHandler(repos.Tool)
	promptHandler := controller.NewPromptHandler(repos.Prompt)
	docHandler := controller.NewDocumentHandler(repos.Document)
	documentShareHandler := controller.NewDocumentShareHandler(repos.DocumentShare, cfg)
	mailboxService := service.NewMailboxService(repos.Mailbox, repos.ThemeState)
	mailboxHandler := controller.NewMailboxHandler(mailboxService, repos.Mailbox, repos.Conversation, repos.ThemeState, repos.Message)
	themeHandler := controller.NewThemeHandler(repos.ThemeState)
	missionHandler := controller.NewMissionHandler(repos.Mission, agents.MissionRunner)
	reflectionAdminHandler := controller.NewReflectionAdminHandler(repos.Trace)
	messageTraceHandler := controller.NewMessageTraceHandler(repos.Message)
	messageDebugHandler := controller.NewMessageDebugHandler(repos.Message, repos.Mem, repos.Note, repos.ThemeState)
	replayHandler := controller.NewReplayHandler(repos.Message, agents.ReplayRunner, infra.Embedder)

	router := controller.NewRouter(&controller.RouterConfig{
		Cfg:                  cfg,
		Orch:                 agents.Orch,
		UserRepo:             repos.User,
		ConvRepo:             repos.Conversation,
		MsgRepo:              repos.Message,
		AgentRepo:            repos.Agent,
		BillHandler:          billHandler,
		NoteHandler:          noteHandler,
		MemHandler:           memHandler,
		AgentHandler:         agentHandler,
		ToolHandler:          toolHandler,
		PromptHandler:        promptHandler,
		DocumentHandler:      docHandler,
		DocumentShareHandler: documentShareHandler,
		MemExtr:              tools.MemExtractor,
		ExtraMiddleware:      []gin.HandlerFunc{middleware.RequestLogger(log.Default())},
	})

	controller.RegisterReflectionRoutes(&controller.ReflectionRoutesConfig{
		R:                      router,
		UserRepo:               repos.User,
		MailboxHandler:         mailboxHandler,
		ThemeHandler:           themeHandler,
		MissionHandler:         missionHandler,
		ReflectionAdminHandler: reflectionAdminHandler,
		MessageTraceHandler:    messageTraceHandler,
		MessageDebugHandler:    messageDebugHandler,
		ReplayHandler:          replayHandler,
		EnableReplay:           cfg.Server.Mode == "debug",
		PendingFindingRepo:     repos.PendingFinding,
		ReflectionOrch:         agents.ReflectionOrch,
		MissionDiscoverer:      agents.MissionDiscoverer,
		CronRunner:             agents.CronRunner,
		AuthMiddleware:         middleware.Auth(repos.User),
		AdminMiddleware:        monitor.AdminOnly(cfg.Server.AdminUsers),
	})

	evalAdminHandler := controller.NewEvalAdminHandler("data/eval", llmClients, infra.Embedder)
	controller.RegisterEvalRoutes(controller.EvalRoutesConfig{
		R:               router,
		AuthMiddleware:  middleware.Auth(repos.User),
		AdminMiddleware: monitor.AdminOnly(cfg.Server.AdminUsers),
		Handler:         evalAdminHandler,
	})

	return router
}

// Serve 启动 HTTP 服务器并处理优雅关闭
func Serve(cfg *config.Config, router *gin.Engine, bgCancel context.CancelFunc, mcpManager *mcp.Manager, mcpIdleCtx context.Context) {
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

	bgCancel()
	mcpManager.Shutdown()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
	log.Println("server stopped")
}

// snapshotProvider 提供监控看板的快照数据
type snapshotProvider struct {
	vectorStore     *agent.VectorStore
	mcpManager      *mcp.Manager
	convRepo        *service.ConversationRepo
	themeStateRepo  *service.ThemeStateRepo
	searchUsageRepo *service.SearchUsageRepo
	searchProvider  string
}

func (p *snapshotProvider) VectorStore() any {
	if p.vectorStore == nil {
		return nil
	}
	return p.vectorStore.Stats()
}

func (p *snapshotProvider) MCP() any {
	if p.mcpManager == nil {
		return nil
	}
	return p.mcpManager.Stats()
}

func (p *snapshotProvider) ActiveConversations() any {
	if p.convRepo == nil {
		return 0
	}
	return p.convRepo.CountActive(context.Background(), time.Hour)
}

func (p *snapshotProvider) Themes() any {
	if p.themeStateRepo == nil {
		return nil
	}
	stats, err := p.themeStateRepo.GlobalStats(context.Background())
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return stats
}

func (p *snapshotProvider) SearchUsage() any {
	if p.searchUsageRepo == nil {
		return nil
	}
	return p.searchUsageRepo.Snapshot(context.Background(), p.searchProvider)
}
