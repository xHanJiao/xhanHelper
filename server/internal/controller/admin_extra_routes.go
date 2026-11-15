package controller

import (
	"github.com/gin-gonic/gin"

	"xhan-helper/internal/agent"
	"xhan-helper/internal/service"
	"xhan-helper/internal/view"
)

// AdminExtraRoutesConfig — Admin 扩展路由（message-trace / debug / replay / hooks）注册所需依赖
type AdminExtraRoutesConfig struct {
	R                   gin.IRouter
	UserRepo            *service.UserRepo
	MessageTraceHandler *MessageTraceHandler
	MessageDebugHandler *MessageDebugHandler
	ReplayHandler       *ReplayHandler
	EnableReplay        bool // admin 路由已有 AdminMiddleware 保护，生产环境也开放
	CronRunner          *agent.CronRunner
	AuthMiddleware      gin.HandlerFunc
	AdminMiddleware     gin.HandlerFunc
}

// RegisterAdminExtraRoutes 注册 Admin 扩展路由（在 NewRouter 之后调用）
func RegisterAdminExtraRoutes(rc *AdminExtraRoutesConfig) {
	// 管理员路由
	admin := rc.R.Group("/api/admin")
	admin.Use(rc.AuthMiddleware, rc.AdminMiddleware)
	{
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
