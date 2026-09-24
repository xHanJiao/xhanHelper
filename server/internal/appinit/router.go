package appinit

import (
	"context"
	"fmt"
	"log"
	"net"
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
	"xhan-helper/internal/mcp"
	"xhan-helper/internal/middleware"
	"xhan-helper/internal/monitor"
	"xhan-helper/internal/service"
)

// RegisterMonitorSnapshots 注册监控快照
func RegisterMonitorSnapshots(vs *agent.VectorStore, mcpManager *mcp.Manager, convRepo *service.ConversationRepo, themeStateRepo *service.ThemeStateRepo) {
	snapshots := &snapshotProvider{
		vectorStore:    vs,
		mcpManager:     mcpManager,
		convRepo:       convRepo,
		themeStateRepo: themeStateRepo,
	}
	monitor.Register("vector_store", snapshots.VectorStore)
	monitor.Register("mcp", snapshots.MCP)
	monitor.Register("active_conversations", snapshots.ActiveConversations)
	monitor.Register("themes", snapshots.Themes)
}

// EnsureLocalUser 本地单用户模式：以「库里的用户」为身份锚点——
// 有且仅有一个就用它（迁移进来的 xhan 原样认领）；空库才新建（默认名 me）；
// 多于一个视为未剪枝，返回错误（由调用方致命退出）。返回 user_id，并把它加进
// admin 列表（内存）——monitor.AdminOnly 依赖 AdminUsers 非空，勿当冗余清理。
// 非本地模式返回 ("", nil)。
func EnsureLocalUser(cfg *config.Config, userRepo *service.UserRepo) (string, error) {
	if !cfg.Server.LocalSingleUser {
		return "", nil
	}
	if cfg.Server.LocalToken == "" {
		return "", fmt.Errorf("local_single_user 需要 local_token（经 XHAN_LOCAL_TOKEN 注入）")
	}

	ctx := context.Background()
	users, err := userRepo.ListAll(ctx)
	if err != nil {
		return "", fmt.Errorf("list users: %w", err)
	}

	var userID, username string
	switch len(users) {
	case 0:
		name := cfg.Server.LocalUsername
		if name == "" {
			name = "me"
		}
		u, err := userRepo.EnsureByUsername(ctx, name)
		if err != nil {
			return "", fmt.Errorf("create local user %q: %w", name, err)
		}
		userID, username = u.ID, u.Username
		log.Printf("[local] empty db, created local user %q", username)
	case 1:
		userID, username = users[0].ID, users[0].Username
	default:
		return "", fmt.Errorf("本地单用户模式要求库里只有一个用户，当前 %d 个；请先剪枝（见 docs/design/local-sqlite/design.md 第七节）", len(users))
	}

	cfg.Server.AdminUsers = append(cfg.Server.AdminUsers, userID)
	log.Printf("[local] single-user mode: username=%s user_id=%s (auto admin)", username, userID)
	return userID, nil
}

