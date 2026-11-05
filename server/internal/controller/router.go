package controller

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
	"sync/atomic"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"

	"xhan-helper/internal/activity"
	"xhan-helper/internal/agent"
	"xhan-helper/internal/backup"
	"xhan-helper/internal/config"
	"xhan-helper/internal/monitor"
	"xhan-helper/internal/service"
	"xhan-helper/internal/view"
	"xhan-helper/internal/web"
)

// trustedProxyCIDRs — 只信任回环反代（nginx）。直连客户端用 RemoteAddr，
// 避免伪造 X-Forwarded-For 绕过登录限流。
var trustedProxyCIDRs = []string{"127.0.0.1", "::1"}

// RouterConfig — 路由注册所需的所有依赖
type RouterConfig struct {
	Cfg                  *config.Config
	Orch                 *agent.Orchestrator
	UserRepo             *service.UserRepo
	ConvRepo             *service.ConversationRepo
	PlanRepo             *service.ConversationPlanRepo
	MsgRepo              *service.MessageRepo
	ArtifactRepo         *service.ArtifactRepo
	AgentRepo            *service.AgentRepo
	BillHandler          *BillHandler
	NoteHandler          *NoteHandler
	MemHandler           *MemoryHandler
	AgentHandler         *AgentHandler
	ToolHandler          *ToolHandler
	PromptHandler        *PromptHandler
	DocumentHandler      *DocumentHandler
	AnnotationHandler    *AnnotationHandler
	DocumentShareHandler *DocumentShareHandler
	MessageShareHandler  *MessageShareHandler
	SummaryHandler       *SummaryHandler
	ReminderHandler      *ReminderHandler
	EventHandler         *EventHandler
	ImageHandler         *ImageHandler
	ArtifactHandler      *ArtifactHandler
	ClientBridge         *agent.ClientToolBridge
	Destroyer            *agent.EphemeralDestroyer
	LlmCostRepo          *service.LlmCostRepo
	LlmPriceRepo         *service.LlmPriceRepo
	ExtraMiddleware      []gin.HandlerFunc
	Ready                *atomic.Bool    // 后台向量预热完成标志；nil 视为已就绪
	AuthMiddleware       gin.HandlerFunc // Bearer 认证（含本地单用户直通），由装配层构造一次
	BackupService        *backup.Service // 仅本地模式创建；nil 则不注册备份路由
	Activity             *activity.Store // 客户端埋点上报落点；nil（采集关闭）时该路由返回 503
	// NotifySessionEnd 补挂 session_end 后处理任务（会话转无痕时用，令现有销毁链路接管）。
	// 由装配层注入，含后台任务开关守卫（关闭后台任务时短路）。
	NotifySessionEnd func(userID, convID, userMsgID string)
}

