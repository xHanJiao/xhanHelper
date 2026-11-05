package controller

import (
	"context"
	"log"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"xhan-helper/internal/agent"
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
	limit := parseListLimit(c, 20)
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
//
// 点通知后把「触发消息」写回**创建这条提醒的那段对话**，并把它的 id 返回给客户端去打开。
// 不落默认会话（那个概念正在淡出）：提醒没有来源会话（秘书 / 反思自动生成）或那段对话已删，
// 就只置已确认、不写任何消息，客户端收到空 conversation_id 便原地不动。
func (h *ReminderHandler) AckReminder(c *gin.Context) {
	userID := c.GetString("user_id")
	id := c.Param("id")

	rem, err := h.reminderRepo.FindByID(c.Request.Context(), userID, id)
	if err != nil {
		log.Printf("[reminders] ack find error: %v", err)
		view.JSONError(c, 404, "reminder not found")
		return
	}

	// 已经 ack 过：幂等返回，不重复插消息（但仍带上会话，重复点通知照样能跳过去）。
	// 判据用 status 而不是 TriggerMessageMsgID：没有来源会话时 msgID 恒为空，
	// 只看 msgID 会让这类提醒每次点都重跑一遍（刷新 acknowledged_at、重复弹文案）。
	if rem.Status == model.ReminderStatusAcknowledged {
		view.JSONOK(c, ackPayload(rem, rem.Status, rem.AcknowledgedAt, rem.TriggerMessage, rem.TriggerMessageMsgID,
			ackTargetConversation(c.Request.Context(), h, userID, rem.ConversationID)))
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

	msgID := ""
	conv := ackTargetConversation(c.Request.Context(), h, userID, rem.ConversationID)
	if conv != nil {
		msg := &model.Message{
			ID:             uuid.New().String(),
			ConversationID: conv.ID,
			UserID:         userID,
			Role:           "assistant",
			Content:        triggerMsg,
			Status:         model.MsgStatusNormal,
		}
		agent.SetNoMerge(msg)
		if err := h.msgRepo.Create(c.Request.Context(), msg); err != nil {
			log.Printf("[reminders] create message error: %v", err)
			view.JSONError(c, 500, "insert trigger message failed")
			return
		}
		msgID = msg.ID
	}

	if err := h.reminderRepo.Ack(c.Request.Context(), userID, id, msgID); err != nil {
		log.Printf("[reminders] ack error: %v", err)
		view.JSONError(c, 500, "ack reminder failed")
		return
	}

	now := time.Now()
	log.Printf("[reminders] ack'd: %s msg=%q conv=%q", id, msgID, rem.ConversationID)
	view.JSONOK(c, ackPayload(rem, model.ReminderStatusAcknowledged, now, triggerMsg, msgID, conv))
}

// ackTargetConversation 点通知后该落到哪段对话：提醒的创建会话仍然在、属于本人、没被删就返回它。
// 其余情况返回 nil（调用方据此不写消息、不跳转）。查库失败按可降级处理：记日志后当没有来源。
func ackTargetConversation(ctx context.Context, h *ReminderHandler, userID, convID string) *model.Conversation {
	if convID == "" {
		return nil
	}
	conv, err := h.convRepo.FindByID(ctx, convID)
	if err != nil {
		log.Printf("[reminders] ack find conv %s error: %v", convID, err)
		return nil
	}
	// 子会话（subAgent）不在用户的会话列表里（列表按 parent_id IS NULL 过滤），
	// 往那儿写消息等于写进用户看不见的地方。
	if conv == nil || conv.UserID != userID || conv.IsDeleted || conv.ParentID != nil {
		return nil
	}
	return conv
}

// ackPayload 组 ack 响应。conv 为 nil（没有可用来源会话）时两个会话字段为空串。
func ackPayload(rem *model.Reminder, status string, ackedAt time.Time, trigger, msgID string,
	conv *model.Conversation) gin.H {
	convID, convTitle := "", ""
	if conv != nil {
		convID, convTitle = conv.ID, conv.Title
	}
	return gin.H{
		"id":                     rem.ID,
		"status":                 status,
		"acknowledged_at":        ackedAt,
		"trigger_message":        trigger,
		"trigger_message_msg_id": msgID,
		"conversation_id":        convID,
		"conversation_title":     convTitle,
	}
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
