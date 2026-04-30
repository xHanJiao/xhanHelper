package controller

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"xhan-helper/internal/agent"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
	"xhan-helper/internal/view"
)

func chat(orch *agent.Orchestrator, convRepo *service.ConversationRepo,
	msgRepo *service.MessageRepo) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			ConversationID *string `json:"conversation_id"`
			Message        string  `json:"message"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.Message == "" {
			view.JSONError(c, 400, "invalid request")
			return
		}

		dev, _ := c.Get("device")
		device := dev.(*model.Device)

		// 获取或创建会话
		var conv *model.Conversation
		if req.ConversationID != nil {
			conv, _ = convRepo.FindByID(c.Request.Context(), *req.ConversationID)
		}
		if conv == nil {
			conv = &model.Conversation{ID: uuid.New().String(), DeviceID: device.ID}
			if err := convRepo.Create(c.Request.Context(), conv); err != nil {
				view.JSONError(c, 500, "create conversation failed")
				return
			}
		}

		// 保存用户消息
		userMsg := &model.Message{
			ID:             uuid.New().String(),
			ConversationID: conv.ID,
			Role:           "user",
			Content:        req.Message,
		}
		if err := msgRepo.Create(c.Request.Context(), userMsg); err != nil {
			view.JSONError(c, 500, "save message failed")
			return
		}

		// 加载历史（包含刚保存的用户消息）
		history, _ := msgRepo.ListByConversation(c.Request.Context(), conv.ID, 20)

		// SSE headers
		w := c.Writer
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		flusher, ok := w.(http.Flusher)
		if !ok {
			view.JSONError(c, 500, "streaming not supported")
			return
		}

		events, err := orch.Run(c.Request.Context(), &agent.RunRequest{
			DeviceID: device.ID,
			Messages: buildLLMMessages(history),
		})
		if err != nil {
			view.WriteSSE(w, flusher, "error", err.Error())
			return
		}

		var fullContent strings.Builder
		for evt := range events {
			select {
			case <-c.Request.Context().Done():
				return
			default:
			}
			switch evt.Type {
			case "text_delta":
				fullContent.WriteString(evt.Content)
				view.WriteSSE(w, flusher, "message",
					`{"delta":"`+view.EscapeJSON(evt.Content)+`"}`)
			case "tool_call":
				view.WriteSSE(w, flusher, "tool_call", evt.Content)
			case "tool_result":
				view.WriteSSE(w, flusher, "tool_result", evt.Content)
			case "done":
				if fullContent.Len() > 0 {
					msgRepo.Create(c.Request.Context(), &model.Message{
						ID:             uuid.New().String(),
						ConversationID: conv.ID,
						Role:           "assistant",
						Content:        fullContent.String(),
					})
				}
				view.WriteSSE(w, flusher, "done",
					`{"conversation_id":"`+conv.ID+`"}`)
			case "error":
				view.WriteSSE(w, flusher, "error", evt.Error)
			}
		}
	}
}

func buildLLMMessages(history []model.Message) []llm.Message {
	msgs := make([]llm.Message, 0, len(history))
	for _, m := range history {
		msgs = append(msgs, llm.Message{Role: m.Role, Content: m.Content})
	}
	return msgs
}