// InitRouter 创建路由、注册所有 handler
func InitRouter(cfg *config.Config, repos *app.Repos, infra *app.Infra, llmClients *app.LLMClients, agents *AgentBundle, tools *ToolsBundle, mcpManager *mcp.Manager, vs *agent.VectorStore, localUserID string) *gin.Engine {
	localAuth := middleware.LocalAuth{
		Enabled: cfg.Server.LocalSingleUser,
		UserID:  localUserID,
		Token:   cfg.Server.LocalToken,
	}
	authMW := middleware.Auth(repos.User, localAuth)
	billHandler := controller.NewBillHandler(repos.Bill)
	noteHandler := controller.NewNoteHandler(repos.Note, tools.EntityPurger)
	memHandler := controller.NewMemoryHandler(repos.Mem, tools.EntityPurger)
	factHandler := controller.NewFactHandler(repos.Fact)
	agentHandler := controller.NewAgentHandler(repos.Agent, llmClients.BgFlash)
	toolHandler := controller.NewToolHandler(repos.Tool)
	promptHandler := controller.NewPromptHandler(repos.Prompt)
	docHandler := controller.NewDocumentHandler(repos.Document)
	annotationHandler := controller.NewAnnotationHandler(repos.DocumentAnnotation, repos.Document)
	documentShareHandler := controller.NewDocumentShareHandler(repos.DocumentShare, cfg)
	messageShareHandler := controller.NewMessageShareHandler(repos.MessageShare, cfg, repos.Conversation)
	summaryHandler := controller.NewSummaryHandler(repos.ConversationSummary, repos.Message)
	reminderHandler := controller.NewReminderHandler(repos.Reminder, repos.Conversation, repos.Message)
	themeHandler := controller.NewThemeHandler(repos.ThemeState)
	reflectionAdminHandler := controller.NewReflectionAdminHandler(repos.Trace)
	messageTraceHandler := controller.NewMessageTraceHandler(repos.Message)
	messageDebugHandler := controller.NewMessageDebugHandler(repos.Message, repos.Mem, repos.Note, repos.ThemeState)
	replayHandler := controller.NewReplayHandler(repos.Message, agents.ReplayRunner, infra.Embedder)
	imageHandler := controller.NewImageHandler(infra.ImageStore)

	router := controller.NewRouter(&controller.RouterConfig{
		Cfg:                  cfg,
		Orch:                 agents.Orch,
		UserRepo:             repos.User,
		ConvRepo:             repos.Conversation,
		PlanRepo:             repos.ConversationPlan,
		MsgRepo:              repos.Message,
		AgentRepo:            repos.Agent,
		BillHandler:          billHandler,
		NoteHandler:          noteHandler,
		MemHandler:           memHandler,
		FactHandler:          factHandler,
		AgentHandler:         agentHandler,
		ToolHandler:          toolHandler,
		PromptHandler:        promptHandler,
		DocumentHandler:      docHandler,
		AnnotationHandler:    annotationHandler,
		DocumentShareHandler: documentShareHandler,
		MessageShareHandler:  messageShareHandler,
		SummaryHandler:       summaryHandler,
		ReminderHandler:      reminderHandler,
		ImageHandler:         imageHandler,
		ClientBridge:         agents.ClientBridge,
		Destroyer:            agents.Destroyer,
		LlmCostRepo:          repos.LlmCost,
		LlmPriceRepo:         repos.LlmPrice,
		ExtraMiddleware:      []gin.HandlerFunc{middleware.RequestLogger(log.Default())},
		Ready:                agents.Ready,
		AuthMiddleware:       authMW,
	})

	controller.RegisterReflectionRoutes(&controller.ReflectionRoutesConfig{
		R:                      router,
		UserRepo:               repos.User,
		ThemeHandler:           themeHandler,
		ReflectionAdminHandler: reflectionAdminHandler,
		MessageTraceHandler:    messageTraceHandler,
		MessageDebugHandler:    messageDebugHandler,
		ReplayHandler:          replayHandler,
		EnableReplay:           true, // admin 路由已有 AdminMiddleware 保护，生产环境开放
		CronRunner:             agents.CronRunner,
		AuthMiddleware:         authMW,
		AdminMiddleware:        monitor.AdminOnly(cfg.Server.AdminUsers),
	})

	evalAdminHandler := controller.NewEvalAdminHandler("data/eval", llmClients.Flash, infra.Embedder)
	evalRunHandler := controller.NewEvalRunHandler("data/eval")
	controller.RegisterEvalRoutes(controller.EvalRoutesConfig{
		R:               router,
		AuthMiddleware:  authMW,
		AdminMiddleware: monitor.AdminOnly(cfg.Server.AdminUsers),
		Handler:         evalAdminHandler,
		RunHandler:      evalRunHandler,
	})

	controller.RegisterCostRoutes(controller.CostRoutesConfig{
		R:               router,
		AuthMiddleware:  authMW,
		AdminMiddleware: monitor.AdminOnly(cfg.Server.AdminUsers),
		Repo:            repos.LlmCost,
		PriceRepo:       repos.LlmPrice,
	})

	controller.RegisterLLMIOAuditRoutes(controller.LLMIOAuditRoutesConfig{
		R:               router,
		AuthMiddleware:  authMW,
		AdminMiddleware: monitor.AdminOnly(cfg.Server.AdminUsers),
		Repo:            repos.LLMCallIO,
	})

	controller.RegisterSubAgentAdminRoutes(controller.SubAgentAdminRoutesConfig{
		R:               router,
		AuthMiddleware:  authMW,
		AdminMiddleware: monitor.AdminOnly(cfg.Server.AdminUsers),
		ConvRepo:        repos.Conversation,
		MsgRepo:         repos.Message,
		CostRepo:        repos.LlmCost,
	})

	return router
}

// Serve 启动 HTTP 服务器并处理优雅关闭
func Serve(cfg *config.Config, router *gin.Engine, bgCancel context.CancelFunc, mcpManager *mcp.Manager, mcpIdleCtx context.Context) {
	addr := net.JoinHostPort(cfg.Server.Host, strconv.Itoa(cfg.Server.Port))
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
	vectorStore    *agent.VectorStore
	mcpManager     *mcp.Manager
	convRepo       *service.ConversationRepo
	themeStateRepo *service.ThemeStateRepo
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
