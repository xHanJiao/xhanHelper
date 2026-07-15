package controller

import (
	"log"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
	"xhan-helper/internal/view"
)

type ReminderHandler struct {
	reminderRepo *service.ReminderRepo
	convRepo     *service.ConversationRepo
	msgRepo      *service.MessageRepo
}

func NewReminderHandler(reminderRepo *service.ReminderRepo, convRepo *service.ConversationRepo, msgRepo *service.MessageRepo) *ReminderHandler {
	return &ReminderHandler{
		reminderRepo: reminderRepo,
		convRepo:     convRepo,
		msgRepo:      msgRepo,
	}
}

// ListReminders GET /api/reminders?status=&limit=20&offset=0
func (h *ReminderHandler) ListReminders(c *gin.Context) {
	userID := c.GetString("user_id")
	status := c.Query("status")
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	list, total, err := h.reminderRepo.List(c.Request.Context(), userID, status, limit, offset)
	if err != nil {
		log.Printf("[reminders] List error: %v", err)
		view.JSONError(c, 500, "query reminders failed")
		return
	}

	view.JSONOK(c, gin.H{"items": list, "total": total})
}

// AckReminder POST /api/reminders/:id/ack
func (h *ReminderHandler) AckReminder(c *gin.Context) {
	userID := c.GetString("user_id")
	id := c.Param("id")

	rem, err := h.reminderRepo.FindByID(c.Request.Context(), userID, id)
	if err != nil {
		log.Printf("[reminders] ack find error: %v", err)
		view.JSONError(c, 404, "reminder not found")
		return
	}

	if rem.TriggerMessageMsgID != "" {
		view.JSONOK(c, gin.H{
			"id":                     rem.ID,
			"status":                 rem.Status,
			"acknowledged_at":        rem.AcknowledgedAt,
			"trigger_message":        rem.TriggerMessage,
			"trigger_message_msg_id": rem.TriggerMessageMsgID,
		})
		return
	}

	if rem.Status == model.ReminderStatusCancelled {
		view.JSONError(c, 400, "此提醒已取消")
		return
	}

	if rem.Status == model.ReminderStatusGenerating {
		view.JSONError(c, 400, "提醒通知尚未生成，请稍后再试")
		return
	}

	triggerMsg := rem.TriggerMessage
	if triggerMsg == "" {
		triggerMsg = rem.Content
	}

	defaultConv, err := h.convRepo.FindDefaultByUser(c.Request.Context(), userID)
	if err != nil {
		log.Printf("[reminders] find default conv error: %v", err)
	}
	if defaultConv == nil {
		defaultConv = &model.Conversation{
			ID:        uuid.New().String(),
			UserID:    userID,
			IsDefault: true,
		}
		if err := h.convRepo.Create(c.Request.Context(), defaultConv); err != nil {
			log.Printf("[reminders] create default conv error: %v", err)
			view.JSONError(c, 500, "create default conversation failed")
			return
		}
	}

	msg := &model.Message{
		ID:             uuid.New().String(),
		ConversationID: defaultConv.ID,
		UserID:         userID,
		Role:           "assistant",
		Content:        triggerMsg,
		Status:         model.MsgStatusNormal,
	}
	if err := h.msgRepo.Create(c.Request.Context(), msg); err != nil {
		log.Printf("[reminders] create message error: %v", err)
		view.JSONError(c, 500, "insert trigger message failed")
		return
	}

	if err := h.reminderRepo.Ack(c.Request.Context(), userID, id, msg.ID); err != nil {
		log.Printf("[reminders] ack error: %v", err)
		view.JSONError(c, 500, "ack reminder failed")
		return
	}

	now := time.Now()
	log.Printf("[reminders] ack'd: %s msg=%s", id[:8], msg.ID[:8])
	view.JSONOK(c, gin.H{
		"id":                     rem.ID,
		"status":                 model.ReminderStatusAcknowledged,
		"acknowledged_at":        now,
		"trigger_message":        triggerMsg,
		"trigger_message_msg_id": msg.ID,
	})
}

// CancelReminder POST /api/reminders/:id/cancel
func (h *ReminderHandler) CancelReminder(c *gin.Context) {
	userID := c.GetString("user_id")
	id := c.Param("id")

	rem, err := h.reminderRepo.FindByID(c.Request.Context(), userID, id)
	if err != nil {
		log.Printf("[reminders] cancel find error: %v", err)
		view.JSONError(c, 404, "reminder not found")
		return
	}

	if rem.Status == model.ReminderStatusAcknowledged {
		view.JSONError(c, 400, "已确认的提醒不能取消")
		return
	}

	if err := h.reminderRepo.Cancel(c.Request.Context(), userID, id); err != nil {
		log.Printf("[reminders] cancel error: %v", err)
		view.JSONError(c, 500, "cancel reminder failed")
		return
	}

	log.Printf("[reminders] cancelled: %s", id[:8])
	view.JSONOK(c, gin.H{"id": id, "status": model.ReminderStatusCancelled})
}
