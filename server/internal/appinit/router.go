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
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"xhan-helper/internal/activity"
	"xhan-helper/internal/agent"
	"xhan-helper/internal/app"
	"xhan-helper/internal/backup"
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

// AssertLocalEnv 本地模式启动断言（第 4 期）：local_env / APP_ENV / 数据根 / token 前缀
// 四者必须配套。任何一处错配都意味着「可能正在写错库」，直接致命退出而不是静默继续。
// 非本地模式不检查。
func AssertLocalEnv(cfg *config.Config) error {
	if !cfg.Server.LocalSingleUser {
		return nil
	}
	env := cfg.Server.LocalEnv
	if env != "dev" && env != "prod" {
		return fmt.Errorf("server.local_env 必须是 dev 或 prod（当前 %q）", env)
	}
	wantAppEnv := "local"
	rootName := "xhanHelper"
	if env == "dev" {
		wantAppEnv = "local.dev"
		rootName = "xhanHelper-dev"
	}
	if got := os.Getenv("APP_ENV"); got != wantAppEnv {
		return fmt.Errorf("local_env=%s 要求 APP_ENV=%s（当前 %q）", env, wantAppEnv, got)
	}
	if cfg.DB.Driver != "sqlite" {
		return fmt.Errorf("本地模式要求 db.driver=sqlite（当前 %q）", cfg.DB.Driver)
	}
	// 数据根：库与图床必须都在 <HOME>/Library/Application Support/<rootName>/ 下
	wantSuffix := "/" + rootName + "/xhan_helper.db"
	if !strings.HasSuffix(cfg.DB.Database, wantSuffix) {
		return fmt.Errorf("local_env=%s 要求库路径以 %s 结尾（当前 %s）", env, wantSuffix, cfg.DB.Database)
	}
	rootDir := strings.TrimSuffix(cfg.DB.Database, "/xhan_helper.db")
	if !strings.HasPrefix(cfg.Storage.ImageDir, rootDir+"/") {
		return fmt.Errorf("local_env=%s 要求 image_dir 在 %s/ 下（当前 %s）", env, rootDir, cfg.Storage.ImageDir)
	}
	// token 前缀：app 注入的是 "<env>:<random>"，防串了构建注入的 token
	prefix := env + ":"
	if !strings.HasPrefix(cfg.Server.LocalToken, prefix) {
		return fmt.Errorf("local_env=%s 要求 local_token 以 %q 开头（app 注入形如 %s<random>）", env, prefix, prefix)
	}
	log.Printf("[local] env=%s app_env=%s db=%s", env, wantAppEnv, cfg.DB.Database)
	return nil
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
	if err := AssertLocalEnv(cfg); err != nil {
		return "", err
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
func InitRouter(cfg *config.Config, repos *app.Repos, infra *app.Infra, llmClients *app.LLMClients, agents *AgentBundle, tools *ToolsBundle, mcpManager *mcp.Manager, vs *agent.VectorStore, localUserID string, backupSvc *backup.Service, activityStore *activity.Store) *gin.Engine {
	localAuth := middleware.LocalAuth{
		Enabled: cfg.Server.LocalSingleUser,
		UserID:  localUserID,
		Token:   cfg.Server.LocalToken,
		Env:     cfg.Server.LocalEnv,
	}
	authMW := middleware.Auth(repos.User, localAuth)
	billHandler := controller.NewBillHandler(repos.Bill)
	noteHandler := controller.NewNoteHandler(repos.Note, tools.EntityPurger)
	memHandler := controller.NewMemoryHandler(repos.Mem, tools.EntityPurger)
	agentHandler := controller.NewAgentHandler(repos.Agent, llmClients.BgFlash)
	toolHandler := controller.NewToolHandler(repos.Tool)
	promptHandler := controller.NewPromptHandler(repos.Prompt)
	docHandler := controller.NewDocumentHandler(repos.Document)
	annotationReplier := agent.NewAnnotationReplier(&agent.AnnotationReplierConfig{
		AnnoRepo:     repos.DocumentAnnotation,
		DocRepo:      repos.Document,
		ReplyRepo:    repos.DocumentAnnotationReply,
		MsgRepo:      repos.Message,
		SummaryRepo:  repos.ConversationSummary,
		Client:       llmClients.Flash, // 批注回复固定 Flash（与主聊天同源）
		Registry:     tools.Registry,
		ToolExcludes: cfg.Chat.ToolExcludes, // 与主循环/plan 模式同源
	})
	annotationHandler := controller.NewAnnotationHandler(repos.DocumentAnnotation, repos.Document, repos.DocumentAnnotationReply, annotationReplier)
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
		BackupService:        backupSvc,
		Activity:             activityStore,
		NotifySessionEnd: func(userID, convID, userMsgID string) {
			if cfg.Server.BackgroundTasksDisabled() {
				return
			}
			agents.CronRunner.NotifySessionEnd(userID, convID, userMsgID)
		},
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
	// 父进程（app）崩溃时自退，避免孤儿进程占端口（仅本地模式）。
	WatchParent(cfg.Server.LocalSingleUser, 5*time.Second)
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
