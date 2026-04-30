package controller

import (
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"xhan-helper/internal/config"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/middleware"
	"xhan-helper/internal/service"
)

// NewRouter 注册所有路由
func NewRouter(cfg *config.Config, llmClient llm.LLMClient,
	devRepo *service.DeviceRepo, convRepo *service.ConversationRepo,
	msgRepo *service.MessageRepo) *gin.Engine {

	r := gin.Default()

	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{"GET", "POST", "OPTIONS"},
		AllowHeaders:     []string{"Authorization", "Content-Type"},
		AllowCredentials: true,
	}))

	// 无需认证
	r.POST("/api/devices/register", registerDevice(devRepo))

	// 需要认证
	auth := r.Group("/api")
	auth.Use(middleware.Auth(devRepo))
	{
		auth.POST("/chat", chat(llmClient, convRepo, msgRepo))
		auth.GET("/conversations", listConversations(convRepo))
		auth.GET("/conversations/:id/messages", getMessages(convRepo, msgRepo))
	}

	return r
}
