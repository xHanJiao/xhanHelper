package controller

import (
	"log"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
	"xhan-helper/internal/view"
)

// ThemeHandler — 主题管理 API
type ThemeHandler struct {
	themeRepo *service.ThemeStateRepo
}

func NewThemeHandler(repo *service.ThemeStateRepo) *ThemeHandler {
	return &ThemeHandler{themeRepo: repo}
}

// ListThemes GET /api/themes
func (h *ThemeHandler) ListThemes(c *gin.Context) {
	userID := c.GetString("user_id")
	list, total, err := h.themeRepo.List(c.Request.Context(), userID, "", 50, 0)
	if err != nil {
		log.Printf("[theme] List error: %v", err)
		view.JSONError(c, 500, "查询主题失败")
		return
	}
	view.JSONOK(c, gin.H{"items": list, "total": total})
}

// CreateTheme POST /api/themes
func (h *ThemeHandler) CreateTheme(c *gin.Context) {
	userID := c.GetString("user_id")
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Name == "" {
		view.JSONError(c, 400, "请提供主题名称")
		return
	}
	theme := &model.ThemeState{
		ID:          uuid.New().String(),
		UserID:      userID,
		Name:        req.Name,
		Description: req.Description,
		Status:      "active",
	}
	if err := h.themeRepo.Create(c.Request.Context(), theme); err != nil {
		log.Printf("[theme] Create error: %v", err)
		view.JSONError(c, 500, "创建主题失败")
		return
	}
	view.JSONOK(c, theme)
}

// UpdateTheme PATCH /api/themes/:id
func (h *ThemeHandler) UpdateTheme(c *gin.Context) {
	userID := c.GetString("user_id")
	id := c.Param("id")
	var req struct {
		Description string `json:"description"`
		Status      string `json:"status"` // active | archived | closed
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		view.JSONError(c, 400, "参数格式错误")
		return
	}
	switch req.Status {
	case "active":
		if err := h.themeRepo.Reactivate(c.Request.Context(), id, userID); err != nil {
			log.Printf("[theme] Reactivate error: %v", err)
			view.JSONError(c, 500, "重新激活失败")
			return
		}
	case "candidate":
		// candidate → active 的转正路径
		if err := h.themeRepo.UpdateStatus(c.Request.Context(), id, userID, "active"); err != nil {
			log.Printf("[theme] UpdateStatus error: %v", err)
			view.JSONError(c, 500, "采纳失败")
			return
		}
	case "closed":
		if err := h.themeRepo.Close(c.Request.Context(), id, userID); err != nil {
			log.Printf("[theme] Close error: %v", err)
			view.JSONError(c, 500, "关闭主题失败")
			return
		}
	case "archived":
		if err := h.themeRepo.Archive(c.Request.Context(), id, userID); err != nil {
			log.Printf("[theme] Archive error: %v", err)
			view.JSONError(c, 500, "归档失败")
			return
		}
	}
	view.JSONOK(c, gin.H{"status": "ok"})
}
