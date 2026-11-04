package controller

import (
	"log"

	"github.com/gin-gonic/gin"

	"xhan-helper/internal/service"
	"xhan-helper/internal/view"
)

// ThemeHandler — 旧主题状态页只读 API。
// 状态页已停写（事件系统接管），阶段 7 换成 EventView 后连这个只读入口一起删。
type ThemeHandler struct {
	themeRepo *service.ThemeStateRepo
}

func NewThemeHandler(repo *service.ThemeStateRepo) *ThemeHandler {
	return &ThemeHandler{themeRepo: repo}
}

// ListThemes GET /api/themes
func (h *ThemeHandler) ListThemes(c *gin.Context) {
	userID := c.GetString("user_id")
	list, total, err := h.themeRepo.ListNotDraft(c.Request.Context(), userID, 50, 0)
	if err != nil {
		log.Printf("[theme] List error: %v", err)
		view.JSONError(c, 500, "查询主题失败")
		return
	}
	view.JSONOK(c, gin.H{"items": list, "total": total})
}
