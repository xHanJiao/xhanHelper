package controller

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"xhan-helper/internal/config"
	"xhan-helper/internal/service"
	"xhan-helper/internal/view"
)

// DocumentShareHandler 文档分享 handler
type DocumentShareHandler struct {
	repo *service.DocumentShareRepo
	cfg  *config.Config
}

// NewDocumentShareHandler 创建文档分享 handler
func NewDocumentShareHandler(repo *service.DocumentShareRepo, cfg *config.Config) *DocumentShareHandler {
	return &DocumentShareHandler{repo: repo, cfg: cfg}
}

// CreateShare 创建或重置分享链接
func (h *DocumentShareHandler) CreateShare(c *gin.Context) {
	docID := c.Param("id")
	userID := c.GetString("user_id")
	validDays := h.cfg.Share.DefaultValidDays

	share, err := h.repo.Create(c.Request.Context(), docID, userID, validDays)
	if err != nil {
		log.Printf("[share] create share failed: docID=%s err=%v", docID, err)
		if err.Error() == "文档不存在" {
			view.JSONError(c, http.StatusNotFound, "文档不存在")
			return
		}
		view.JSONError(c, http.StatusInternalServerError, "创建分享失败")
		return
	}

	view.JSONOK(c, gin.H{
		"id":         share.ID,
		"token":      share.Token,
		"url":        "/s/" + share.Token,
		"expires_at": share.ExpiresAt,
	})
}

// GetShare 获取文档当前的分享状态
func (h *DocumentShareHandler) GetShare(c *gin.Context) {
	docID := c.Param("id")
	userID := c.GetString("user_id")

	share, err := h.repo.GetByDocumentID(c.Request.Context(), docID, userID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			view.JSONError(c, http.StatusNotFound, "该文档暂未分享")
			return
		}
		log.Printf("[share] get share failed: docID=%s err=%v", docID, err)
		view.JSONError(c, http.StatusNotFound, "文档不存在")
		return
	}

	view.JSONOK(c, gin.H{
		"id":         share.ID,
		"token":      share.Token,
		"url":        "/s/" + share.Token,
		"expires_at": share.ExpiresAt,
		"is_valid":   time.Now().Before(share.ExpiresAt),
	})
}

// ViewSharedDocument 公开查看分享的文档
func (h *DocumentShareHandler) ViewSharedDocument(c *gin.Context) {
	token := c.Param("token")

	share, doc, err := h.repo.GetByToken(c.Request.Context(), token)
	if err != nil {
		log.Printf("[share] view shared failed: token=%s err=%v", token, err)
		if err.Error() == "分享已过期" {
			view.JSONError(c, http.StatusGone, "分享链接已过期")
			return
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			view.JSONError(c, http.StatusNotFound, "分享链接无效")
			return
		}
		view.JSONError(c, http.StatusNotFound, "文档不存在")
		return
	}

	view.JSONOK(c, gin.H{
		"title":      doc.Title,
		"content":    doc.Content,
		"tags":       doc.Tags,
		"created_at": doc.CreatedAt,
		"updated_at": doc.UpdatedAt,
		"expires_at": share.ExpiresAt,
	})

	go h.repo.IncrementVisitCount(context.Background(), token)
}
