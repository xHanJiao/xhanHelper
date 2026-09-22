package controller

import (
	"github.com/gin-gonic/gin"

	"xhan-helper/internal/agent"
	"xhan-helper/internal/service"
	"xhan-helper/internal/view"
)

// ReflectionRoutesConfig — 反思系统路由注册所需的所有依赖
type ReflectionRoutesConfig struct {
	R                      gin.IRouter
	UserRepo               *service.UserRepo
	ThemeHandler           *ThemeHandler
	ReflectionAdminHandler *ReflectionAdminHandler
	MessageTraceHandler    *MessageTraceHandler
	MessageDebugHandler    *MessageDebugHandler
	ReplayHandler          *ReplayHandler
	EnableReplay           bool // admin 路由已有 AdminMiddleware 保护，生产环境也开放
	CronRunner             *agent.CronRunner
	AuthMiddleware         gin.HandlerFunc
	AdminMiddleware        gin.HandlerFunc
}

// RegisterReflectionRoutes 注册 Phase 6 反思系统相关路由（在 NewRouter 之后调用）
func RegisterReflectionRoutes(rc *ReflectionRoutesConfig) {
	// 认证路由
	auth := rc.R.Group("/api")
	auth.Use(rc.AuthMiddleware)
	{
		// 主题管理
		auth.GET("/themes", rc.ThemeHandler.ListThemes)
		auth.POST("/themes", rc.ThemeHandler.CreateTheme)
		auth.PATCH("/themes/:id", rc.ThemeHandler.UpdateTheme)
	}

	// 管理员路由
	admin := rc.R.Group("/api/admin")
	admin.Use(rc.AuthMiddleware, rc.AdminMiddleware)
	{
		admin.GET("/reflection/traces", rc.ReflectionAdminHandler.ListTraces)
		admin.GET("/reflection/traces/:id", rc.ReflectionAdminHandler.GetTrace)
		admin.GET("/reflection/decisions", rc.ReflectionAdminHandler.ListDecisions)
		admin.GET("/reflection/llm-calls/:id", rc.ReflectionAdminHandler.GetLLMCall)
		admin.GET("/message-traces", rc.MessageTraceHandler.ListMessageTraces)
		admin.GET("/messages/:id/debug", rc.MessageDebugHandler.GetMessageDebug)
		admin.GET("/messages/:id/debug-detail", rc.MessageDebugHandler.GetMessageDebugDetail)
		admin.GET("/resource-lookup", rc.MessageDebugHandler.LookupResource)
		admin.POST("/resource-lookup/batch", rc.MessageDebugHandler.LookupResourcesBatch)
		if rc.EnableReplay {
			admin.POST("/replay/preview", rc.ReplayHandler.PreviewReplay)
			admin.POST("/replay/compare", rc.ReplayHandler.CompareReplay)
		}
		admin.GET("/hooks", func(c *gin.Context) {
			view.JSONOK(c, rc.CronRunner.GetHookStatus())
		})
	}
}
