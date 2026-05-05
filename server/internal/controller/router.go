package controller

import (
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"xhan-helper/internal/agent"
	"xhan-helper/internal/config"
	"xhan-helper/internal/middleware"
	"xhan-helper/internal/service"
)

// NewRouter 注册所有路由
func NewRouter(cfg *config.Config, orch *agent.Orchestrator,
	userRepo *service.UserRepo, convRepo *service.ConversationRepo,
	msgRepo *service.MessageRepo, agentRepo *service.AgentRepo,
	billHandler *BillHandler, noteHandler *NoteHandler,
	memHandler *MemoryHandler, agentHandler *AgentHandler, memExtr *agent.MemoryExtractor) *gin.Engine {

	r := gin.Default()

	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{"GET", "POST", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Authorization", "Content-Type"},
		AllowCredentials: false,
	}))

	// 无需认证 — 账号注册/登录
	r.POST("/api/auth/register", registerUser(userRepo, cfg.Server.RegistrationEnabled, cfg.Server.AllowedUsers))
	r.POST("/api/auth/login", loginUser(userRepo))

	// 需要认证
	auth := r.Group("/api")
	auth.Use(middleware.Auth(userRepo))
	{
		auth.POST("/auth/logout", logoutUser(userRepo))
		auth.POST("/chat", chat(orch, convRepo, msgRepo, agentRepo))
		auth.GET("/conversations", listConversations(convRepo, agentRepo))
		auth.GET("/conversations/:id/messages", getMessages(convRepo, msgRepo))
		auth.PATCH("/conversations/:id", renameConversation(convRepo))
		auth.DELETE("/conversations/:id", deleteConversation(convRepo))
		auth.POST("/conversations/:id/extract-memories", extractMemories(convRepo, memExtr))
		auth.GET("/bills", billHandler.ListBills)
		auth.GET("/bills/export", billHandler.ExportBills)
		auth.POST("/bills/import", billHandler.ImportBills)
		auth.GET("/notes", noteHandler.ListNotes)
		auth.DELETE("/notes/:id", noteHandler.DeleteNote)
		auth.GET("/memories", memHandler.ListMemories)
		auth.DELETE("/memories/:id", memHandler.DeleteMemory)
		auth.POST("/memories/batch", memHandler.BatchGetMemories)
		auth.POST("/memories/batch-delete", memHandler.BatchDeleteMemories)
		auth.POST("/agents", agentHandler.CreateAgent)
		auth.GET("/agents", agentHandler.ListAgents)
		auth.GET("/agents/:id", agentHandler.GetAgent)
		auth.PATCH("/agents/:id", agentHandler.UpdateAgent)
		auth.DELETE("/agents/:id", agentHandler.DeleteAgent)
	}

	return r
}
