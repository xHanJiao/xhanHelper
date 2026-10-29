package controller

import (
	"errors"
	"log"
	"net/http"
	"strconv"

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

// ListArtifacts GET /api/artifacts?limit=30&offset=0 — 灵阅列表（按时间倒序，不含正文）。
// 直接返回 model.Artifact：HTML 字段是 json:"-"、DeletedAt 也不序列化，
// 所以这里不需要再定义一个 DTO 去防漏（见 docs/design/html-artifact §9.5）。
// limit 默认 30 与客户端 ArtifactViewModel.pageSize 一致；两边改一处时记得对齐。
func (h *ArtifactHandler) ListArtifacts(c *gin.Context) {
	userID := c.GetString("user_id")
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "30"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	// limit 超界/非数字回默认值，不让一次误传把整表拉出来
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	if offset < 0 {
		offset = 0
	}

	items, total, err := h.repo.ListByUser(c.Request.Context(), userID, limit, offset)
	if err != nil {
		log.Printf("[artifact] ListArtifacts user=%s: %v", userID, err)
		view.JSONError(c, http.StatusInternalServerError, "query artifacts failed")
		return
	}
	view.JSONOK(c, gin.H{"items": items, "total": total})
}
