package controller

import (
	"errors"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"xhan-helper/internal/service"
	"xhan-helper/internal/view"
)

// ArtifactHandler — HTML 产物的唯一正文出口。
type ArtifactHandler struct {
	repo *service.ArtifactRepo
}

func NewArtifactHandler(repo *service.ArtifactRepo) *ArtifactHandler {
	return &ArtifactHandler{repo: repo}
}

// GetArtifact GET /api/artifacts/:id — 返回单条（含正文）。
// 消息列表永远不带正文，只有这个接口带（见 docs/design/html-artifact §4.1）。
func (h *ArtifactHandler) GetArtifact(c *gin.Context) {
	userID := c.GetString("user_id")
	a, err := h.repo.GetByID(c.Request.Context(), userID, c.Param("id"))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			view.JSONError(c, http.StatusNotFound, "artifact not found")
			return
		}
		log.Printf("[artifact] GetArtifact id=%s user=%s: %v", c.Param("id"), userID, err)
		view.JSONError(c, http.StatusInternalServerError, "query artifact failed")
		return
	}
	view.JSONOK(c, gin.H{
		"id":         a.ID,
		"title":      a.Title,
		"summary":    a.Summary,
		"html":       a.HTML,
		"created_at": a.CreatedAt,
	})
}
