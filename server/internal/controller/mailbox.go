package controller

import (
	"log"
	"strconv"

	"github.com/gin-gonic/gin"

	"xhan-helper/internal/service"
	"xhan-helper/internal/view"
)

// MailboxHandler — 信箱 API
type MailboxHandler struct {
	mailboxRepo *service.MailboxRepo
}

func NewMailboxHandler(repo *service.MailboxRepo) *MailboxHandler {
	return &MailboxHandler{mailboxRepo: repo}
}

// ListItems GET /api/mailbox
func (h *MailboxHandler) ListItems(c *gin.Context) {
	userID := c.GetString("user_id")
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	itemType := c.Query("type")
	isReadStr := c.Query("is_read")

	var isRead *bool
	if isReadStr == "true" {
		t := true
		isRead = &t
	} else if isReadStr == "false" {
		f := false
		isRead = &f
	}

	list, total, err := h.mailboxRepo.ListByUser(c.Request.Context(), userID, itemType, isRead, limit, offset)
	if err != nil {
		log.Printf("[mailbox] ListByUser error: %v", err)
		view.JSONError(c, 500, "查询信箱失败")
		return
	}

	view.JSONOK(c, gin.H{
		"items": list,
		"total": total,
	})
}

// UnreadCount GET /api/mailbox/unread-count
func (h *MailboxHandler) UnreadCount(c *gin.Context) {
	userID := c.GetString("user_id")
	count, err := h.mailboxRepo.UnreadCount(c.Request.Context(), userID)
	if err != nil {
		log.Printf("[mailbox] UnreadCount error: %v", err)
		view.JSONError(c, 500, "查询未读数失败")
		return
	}
	view.JSONOK(c, gin.H{"count": count})
}

// MarkRead PATCH /api/mailbox/:id/read
func (h *MailboxHandler) MarkRead(c *gin.Context) {
	userID := c.GetString("user_id")
	id := c.Param("id")
	if err := h.mailboxRepo.MarkRead(c.Request.Context(), id, userID); err != nil {
		log.Printf("[mailbox] MarkRead error: %v", err)
		view.JSONError(c, 500, "标记已读失败")
		return
	}
	view.JSONOK(c, gin.H{"status": "ok"})
}

// Archive PATCH /api/mailbox/:id/archive
func (h *MailboxHandler) Archive(c *gin.Context) {
	userID := c.GetString("user_id")
	id := c.Param("id")
	if err := h.mailboxRepo.Archive(c.Request.Context(), id, userID); err != nil {
		log.Printf("[mailbox] Archive error: %v", err)
		view.JSONError(c, 500, "归档失败")
		return
	}
	view.JSONOK(c, gin.H{"status": "ok"})
}

// BatchRead POST /api/mailbox/batch-read
func (h *MailboxHandler) BatchRead(c *gin.Context) {
	userID := c.GetString("user_id")
	var req struct {
		IDs []string `json:"ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		view.JSONError(c, 400, "参数格式错误")
		return
	}
	if err := h.mailboxRepo.BatchMarkRead(c.Request.Context(), req.IDs, userID); err != nil {
		log.Printf("[mailbox] BatchMarkRead error: %v", err)
		view.JSONError(c, 500, "批量已读失败")
		return
	}
	view.JSONOK(c, gin.H{"status": "ok"})
}
