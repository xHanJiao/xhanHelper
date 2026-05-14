package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"xhan-helper/internal/agent"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/monitor"
	"xhan-helper/internal/service"
	"xhan-helper/internal/view"
)

func chat(orch *agent.Orchestrator, convRepo *service.ConversationRepo,
	msgRepo *service.MessageRepo, agentRepo *service.AgentRepo) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			ConversationID  *string `json:"conversation_id"`
			Message         string  `json:"message"`
			DeepMode        bool    `json:"deep_mode"`
			AgentID         *string `json:"agent_id"`
			MemoryThreshold float64 `json:"memory_threshold"`
			MemoryLimit     int     `json:"memory_limit"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.Message == "" {
			view.JSONError(c, 400, "invalid request")
			return
		}

		userID := c.GetString("user_id")

		// 创建或加载会话
		var conv *model.Conversation
		if req.ConversationID != nil {
			var err error
			conv, err = convRepo.FindByID(c.Request.Context(), *req.ConversationID)
			if err != nil {
				log.Printf("[chat] FindByID error: %v", err)
			}
		}
		if conv == nil {
			conv = &model.Conversation{ID: uuid.New().String(), UserID: userID}
			if req.AgentID != nil && *req.AgentID != "" {
				conv.AgentID = req.AgentID
			}
			if err := convRepo.Create(c.Request.Context(), conv); err != nil {
				log.Printf("[chat] CreateConversation error: %v", err)
				view.JSONError(c, 500, "create conversation failed")
				return
			}
		}

		// 加载智能体 system prompt 和 LLM 参数（新会话和续聊都支持）
		var agentSystemPrompt string
		var agentID string
		var agentTemperature *float64
		var agentMaxTokens *int
		if conv.AgentID != nil && *conv.AgentID != "" && agentRepo != nil {
			a, err := agentRepo.FindByID(c.Request.Context(), *conv.AgentID, userID)
			if err != nil {
				log.Printf("[chat] FindByID agent error: %v", err)
			} else if a != nil {
				agentSystemPrompt = a.SystemPrompt
				agentID = a.ID
				if a.ParamsJSON != "" {
					var params struct {
						Temperature *float64 `json:"temperature"`
						MaxTokens   *int     `json:"max_tokens"`
					}
					if err := json.Unmarshal([]byte(a.ParamsJSON), &params); err != nil {
						log.Printf("[chat] parse agent params_json error: %v", err)
					} else {
						agentTemperature = params.Temperature
						agentMaxTokens = params.MaxTokens
					}
				}
			}
		}

		// —— 预处理管线 ——
		userSpecifiedAgent := req.AgentID != nil && *req.AgentID != ""
		pc := &agent.PreContext{
			RawMsg:             req.Message,
			UserID:             userID,
			UserSpecifiedAgent: userSpecifiedAgent,
		}
		pipelineCtx := agent.WithPreContext(c.Request.Context(), pc)

		pipeline, err := agent.NewPipeline(
			agent.NewShortInputCheckStep(),
			agent.NewNormalizeDatesStep(),
			agent.NewAutoRouteStep(orch.FlashClient(), agentRepo, conv.AgentID, conv.ID, msgRepo),
		)
		if err != nil {
			log.Printf("[chat] pipeline init error: %v", err)
		}
		if _, pipeErr := pipeline.Run(pipelineCtx); pipeErr != nil {
			log.Printf("[chat] pipeline run error: %v", pipeErr)
		}

		// 读取管线结果
		normalizedMsg := pc.NormalizedMsg
		if normalizedMsg == "" {
			normalizedMsg = req.Message
		}
		if pc.AgentID != "" && !userSpecifiedAgent {
			agentID = pc.AgentID
			agentSystemPrompt = pc.AgentSystemPrompt
			if conv.AgentID == nil || *conv.AgentID == "" {
				conv.AgentID = &pc.AgentID
				if err := convRepo.SetAgentID(c.Request.Context(), conv.ID, pc.AgentID); err != nil {
					log.Printf("[chat] SetAgentID error: %v", err)
				}
			}
		}

		history, err := msgRepo.ListByConversationRecent(c.Request.Context(), conv.ID, 100)
		if err != nil {
			log.Printf("[chat] ListByConversation error: %v", err)
		}

		w := c.Writer
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		flusher, ok := w.(http.Flusher)
		if !ok {
			log.Printf("[chat] streaming not supported: ResponseWriter does not implement http.Flusher")
			view.JSONError(c, 500, "streaming not supported")
			return
		}
		monitor.IncSSE()
		defer monitor.DecSSE()

		var messages []llm.Message
		if req.DeepMode {
			messages = agent.BuildMessagesForPro(history)
		} else {
			messages = agent.BuildMessagesForFlash(history)
		}

		events, err := orch.Run(context.Background(), &agent.RunRequest{
			UserID:             userID,
			ConversationID:     conv.ID,
			Messages:           messages,
			DeepMode:           req.DeepMode,
			UserMessageContent: normalizedMsg,
			AgentSystemPrompt:  agentSystemPrompt,
			AgentID:            agentID,
			MemoryThreshold:    req.MemoryThreshold,
			MemoryLimit:        req.MemoryLimit,
			SkipMemory:         pc.SkipMemory,
			Temperature:        agentTemperature,
			MaxTokens:          agentMaxTokens,
		})
		if err != nil {
			view.WriteSSE(w, flusher, "error", err.Error())
			return
		}

		// 发送 agent_matched 事件（如果管线匹配到了智能体）
		if pc.AgentName != "" && pc.Confidence > 0 {
			matchedJSON := fmt.Sprintf(`{"agent_id":"%s","agent_name":"%s","confidence":%.2f}`,
				pc.AgentID, pc.AgentName, pc.Confidence)
			view.WriteSSE(w, flusher, "agent_matched", matchedJSON)
		}

		for evt := range events {
			select {
			case <-c.Request.Context().Done():
				return
			default:
			}
			switch evt.Type {
			case "text_delta":
				view.WriteSSE(w, flusher, "message",
					`{"delta":"`+view.EscapeJSON(evt.Content)+`"}`)
			case "plan", "plan_node_start", "plan_node_done", "plan_done":
				view.WriteSSE(w, flusher, evt.Type, evt.Content)
			case "thought", "thought_delta", "thought_done":
				view.WriteSSE(w, flusher, evt.Type, evt.Content)
			case "status":
				view.WriteSSE(w, flusher, "status", evt.Content)
			case "tool_call":
				view.WriteSSE(w, flusher, "tool_call", evt.Content)
			case "tool_result":
				view.WriteSSE(w, flusher, "tool_result", evt.Content)
			case "done":
				doneJSON := mergeTitle(c.Request.Context(), orch, convRepo, conv, req.Message, evt.Content)
				view.WriteSSE(w, flusher, "done", doneJSON)
			case "error":
				view.WriteSSE(w, flusher, "error", evt.Error)
			}
		}
	}
}

func mergeTitle(ctx context.Context, orch *agent.Orchestrator, convRepo *service.ConversationRepo, conv *model.Conversation, userMsg, doneContent string) string {
	if conv.Title != "新对话" || userMsg == "" {
		return doneContent
	}
	t := orch.GenerateTitle(ctx, userMsg)
	if t == "" {
		return doneContent
	}
	convRepo.Rename(ctx, conv.ID, t)
	conv.Title = t

	var m map[string]interface{}
	if err := json.Unmarshal([]byte(doneContent), &m); err != nil {
		return doneContent
	}
	m["title"] = t
	b, _ := json.Marshal(m)
	return string(b)
}

func buildLLMMessages(history []model.Message) []llm.Message {
	msgs := make([]llm.Message, 0, len(history))
	for _, m := range history {
		msg := llm.Message{Role: m.Role, Content: m.Content}
		if m.ReasoningContent != nil {
			msg.ReasoningContent = *m.ReasoningContent
		}
		if m.ToolCallID != nil {
			msg.ToolCallID = *m.ToolCallID
		}
		if m.ToolCallsJSON != nil {
			var tcs []llm.ToolCall
			if err := json.Unmarshal([]byte(*m.ToolCallsJSON), &tcs); err == nil {
				msg.ToolCalls = tcs
			}
		}
		msgs = append(msgs, msg)
	}
	return msgs
}
