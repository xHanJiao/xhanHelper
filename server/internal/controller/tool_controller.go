package controller

import (
	"encoding/json"
	"log"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
	"xhan-helper/internal/view"
)

// ToolHandler — 动态工具管理 API
type ToolHandler struct {
	toolRepo *service.ToolRepo
}

// NewToolHandler 创建 ToolHandler
func NewToolHandler(toolRepo *service.ToolRepo) *ToolHandler {
	return &ToolHandler{toolRepo: toolRepo}
}

// ListTools GET /api/tools?status=
func (h *ToolHandler) ListTools(c *gin.Context) {
	userID := c.GetString("user_id")
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	tools, total, err := h.toolRepo.ListTools(c.Request.Context(), userID, c.Query("status"), limit, offset)
	if err != nil {
		view.JSONError(c, 500, "query tools failed")
		return
	}
	view.JSONOK(c, gin.H{"tools": tools, "total": total})
}

// CreateTool POST /api/tools
func (h *ToolHandler) CreateTool(c *gin.Context) {
	userID := c.GetString("user_id")
	var req model.ToolRegistry
	if err := c.ShouldBindJSON(&req); err != nil {
		view.JSONError(c, 400, "invalid request")
		return
	}
	if req.Name == "" || req.SchemaJSON == "" {
		view.JSONError(c, 400, "name and schema_json required")
		return
	}
	if !json.Valid([]byte(req.SchemaJSON)) {
		view.JSONError(c, 400, "schema_json is not valid JSON")
		return
	}
	req.ID = uuid.New().String()
	req.UserID = userID
	req.Status = "draft"
	if err := h.toolRepo.CreateTool(c.Request.Context(), &req); err != nil {
		view.JSONError(c, 500, "create tool failed")
		return
	}
	view.JSONOK(c, req)
}

// UpdateTool PATCH /api/tools/:id
func (h *ToolHandler) UpdateTool(c *gin.Context) {
	userID := c.GetString("user_id")
	id := c.Param("id")

	existing, err := h.toolRepo.FindToolByID(c.Request.Context(), id, userID)
	if err != nil || existing == nil {
		view.JSONError(c, 404, "tool not found")
		return
	}

	var req model.ToolRegistry
	if err := c.ShouldBindJSON(&req); err != nil {
		view.JSONError(c, 400, "invalid request")
		return
	}

	req.ID = id
	req.UserID = userID
	if err := h.toolRepo.UpdateTool(c.Request.Context(), &req); err != nil {
		view.JSONError(c, 500, "update tool failed")
		return
	}
	updated, err := h.toolRepo.FindToolByID(c.Request.Context(), id, userID)
	if err != nil {
		log.Printf("[tool] FindToolByID after update error: %v", err)
	}
	view.JSONOK(c, updated)
}

// DeleteTool DELETE /api/tools/:id
func (h *ToolHandler) DeleteTool(c *gin.Context) {
	userID := c.GetString("user_id")
	if err := h.toolRepo.DeleteTool(c.Request.Context(), c.Param("id"), userID); err != nil {
		view.JSONError(c, 500, "delete tool failed")
		return
	}
	view.JSONOK(c, gin.H{"deleted": true})
}
