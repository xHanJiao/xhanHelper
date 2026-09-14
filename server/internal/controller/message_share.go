package controller

import (
	"context"
	"errors"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"xhan-helper/internal/config"
	"xhan-helper/internal/service"
	"xhan-helper/internal/view"
)

// MessageShareHandler 消息分享 handler
type MessageShareHandler struct {
	repo     *service.MessageShareRepo
	cfg      *config.Config
	convRepo *service.ConversationRepo
}

// NewMessageShareHandler 创建消息分享 handler
func NewMessageShareHandler(repo *service.MessageShareRepo, cfg *config.Config, convRepo *service.ConversationRepo) *MessageShareHandler {
	if convRepo == nil {
		panic("message_share: convRepo is nil (wiring bug)")
	}
	return &MessageShareHandler{repo: repo, cfg: cfg, convRepo: convRepo}
}

// createShareReq 创建分享请求体
type createShareReq struct {
	UserMessageIDs []string `json:"user_message_ids"`
}

// CreateMessageShare 创建消息分享
func (h *MessageShareHandler) CreateMessageShare(c *gin.Context) {
	convID := c.Param("id")
	userID := c.GetString("user_id")

	// 无痕会话不支持分享（公开链接与"阅后即焚"矛盾）
	if ephemeral, err := h.convRepo.IsEphemeral(c.Request.Context(), convID); err != nil {
		log.Printf("[share] IsEphemeral error: convID=%s err=%v", convID, err)
		view.JSONError(c, http.StatusInternalServerError, "创建分享失败")
		return
	} else if ephemeral {
		view.JSONError(c, http.StatusForbidden, "无痕会话不支持分享")
		return
	}

	var req createShareReq
	if err := c.ShouldBindJSON(&req); err != nil {
		view.JSONError(c, http.StatusBadRequest, "请求格式错误")
		return
	}

	if len(req.UserMessageIDs) < 1 {
		view.JSONError(c, http.StatusBadRequest, "至少选择 1 轮消息")
		return
	}
	if len(req.UserMessageIDs) > 10 {
		view.JSONError(c, http.StatusBadRequest, "最多选择 10 轮消息")
		return
	}

	share, err := h.repo.Create(c.Request.Context(), convID, userID, req.UserMessageIDs)
	if err != nil {
		log.Printf("[share] create message share failed: convID=%s err=%v", convID, err)
		msg := err.Error()
		if msg == "部分消息不属于当前用户或对话" {
			view.JSONError(c, http.StatusForbidden, msg)
			return
		}
		if msg == "没有可分享的消息" {
			view.JSONError(c, http.StatusBadRequest, msg)
			return
		}
		view.JSONError(c, http.StatusInternalServerError, "创建分享失败")
		return
	}

	view.JSONOK(c, gin.H{
		"token":      share.Token,
		"expires_at": share.ExpiresAt,
	})
}

// ViewSharedMessages 公开查看分享的消息
func (h *MessageShareHandler) ViewSharedMessages(c *gin.Context) {
	token := c.Param("token")

	messages, err := h.repo.GetMessagesByToken(c.Request.Context(), token)
	if err != nil {
		log.Printf("[share] view shared messages failed: token=%s err=%v", token, err)
		if err.Error() == "分享已过期" {
			view.JSONError(c, http.StatusGone, "分享链接已过期（有效期7天）")
			return
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			view.JSONError(c, http.StatusNotFound, "该分享不存在或已被删除")
			return
		}
		view.JSONError(c, http.StatusNotFound, "该分享不存在或已被删除")
		return
	}

	// 获取分享信息用于返回 expires_at
	share, _ := h.repo.GetByToken(c.Request.Context(), token)
	expiresAt := ""
	if share != nil {
		expiresAt = share.ExpiresAt.Format("2006-01-02T15:04:05Z07:00")
	}

	view.JSONOK(c, gin.H{
		"messages":   messages,
		"expires_at": expiresAt,
	})

	go h.repo.IncrementVisitCount(context.Background(), token)
}
