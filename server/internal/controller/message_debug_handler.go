package controller

import (
	"log"
	"strconv"

	"github.com/gin-gonic/gin"

	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
	"xhan-helper/internal/view"
)

// MessageDebugHandler — 消息调试查询（管理端）
type MessageDebugHandler struct {
	msgRepo *service.MessageRepo
}

func NewMessageDebugHandler(msgRepo *service.MessageRepo) *MessageDebugHandler {
	return &MessageDebugHandler{msgRepo: msgRepo}
}

// GetMessageDebug 返回以指定用户消息为锚点的一轮完整消息（不含重量级字段）。
func (h *MessageDebugHandler) GetMessageDebug(c *gin.Context) {
	msgID := c.Param("id")
	if msgID == "" {
		view.JSONError(c, 400, "缺少消息 ID")
		return
	}

	anchor, err := h.msgRepo.GetByID(c.Request.Context(), msgID)
	if err != nil || anchor == nil {
		log.Printf("[debug] get anchor msg %s error: %v", msgID, err)
		view.JSONError(c, 404, "消息不存在")
		return
	}
	if anchor.Role != "user" {
		view.JSONError(c, 400, "仅支持以用户消息为锚点")
		return
	}

	// 获取该时间点之后的消息，limit 默认 200，可通过 query 参数覆盖
	limit := 200
	if l, err := strconv.Atoi(c.Query("limit")); err == nil && l > 0 && l <= 1000 {
		limit = l
	}
	msgs, err := h.msgRepo.GetMessagesSince(c.Request.Context(), anchor.ConversationID, anchor.CreatedAt, limit)
	if err != nil {
		log.Printf("[debug] GetMessagesSince error: %v", err)
		view.JSONError(c, 500, "查询消息失败")
		return
	}

	// 截取到下一个 user 消息之前
	roundMsgs := cutRound(msgs, msgID)

	view.JSONOK(c, gin.H{
		"anchor_msg":     cleanForDebug(anchor),
		"round_messages": cleanForDebugList(roundMsgs),
	})
}

// GetMessageDebugDetail 返回单条消息的指定重量级字段。
func (h *MessageDebugHandler) GetMessageDebugDetail(c *gin.Context) {
	msgID := c.Param("id")
	field := c.Query("field")
	if field != "request_json" && field != "extra" {
		view.JSONError(c, 400, "field 必须为 request_json 或 extra")
		return
	}

	msg, err := h.msgRepo.GetByID(c.Request.Context(), msgID)
	if err != nil || msg == nil {
		log.Printf("[debug] get msg %s error: %v", msgID, err)
		view.JSONError(c, 404, "消息不存在")
		return
	}

	var value *string
	switch field {
	case "request_json":
		value = msg.RequestJSON
	case "extra":
		value = msg.Extra
	}

	view.JSONOK(c, gin.H{
		"id":    msg.ID,
		"field": field,
		"value": value,
	})
}

// cutRound 从消息列表中截取以 anchorID 开头到下一个 user 消息之间的一轮。
func cutRound(msgs []model.Message, anchorID string) []model.Message {
	var result []model.Message
	started := false
	for i := range msgs {
		m := &msgs[i]
		if m.ID == anchorID {
			started = true
		}
		if !started {
			continue
		}
		if m.Role == "user" && m.ID != anchorID {
			break
		}
		result = append(result, *m)
	}
	return result
}

// cleanForDebug 清除调试字段，但保留 request_json 和 extra（供 detail API 使用）。
func cleanForDebug(msg *model.Message) model.Message {
	c := *msg
	// 不返回重量级字段，由 detail API 懒加载
	c.RequestJSON = nil
	c.ResponseJSON = nil
	c.Extra = nil
	return c
}

func cleanForDebugList(msgs []model.Message) []model.Message {
	out := make([]model.Message, len(msgs))
	for i := range msgs {
		out[i] = cleanForDebug(&msgs[i])
	}
	return out
}
