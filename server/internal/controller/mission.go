package controller

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"xhan-helper/internal/agent"
	"xhan-helper/internal/service"
	"xhan-helper/internal/view"
)

// MissionHandler — 任务管理 API
type MissionHandler struct {
	missionRepo   *service.MissionRepo
	missionRunner *agent.MissionRunner
}

func NewMissionHandler(repo *service.MissionRepo, runner *agent.MissionRunner) *MissionHandler {
	return &MissionHandler{missionRepo: repo, missionRunner: runner}
}

// ListMissions GET /api/missions
func (h *MissionHandler) ListMissions(c *gin.Context) {
	userID := c.GetString("user_id")
	status := c.Query("status")

	var statuses []string
	if status != "" {
		statuses = []string{status}
	} else {
		statuses = []string{"active", "paused"}
	}

	list, err := h.missionRepo.ListByStatuses(c.Request.Context(), userID, statuses)
	if err != nil {
		log.Printf("[mission] List error: %v", err)
		view.JSONError(c, 500, "查询任务列表失败")
		return
	}
	view.JSONOK(c, gin.H{"items": list, "total": len(list)})
}

// GetMission GET /api/missions/:id
func (h *MissionHandler) GetMission(c *gin.Context) {
	userID := c.GetString("user_id")
	id := c.Param("id")

	m, err := h.missionRepo.GetByID(c.Request.Context(), id, userID)
	if err != nil {
		log.Printf("[mission] Get error: %v", err)
		view.JSONError(c, 500, "查询任务失败")
		return
	}
	if m == nil {
		view.JSONError(c, 404, "任务不存在")
		return
	}
	view.JSONOK(c, m)
}

// UpdateMission PATCH /api/missions/:id
func (h *MissionHandler) UpdateMission(c *gin.Context) {
	userID := c.GetString("user_id")
	id := c.Param("id")

	var req struct {
		Status    string `json:"status"` // active | paused | completed | rejected
		Goal      string `json:"goal"`
		CheckCron string `json:"check_cron"` // daily | weekly | 2daily
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		view.JSONError(c, 400, "参数格式错误")
		return
	}

	m, err := h.missionRepo.GetByID(c.Request.Context(), id, userID)
	if err != nil || m == nil {
		view.JSONError(c, 404, "任务不存在")
		return
	}

	if req.Goal != "" {
		m.Goal = req.Goal
	}
	if req.CheckCron != "" {
		m.CheckCron = req.CheckCron
	}
	if req.Goal != "" || req.CheckCron != "" {
		if err := h.missionRepo.Update(c.Request.Context(), m); err != nil {
			log.Printf("[mission] Update error: %v", err)
			view.JSONError(c, 500, "更新任务失败")
			return
		}
	}

	if req.Status != "" {
		if err := h.missionRepo.UpdateStatus(c.Request.Context(), id, userID, req.Status); err != nil {
			log.Printf("[mission] UpdateStatus error: %v", err)
			view.JSONError(c, 500, "更新任务状态失败")
			return
		}
	}

	view.JSONOK(c, gin.H{"status": "ok"})
}

// TriggerCheck POST /api/missions/:id/check
func (h *MissionHandler) TriggerCheck(c *gin.Context) {
	userID := c.GetString("user_id")
	id := c.Param("id")

	m, err := h.missionRepo.GetByID(c.Request.Context(), id, userID)
	if err != nil || m == nil {
		view.JSONError(c, 404, "任务不存在")
		return
	}
	if m.Status != "active" {
		view.JSONError(c, 400, "只能检查 active 状态的任务")
		return
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := h.missionRunner.Run(ctx, m); err != nil {
			log.Printf("[mission] manual check error: id=%s err=%v", id[:8], err)
		}
	}()

	c.JSON(http.StatusOK, gin.H{"status": "ok", "message": "任务检查已触发，稍后查看信箱"})
}
