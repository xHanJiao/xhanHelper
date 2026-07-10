package controller

import (
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"

	"xhan-helper/internal/agent"
	"xhan-helper/internal/config"
	"xhan-helper/internal/middleware"
	"xhan-helper/internal/monitor"
	"xhan-helper/internal/service"
)

// RouterConfig — 路由注册所需的所有依赖
type RouterConfig struct {
	Cfg                  *config.Config
	Orch                 *agent.Orchestrator
	UserRepo             *service.UserRepo
	ConvRepo             *service.ConversationRepo
	MsgRepo              *service.MessageRepo
	AgentRepo            *service.AgentRepo
	BillHandler          *BillHandler
	NoteHandler          *NoteHandler
	MemHandler           *MemoryHandler
	AgentHandler         *AgentHandler
	ToolHandler          *ToolHandler
	PromptHandler        *PromptHandler
	DocumentHandler      *DocumentHandler
	DocumentShareHandler *DocumentShareHandler
	MessageShareHandler  *MessageShareHandler
	SummaryHandler       *SummaryHandler
	MemExtr              *agent.MemoryExtractor
	ExtraMiddleware      []gin.HandlerFunc
}

// NewRouter 注册所有路由
func NewRouter(rc *RouterConfig) *gin.Engine {

	r := gin.Default()

	for _, m := range rc.ExtraMiddleware {
		r.Use(m)
	}

	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{"GET", "POST", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Authorization", "Content-Type"},
		AllowCredentials: false,
	}))

	// 无需认证 — 账号注册/登录
	r.POST("/api/auth/register", registerUser(rc.UserRepo, rc.Cfg.Server.RegistrationEnabled, rc.Cfg.Server.AllowedUsers, rc.Cfg.Server.AdminUsers))
	r.POST("/api/auth/login", loginUser(rc.UserRepo, rc.Cfg.Server.AdminUsers))
	// 公开分享
	r.GET("/api/share/messages/:token", rc.MessageShareHandler.ViewSharedMessages)
	r.GET("/api/share/:token", rc.DocumentShareHandler.ViewSharedDocument)
	// 需要认证
	auth := r.Group("/api")
	auth.Use(middleware.Auth(rc.UserRepo))
	{
		auth.POST("/auth/refresh", refreshToken(rc.UserRepo))
		auth.POST("/auth/logout", logoutUser(rc.UserRepo))
		auth.POST("/auth/change-password", changePassword(rc.UserRepo))
		auth.POST("/chat", chat(rc.Orch, rc.Cfg, rc.ConvRepo, rc.MsgRepo, rc.AgentRepo))
		auth.GET("/llm-config", llmConfig(rc.Cfg, rc.AgentRepo))
		auth.POST("/conversations", createConversation(rc.ConvRepo, rc.AgentRepo))
		auth.GET("/conversations", listConversations(rc.ConvRepo, rc.AgentRepo))
		auth.GET("/conversations/default", getDefaultConversation(rc.ConvRepo, rc.AgentRepo))
		auth.GET("/conversations/:id/messages", getMessages(rc.ConvRepo, rc.MsgRepo))
		auth.GET("/conversations/:id/messages-v2", getMessagesCursor(rc.MsgRepo, rc.ConvRepo))
		auth.DELETE("/conversations/:id/messages/:msgId", deleteMessage(rc.MsgRepo, rc.ConvRepo))
		auth.PATCH("/conversations/:id", renameConversation(rc.ConvRepo, rc.AgentRepo))
		auth.DELETE("/conversations/:id", deleteConversation(rc.ConvRepo))
		auth.POST("/conversations/:id/extract-memories", extractMemories(rc.ConvRepo, rc.MemExtr))
		auth.POST("/conversations/:id/extract-memories/stream", extractMemoriesStream(rc.ConvRepo, rc.MemExtr))
		auth.POST("/conversations/:id/share-messages", rc.MessageShareHandler.CreateMessageShare)
		auth.GET("/bills", rc.BillHandler.ListBills)
		auth.GET("/bills/export", rc.BillHandler.ExportBills)
		auth.POST("/bills/import", rc.BillHandler.ImportBills)
		auth.GET("/summaries", rc.SummaryHandler.ListSummaries)
		auth.GET("/notes", rc.NoteHandler.ListNotes)
		auth.POST("/notes/batch", rc.NoteHandler.BatchGetNotes)
		auth.DELETE("/notes/:id", rc.NoteHandler.DeleteNote)
		auth.GET("/memories", rc.MemHandler.ListMemories)
		auth.DELETE("/memories/:id", rc.MemHandler.DeleteMemory)
		auth.POST("/memories/batch", rc.MemHandler.BatchGetMemories)
		auth.POST("/memories/batch-delete", rc.MemHandler.BatchDeleteMemories)
		auth.POST("/agents", rc.AgentHandler.CreateAgent)
		auth.GET("/agents", rc.AgentHandler.ListAgents)
		auth.GET("/agents/:id", rc.AgentHandler.GetAgent)
		auth.PATCH("/agents/:id", rc.AgentHandler.UpdateAgent)
		auth.DELETE("/agents/:id", rc.AgentHandler.DeleteAgent)
		auth.GET("/tools", rc.ToolHandler.ListTools)
		auth.POST("/tools", rc.ToolHandler.CreateTool)
		auth.PATCH("/tools/:id", rc.ToolHandler.UpdateTool)
		auth.DELETE("/tools/:id", rc.ToolHandler.DeleteTool)
		auth.GET("/prompts", rc.PromptHandler.ListPrompts)
		auth.POST("/prompts", rc.PromptHandler.CreatePrompt)
		auth.POST("/prompts/:id/rollback", rc.PromptHandler.RollbackPrompt)
		auth.DELETE("/prompts/:id", rc.PromptHandler.DeletePrompt)
		auth.GET("/documents", rc.DocumentHandler.ListDocuments)
		auth.GET("/documents/:id", rc.DocumentHandler.GetDocument)
		auth.DELETE("/documents/:id", rc.DocumentHandler.DeleteDocument)
		auth.POST("/documents/:id/share", rc.DocumentShareHandler.CreateShare)
		auth.GET("/documents/:id/share", rc.DocumentShareHandler.GetShare)
	}

	// 管理员路由
	admin := r.Group("/api/admin", middleware.Auth(rc.UserRepo), monitor.AdminOnly(rc.Cfg.Server.AdminUsers))
	admin.GET("/health", monitor.HealthHandlerGlobal())
	admin.POST("/users", createUserByAdmin(rc.UserRepo))
	admin.GET("/users", listUsers(rc.UserRepo))

	return r
}
