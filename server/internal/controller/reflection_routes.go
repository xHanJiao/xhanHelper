package controller

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"xhan-helper/internal/agent"
	"xhan-helper/internal/service"
)

// RegisterReflectionRoutes 注册 Phase 6 反思系统相关路由（在 NewRouter 之后调用）
func RegisterReflectionRoutes(
	r gin.IRouter,
	userRepo *service.UserRepo,
	mailboxHandler *MailboxHandler,
	themeHandler *ThemeHandler,
	reflectionAdminHandler *ReflectionAdminHandler,
	reflectionOrch *agent.ReflectionOrchestrator,
	authMiddleware gin.HandlerFunc,
	adminMiddleware gin.HandlerFunc,
) {
	// 认证路由
	auth := r.Group("/api")
	auth.Use(authMiddleware)
	{
		// 信箱
		auth.GET("/mailbox", mailboxHandler.ListItems)
		auth.GET("/mailbox/unread-count", mailboxHandler.UnreadCount)
		auth.PATCH("/mailbox/:id/read", mailboxHandler.MarkRead)
		auth.PATCH("/mailbox/:id/archive", mailboxHandler.Archive)
		auth.POST("/mailbox/batch-read", mailboxHandler.BatchRead)

		// 主题管理
		auth.GET("/themes", themeHandler.ListThemes)
		auth.POST("/themes", themeHandler.CreateTheme)
		auth.PATCH("/themes/:id", themeHandler.UpdateTheme)

		// 手动触发反思
		auth.POST("/reflection/trigger", func(c *gin.Context) {
			userID := c.GetString("user_id")
			go func() {
				if err := reflectionOrch.RunOnce(context.Background(), userID, "manual"); err != nil {
					// 错误已记录在 trace 中
					_ = err
				}
			}()
			c.JSON(http.StatusOK, gin.H{"status": "ok", "message": "反思已触发，稍后查看信箱"})
		})
	}

	// 管理员路由
	admin := r.Group("/api/admin")
	admin.Use(authMiddleware, adminMiddleware)
	{
		admin.GET("/reflection/traces", reflectionAdminHandler.ListTraces)
		admin.GET("/reflection/traces/:id", reflectionAdminHandler.GetTrace)
		admin.GET("/reflection/decisions", reflectionAdminHandler.ListDecisions)
		admin.GET("/reflection/llm-calls/:id", reflectionAdminHandler.GetLLMCall)
	}
}