// NewRouter 注册所有路由
func NewRouter(rc *RouterConfig) *gin.Engine {

	// 关闭 gin 自动重定向（尾斜杠/大小写修正会返回 301，而 301 响应不带 CORS 头，
	// 跨域直连下浏览器报误导性的 "Cross-origin redirection denied"）——错误路径直接 404。
	r := gin.New()
	// 只信任回环反代（nginx）：gin 从右往左取 X-Forwarded-For 里第一个非受信地址，
	// 客户端自己伪造的左侧地址不会影响 ClientIP()/限流 key。
	if err := r.SetTrustedProxies(trustedProxyCIDRs); err != nil {
		panic("set trusted proxies: " + err.Error())
	}
	r.RedirectTrailingSlash = false
	r.RedirectFixedPath = false
	r.Use(gin.Logger(), gin.Recovery())

	for _, m := range rc.ExtraMiddleware {
		r.Use(m)
	}

	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{"GET", "POST", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Authorization", "Content-Type"},
		AllowCredentials: false,
	}))

	// 无需认证 — 账号注册/登录（共享一个失败限流器，按 IP+用户名 计数）
	authLimiter := newAuthFailureLimiter()
	r.POST("/api/auth/register", registerUser(rc.UserRepo, rc.Cfg.Server.RegistrationEnabled, rc.Cfg.Server.AllowedUsers, rc.Cfg.Server.AdminUsers, authLimiter))
	r.POST("/api/auth/login", loginUser(rc.UserRepo, rc.Cfg.Server.AdminUsers, authLimiter))
	// 公开分享
	r.GET("/api/share/messages/:token", rc.MessageShareHandler.ViewSharedMessages)
	r.GET("/api/share/artifacts/:token", rc.ArtifactHandler.ViewSharedArtifact)
	r.GET("/api/share/:token", rc.DocumentShareHandler.ViewSharedDocument)
	// 图床公开读：仅服务 Visibility=public 的图（文档配图/分享页/PDF 导出；<img> 带不了请求头）
	r.GET("/images/:file", rc.ImageHandler.ServePublic)
	// 健康分级（无认证）：starting = HTTP 已就绪、后台向量预热中；ready = 预热完成。
	// 有意不探数据库：探针只回答「进程/HTTP 是否活着」，库连接状态不该由这个公开接口承担。
	r.GET("/api/health", func(c *gin.Context) {
		status := "ready"
		if rc.Ready != nil && !rc.Ready.Load() {
			status = "starting"
		}
		c.JSON(http.StatusOK, gin.H{"status": status, "activity_enabled": rc.Activity != nil})
	})
	// 需要认证
	auth := r.Group("/api")
	auth.Use(rc.AuthMiddleware)
	{
		// 本地模式：立即备份（数据根整体打包；见 docs/design/local-backup）
		if rc.Cfg.Server.LocalSingleUser && rc.BackupService != nil {
			auth.POST("/local/backup", localBackupTrigger(rc.Cfg, rc.BackupService))
		}
		auth.POST("/auth/refresh", refreshToken(rc.UserRepo))
		auth.POST("/auth/logout", logoutUser(rc.UserRepo))
		auth.POST("/auth/change-password", changePassword(rc.UserRepo))
		auth.POST("/chat", chat(rc.Orch, rc.Cfg, rc.ConvRepo, rc.MsgRepo, rc.AgentRepo, rc.ImageHandler.Store()))
		auth.POST("/chat/tool-result", chatToolResult(rc.ClientBridge))
		auth.POST("/chat/stop", stopChat(rc.ConvRepo))
		auth.POST("/events/batch", eventsBatch(rc.Activity))
		auth.GET("/llm-config", llmConfig(rc.Cfg, rc.AgentRepo))
		auth.POST("/conversations", createConversation(rc.ConvRepo, rc.AgentRepo))
		auth.GET("/conversations", listConversations(rc.ConvRepo, rc.AgentRepo))
		auth.GET("/conversations/default", getDefaultConversation(rc.ConvRepo, rc.AgentRepo))
		auth.GET("/conversations/:id/messages", getMessages(rc.ConvRepo, rc.MsgRepo, rc.ArtifactRepo))
		auth.GET("/conversations/:id/messages-v2", getMessagesCursor(rc.MsgRepo, rc.ConvRepo, rc.ArtifactRepo))
		auth.DELETE("/conversations/:id/messages/:msgId", deleteMessage(rc.MsgRepo, rc.ConvRepo))
		auth.PATCH("/conversations/:id", renameConversation(rc.ConvRepo, rc.AgentRepo))
		auth.PATCH("/conversations/:id/mode", updateConversationMode(rc.ConvRepo))
		auth.PATCH("/conversations/:id/ephemeral", updateConversationEphemeral(rc.ConvRepo, rc.NotifySessionEnd))
		auth.GET("/conversations/:id/plans", getConversationPlan(rc.ConvRepo, rc.PlanRepo))
		auth.GET("/conversations/:id/usage", getConversationUsage(rc.ConvRepo, rc.MsgRepo, rc.LlmCostRepo, rc.LlmPriceRepo, rc.Cfg))
		auth.DELETE("/conversations/:id", deleteConversation(rc.ConvRepo, rc.Destroyer))
		auth.POST("/conversations/:id/restore", restoreConversation(rc.ConvRepo))
		auth.POST("/conversations/:id/share-messages", rc.MessageShareHandler.CreateMessageShare)
		auth.GET("/bills", rc.BillHandler.ListBills)
		auth.GET("/bills/stats", rc.BillHandler.StatsBills)
		auth.GET("/bills/owners", rc.BillHandler.OwnersBills)
		auth.GET("/bills/categories", rc.BillHandler.CategoriesBills)
		auth.GET("/bills/export", rc.BillHandler.ExportBills)
		auth.POST("/bills/import", rc.BillHandler.ImportBills)
		auth.GET("/summaries", rc.SummaryHandler.ListSummaries)
		auth.GET("/summaries/:id/messages", rc.SummaryHandler.GetSummaryMessages)
		auth.GET("/notes", rc.NoteHandler.ListNotes)
		auth.POST("/notes/batch", rc.NoteHandler.BatchGetNotes)
		auth.DELETE("/notes/:id", rc.NoteHandler.DeleteNote)
		auth.GET("/memories", rc.MemHandler.ListMemories)
		auth.GET("/memories/topics", rc.MemHandler.ListMemoryTopics)
		auth.DELETE("/memories/:id", rc.MemHandler.DeleteMemory)
		auth.POST("/memories/batch", rc.MemHandler.BatchGetMemories)
		auth.POST("/memories/batch-delete", rc.MemHandler.BatchDeleteMemories)
		auth.PATCH("/memories/:id/rating", rc.MemHandler.SetMemoryRating)
		auth.GET("/event-threads", rc.EventHandler.ListEvents)
		auth.GET("/event-threads/:id", rc.EventHandler.GetEvent)
		auth.POST("/agents", rc.AgentHandler.CreateAgent)
		auth.GET("/agents", rc.AgentHandler.ListAgents)
		auth.GET("/agents/:id", rc.AgentHandler.GetAgent)
		auth.PATCH("/agents/:id", rc.AgentHandler.UpdateAgent)
		auth.DELETE("/agents/:id", rc.AgentHandler.DeleteAgent)
		auth.GET("/agents-for-input-bar", rc.AgentHandler.ListAgentsForInputBar)
		auth.GET("/tools", rc.ToolHandler.ListTools)
		auth.POST("/tools", rc.ToolHandler.CreateTool)
		auth.PATCH("/tools/:id", rc.ToolHandler.UpdateTool)
		auth.DELETE("/tools/:id", rc.ToolHandler.DeleteTool)
		auth.GET("/prompts", rc.PromptHandler.ListPrompts)
		auth.POST("/prompts", rc.PromptHandler.CreatePrompt)
		auth.POST("/prompts/:id/rollback", rc.PromptHandler.RollbackPrompt)
		auth.DELETE("/prompts/:id", rc.PromptHandler.DeletePrompt)
		auth.GET("/documents", rc.DocumentHandler.ListDocuments)
		auth.GET("/documents/tags", rc.DocumentHandler.ListDocumentTags)
		auth.GET("/documents/trash", rc.DocumentHandler.ListTrashDocuments)
		auth.POST("/documents/trash/restore", rc.DocumentHandler.RestoreAllTrashDocuments)
		auth.DELETE("/documents/trash", rc.DocumentHandler.PurgeAllTrashDocuments)
		auth.GET("/documents/:id", rc.DocumentHandler.GetDocument)
		auth.DELETE("/documents/:id", rc.DocumentHandler.DeleteDocument)
		auth.POST("/documents/:id/restore", rc.DocumentHandler.RestoreDocument)
		auth.POST("/documents/:id/share", rc.DocumentShareHandler.CreateShare)
		auth.GET("/documents/:id/share", rc.DocumentShareHandler.GetShare)
		// 文档划线批注：列表/新建挂在文档下，改/删/重挂挂在批注下
		auth.GET("/documents/:id/annotations", rc.AnnotationHandler.ListAnnotations)
		auth.POST("/documents/:id/annotations", rc.AnnotationHandler.CreateAnnotation)
		auth.PATCH("/annotations/:id", rc.AnnotationHandler.UpdateAnnotation)
		auth.DELETE("/annotations/:id", rc.AnnotationHandler.DeleteAnnotation)
		auth.POST("/annotations/:id/relocate", rc.AnnotationHandler.RelocateAnnotation)
		auth.GET("/annotations/:id/replies", rc.AnnotationHandler.ListReplies)
		auth.POST("/annotations/:id/reply", rc.AnnotationHandler.ReplyAnnotation)
		auth.GET("/reminders", rc.ReminderHandler.ListReminders)
		auth.POST("/reminders/:id/ack", rc.ReminderHandler.AckReminder)
		auth.POST("/reminders/:id/cancel", rc.ReminderHandler.CancelReminder)
		// 图床：上传 + 管理 + 私有读（鉴权 + owner）
		auth.POST("/images/upload", rc.ImageHandler.Upload)
		auth.GET("/images", rc.ImageHandler.List)
		auth.GET("/images/:id/file", rc.ImageHandler.ServePrivate)
		auth.DELETE("/images/:id", rc.ImageHandler.Delete)

		// HTML 产物：正文出口（JSON / 裸 HTML + ETag；消息列表只带摘要，见 docs/design/html-artifact §4.1）
		auth.GET("/artifacts", rc.ArtifactHandler.ListArtifacts)
		auth.GET("/artifacts/tags", rc.ArtifactHandler.ListArtifactTags)
		auth.GET("/artifacts/:id", rc.ArtifactHandler.GetArtifact)
		auth.GET("/artifacts/:id/html", rc.ArtifactHandler.ServeArtifactHTML)
		auth.DELETE("/artifacts/:id", rc.ArtifactHandler.DeleteArtifact)
		auth.POST("/artifacts/batch-delete", rc.ArtifactHandler.BatchDeleteArtifacts)
		auth.POST("/artifacts/:id/share", rc.ArtifactHandler.CreateArtifactShare)
		auth.DELETE("/artifacts/:id/share", rc.ArtifactHandler.DeleteArtifactShare)
	}

	// 管理员路由
	admin := r.Group("/api/admin", rc.AuthMiddleware, monitor.AdminOnly(rc.Cfg.Server.AdminUsers))
	admin.GET("/health", monitor.HealthHandlerGlobal())
	admin.POST("/users", createUserByAdmin(rc.UserRepo))
	admin.GET("/users", listUsers(rc.UserRepo))
	admin.POST("/users/:id/reset-password", resetUserPassword(rc.UserRepo))

	// 本地模式：Go 直接托管内嵌前端。云端由 nginx 托管且二进制里未必有 dist，
	// 故按 local_single_user 门控，避免意外把云端也变成 serve SPA 回落。
	if rc.Cfg.Server.LocalSingleUser {
		registerEmbeddedFrontend(r)
	}

	return r
}

