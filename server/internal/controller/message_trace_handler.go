package controller

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"xhan-helper/internal/service"
	"xhan-helper/internal/view"
)

// MessageTraceHandler — 消息 Trace 查询（管理端）
type MessageTraceHandler struct {
	msgRepo *service.MessageRepo
}

func NewMessageTraceHandler(msgRepo *service.MessageRepo) *MessageTraceHandler {
	return &MessageTraceHandler{msgRepo: msgRepo}
}

// ListMessageTraces 分页列出有 prompt trace 的消息
func (h *MessageTraceHandler) ListMessageTraces(c *gin.Context) {
	userID := c.GetString("user_id")
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	list, total, err := h.msgRepo.ListWithTrace(c.Request.Context(), userID, limit, offset)
	if err != nil {
		view.JSONError(c, 500, "查询失败: "+err.Error())
		return
	}

	view.JSONOK(c, gin.H{
		"items": list,
		"total": total,
	})
}
