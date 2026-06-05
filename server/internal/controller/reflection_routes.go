package controller

import (
	"context"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"xhan-helper/internal/agent"
	"xhan-helper/internal/service"
	"xhan-helper/internal/view"
)

// ReflectionRoutesConfig — 反思系统路由注册所需的所有依赖
type ReflectionRoutesConfig struct {
	R                      gin.IRouter
	UserRepo               *service.UserRepo
	MailboxHandler         *MailboxHandler
	ThemeHandler           *ThemeHandler
	MissionHandler         *MissionHandler
	ReflectionAdminHandler *ReflectionAdminHandler
	MessageTraceHandler    *MessageTraceHandler
	MessageDebugHandler    *MessageDebugHandler
	ReplayHandler          *ReplayHandler
	EnableReplay           bool // 仅 dev 环境开放
	PendingFindingRepo     *service.PendingFindingRepo
	ReflectionOrch         *agent.ReflectionOrchestrator
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
		// 信箱
		auth.GET("/mailbox", rc.MailboxHandler.ListItems)
		auth.GET("/mailbox/unread-count", rc.MailboxHandler.UnreadCount)
		auth.PATCH("/mailbox/:id/read", rc.MailboxHandler.MarkRead)
		auth.PATCH("/mailbox/:id/archive", rc.MailboxHandler.Archive)
		auth.POST("/mailbox/:id/chat", rc.MailboxHandler.StartChat)
		auth.POST("/mailbox/batch-read", rc.MailboxHandler.BatchRead)

		// 主题管理
		auth.GET("/themes", rc.ThemeHandler.ListThemes)
		auth.POST("/themes", rc.ThemeHandler.CreateTheme)
		auth.PATCH("/themes/:id", rc.ThemeHandler.UpdateTheme)

		// 任务管理（Mission）
		auth.GET("/missions", rc.MissionHandler.ListMissions)
		auth.GET("/missions/:id", rc.MissionHandler.GetMission)
		auth.PATCH("/missions/:id", rc.MissionHandler.UpdateMission)
		auth.POST("/missions/:id/check", rc.MissionHandler.TriggerCheck)

		// 手动触发反思
		auth.POST("/reflection/trigger", func(c *gin.Context) {
			userID := c.GetString("user_id")
			go func() {
				if err := rc.ReflectionOrch.RunOnce(context.Background(), userID, "manual"); err != nil {
					// 错误已记录在 trace 中
					_ = err
				}
			}()
			c.JSON(http.StatusOK, gin.H{"status": "ok", "message": "反思已触发，稍后查看信箱"})
		})
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
		admin.GET("/pending-findings", func(c *gin.Context) {
			limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
			offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
			status := c.Query("status")
			list, total, err := rc.PendingFindingRepo.ListAll(c.Request.Context(), status, limit, offset)
			if err != nil {
				view.JSONError(c, 500, "查询失败")
				return
			}
			view.JSONOK(c, gin.H{"items": list, "total": total})
		})
	}
}