// registerEmbeddedFrontend 用内嵌的 front-end/dist 托管前端：
// 静态文件直接命中；缺失路径仅当「无扩展名」时回落到 index.html（SPA 路由刷新不 404），
// 缺失的带扩展名资源与目录请求一律 404（避免把丢的 JS/CSS 渲染成 HTML、避免目录列表）。
func registerEmbeddedFrontend(r *gin.Engine) {
	dist := web.DistFS()
	fileServer := http.FileServer(http.FS(dist))
	r.NoRoute(func(c *gin.Context) {
		p := c.Request.URL.Path
		if strings.HasPrefix(p, "/api") || strings.HasPrefix(p, "/images") {
			view.JSONError(c, http.StatusNotFound, "not found")
			return
		}
		if c.Request.Method != http.MethodGet {
			c.Status(http.StatusNotFound)
			return
		}
		if name := strings.TrimPrefix(p, "/"); name != "" {
			if info, err := fs.Stat(dist, strings.TrimSuffix(name, "/")); err == nil {
				if info.IsDir() {
					c.Status(http.StatusNotFound)
					return
				}
				fileServer.ServeHTTP(c.Writer, c.Request)
				return
			}
		}
		if path.Ext(p) != "" {
			c.Status(http.StatusNotFound)
			return
		}
		index, err := fs.ReadFile(dist, "index.html")
		if err != nil {
			view.JSONError(c, http.StatusNotFound, "frontend not embedded")
			return
		}
		c.Data(http.StatusOK, "text/html; charset=utf-8", index)
	})
}
