package controller

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
	"xhan-helper/internal/view"
)

// PromptHandler — 用户 System Prompt REST API
type PromptHandler struct {
	promptRepo *service.UserPromptRepo
}

// NewPromptHandler 创建 PromptHandler
func NewPromptHandler(promptRepo *service.UserPromptRepo) *PromptHandler {
	return &PromptHandler{promptRepo: promptRepo}
}

// ListPrompts GET /api/prompts?type=&status=
func (h *PromptHandler) ListPrompts(c *gin.Context) {
	userID := c.GetString("user_id")
	list, err := h.promptRepo.ListByUser(c.Request.Context(), userID, c.Query("type"), c.Query("status"))
	if err != nil {
		view.JSONError(c, 500, "query prompts failed")
		return
	}
	view.JSONOK(c, list)
}

// CreatePrompt POST /api/prompts
func (h *PromptHandler) CreatePrompt(c *gin.Context) {
	userID := c.GetString("user_id")

	var req struct {
		PromptType   string `json:"prompt_type"`
		SystemPrompt string `json:"system_prompt"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		view.JSONError(c, 400, "invalid request")
		return
	}
	if req.PromptType != "flash" && req.PromptType != "pro" {
		view.JSONError(c, 400, "prompt_type must be flash or pro")
		return
	}
	if req.SystemPrompt == "" {
		view.JSONError(c, 400, "system_prompt required")
		return
	}

	p := &model.UserPrompt{
		ID:           uuid.New().String(),
		UserID:       userID,
		PromptType:   req.PromptType,
		SystemPrompt: req.SystemPrompt,
		Status:       "active",
	}
	if err := h.promptRepo.Create(c.Request.Context(), p); err != nil {
		view.JSONError(c, 500, "create prompt failed")
		return
	}
	view.JSONOK(c, p)
}

// RollbackPrompt POST /api/prompts/:id/rollback
func (h *PromptHandler) RollbackPrompt(c *gin.Context) {
	userID := c.GetString("user_id")
	id := c.Param("id")

	existing, err := h.promptRepo.ListByUser(c.Request.Context(), userID, "", "")
	if err != nil {
		view.JSONError(c, 500, "query failed")
		return
	}
	var target *model.UserPrompt
	for i := range existing {
		if existing[i].ID == id {
			target = &existing[i]
			break
		}
	}
	if target == nil {
		view.JSONError(c, 404, "prompt not found")
		return
	}

	if err := h.promptRepo.Rollback(c.Request.Context(), id, userID, target.PromptType); err != nil {
		view.JSONError(c, 500, "rollback failed")
		return
	}
	view.JSONOK(c, gin.H{"ok": true})
}

// DeletePrompt DELETE /api/prompts/:id
func (h *PromptHandler) DeletePrompt(c *gin.Context) {
	userID := c.GetString("user_id")
	if err := h.promptRepo.Delete(c.Request.Context(), c.Param("id"), userID); err != nil {
		view.JSONError(c, 500, "delete failed")
		return
	}
	view.JSONOK(c, gin.H{"deleted": true})
}
