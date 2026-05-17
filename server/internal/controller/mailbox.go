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

// MailboxHandler — 信箱 API
type MailboxHandler struct {
	mailboxService *service.MailboxService
	mailboxRepo    *service.MailboxRepo
	convRepo       *service.ConversationRepo
	themeRepo      *service.ThemeStateRepo
	msgRepo        *service.MessageRepo
}

func NewMailboxHandler(mailboxService *service.MailboxService, mailboxRepo *service.MailboxRepo, convRepo *service.ConversationRepo, themeRepo *service.ThemeStateRepo, msgRepo *service.MessageRepo) *MailboxHandler {
	return &MailboxHandler{mailboxService: mailboxService, mailboxRepo: mailboxRepo, convRepo: convRepo, themeRepo: themeRepo, msgRepo: msgRepo}
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

	list, total, err := h.mailboxService.ListPacked(c.Request.Context(), userID, itemType, isRead, limit, offset)
	if err != nil {
		log.Printf("[mailbox] ListPacked error: %v", err)
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

// StartChat POST /api/mailbox/:id/chat — 从信箱消息创建/跳转对话
func (h *MailboxHandler) StartChat(c *gin.Context) {
	userID := c.GetString("user_id")
	itemID := c.Param("id")

	// 读信箱消息
	item, err := h.mailboxRepo.GetByIDAndUser(c.Request.Context(), itemID, userID)
	if err != nil {
		log.Printf("[mailbox] StartChat GetByID error: %v", err)
		view.JSONError(c, 404, "消息不存在")
		return
	}
	if item.EntityType != "theme" || item.EntityID == "" {
		view.JSONError(c, 400, "该消息不支持跳转对话")
		return
	}

	// 查主题是否已有关联对话
	theme, err := h.themeRepo.GetByID(c.Request.Context(), item.EntityID, userID)
	if err != nil {
		log.Printf("[mailbox] StartChat GetByID error: %v", err)
		view.JSONError(c, 500, "查询失败")
		return
	}
	if theme == nil {
		view.JSONError(c, 404, "主题不存在")
		return
	}

	// 已有绑定且对话未删除 → 直接跳转
	if theme.ConversationID != nil && *theme.ConversationID != "" {
		conv, err := h.convRepo.FindByID(c.Request.Context(), *theme.ConversationID)
		if err != nil {
			log.Printf("[mailbox] StartChat FindByID error: %v", err)
		}
		if err == nil && conv != nil && !conv.IsDeleted {
			if err := h.mailboxRepo.MarkRead(c.Request.Context(), itemID, userID); err != nil {
				log.Printf("[mailbox] StartChat MarkRead error: %v", err)
			}
			view.JSONOK(c, gin.H{"conversation_id": conv.ID, "created": false})
			return
		}
	}

	// 创建新对话
	extraJSON, err := json.Marshal(map[string]interface{}{
		"context": map[string]interface{}{
			"theme_id":        item.EntityID,
			"entity_type":     item.EntityType,
			"entity_id":       item.EntityID,
			"mailbox_item_id": itemID,
			"title":           item.Title,
		},
	})
	if err != nil {
		log.Printf("[mailbox] StartChat marshal extra error: %v", err)
		view.JSONError(c, 500, "序列化失败")
		return
	}
	convID := uuid.New().String()

	conv := &model.Conversation{
		ID:     convID,
		UserID: userID,
		Title:  item.Title,
		Extra:  string(extraJSON),
	}
	if err := h.convRepo.Create(c.Request.Context(), conv); err != nil {
		log.Printf("[mailbox] create conv error: %v", err)
		view.JSONError(c, 500, "创建对话失败")
		return
	}

	// candidate/draft → active（点击即确认）
	if theme.Status == "candidate" || theme.Status == "draft" {
		if err := h.themeRepo.UpdateStatus(c.Request.Context(), item.EntityID, userID, "active"); err != nil {
			log.Printf("[mailbox] StartChat UpdateStatus error: %v", err)
		}
	}

	// 插入 greeting 消息（复用信箱已有的 Secretary 文案）
	greetingMsgID := uuid.New().String()
	greetingContent := item.Greeting
	if greetingContent == "" {
		greetingContent = item.Content
	}
	if greetingContent == "" {
		greetingContent = item.Title
	}
	if err := h.msgRepo.Create(c.Request.Context(), &model.Message{
		ID:             greetingMsgID,
		ConversationID: convID,
		UserID:         userID,
		Role:           "assistant",
		Content:        greetingContent,
	}); err != nil {
		log.Printf("[mailbox] create greeting msg error: %v", err)
	}

	if err := h.themeRepo.UpdateConversationID(c.Request.Context(), item.EntityID, userID, convID); err != nil {
		log.Printf("[mailbox] StartChat UpdateConversationID error: %v", err)
	}
	if err := h.mailboxRepo.MarkRead(c.Request.Context(), itemID, userID); err != nil {
		log.Printf("[mailbox] StartChat MarkRead error: %v", err)
	}

	view.JSONOK(c, gin.H{
		"conversation_id": convID,
		"created":         true,
		"greeting": map[string]interface{}{
			"id":      greetingMsgID,
			"role":    "assistant",
			"content": greetingContent,
		},
	})
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
