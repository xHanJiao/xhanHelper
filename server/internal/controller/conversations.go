package controller

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"xhan-helper/internal/agent"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
	"xhan-helper/internal/view"
)

func listConversations(convRepo *service.ConversationRepo, agentRepo *service.AgentRepo) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetString("user_id")
		limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
		offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
		list, total, err := convRepo.ListByUser(c.Request.Context(), userID, limit, offset)
		if err != nil {
			log.Printf("[conv] List error: %v", err)
			view.JSONError(c, 500, "query conversations failed")
			return
		}

		// 收集 agent_id，批量查名称
		agentNames := make(map[string]string)
		if agentRepo != nil {
			var ids []string
			for _, conv := range list {
				if conv.AgentID != nil && *conv.AgentID != "" {
					ids = append(ids, *conv.AgentID)
				}
			}
			if len(ids) > 0 {
				agents, err := agentRepo.FindByIDs(c.Request.Context(), ids)
				if err != nil {
					log.Printf("[conv] FindByIDs error: %v", err)
				}
				for _, a := range agents {
					agentNames[a.ID] = a.Name
				}
			}
		}

		// 构建带 agent_name 的响应
		result := make([]map[string]interface{}, len(list))
		for i, conv := range list {
			m := map[string]interface{}{
				"id":         conv.ID,
				"user_id":    conv.UserID,
				"title":      conv.Title,
				"agent_id":   conv.AgentID,
				"created_at": conv.CreatedAt,
				"updated_at": conv.UpdatedAt,
			}
			if conv.AgentID != nil && *conv.AgentID != "" {
				m["agent_name"] = agentNames[*conv.AgentID]
			}
			// 解析主题上下文
			if conv.Extra != "" {
				var extra model.ConversationExtra
				if json.Unmarshal([]byte(conv.Extra), &extra) == nil && extra.Context != nil && extra.Context.Title != "" {
					m["theme_title"] = extra.Context.Title
				}
			}
			result[i] = m
		}
		view.JSONOK(c, gin.H{"conversations": result, "total": total})
	}
}

// requireConversationOwner 校验会话归属，非 owner 返回 403，未找到返回 404
func requireConversationOwner(c *gin.Context, convRepo *service.ConversationRepo, convID string) bool {
	conv, err := convRepo.FindByID(c.Request.Context(), convID)
	if err != nil || conv == nil {
		view.JSONError(c, 404, "conversation not found")
		return false
	}
	userID := c.GetString("user_id")
	if conv.UserID != userID {
		view.JSONError(c, 403, "access denied")
		return false
	}
	return true
}

func getMessages(convRepo *service.ConversationRepo,
	msgRepo *service.MessageRepo) gin.HandlerFunc {
	return func(c *gin.Context) {
		convID := c.Param("id")
		if !requireConversationOwner(c, convRepo, convID) {
			return
		}
		msgs, err := msgRepo.ListByConversation(c.Request.Context(), convID, 100)
		if err != nil {
			log.Printf("[conv] GetMessages error: %v", err)
			view.JSONError(c, 500, "query messages failed")
			return
		}
		msgs = cleanMessages(msgs)
		view.JSONOK(c, msgs)
	}
}

func getMessagesCursor(msgRepo *service.MessageRepo, convRepo *service.ConversationRepo) gin.HandlerFunc {
	return func(c *gin.Context) {
		convID := c.Param("id")
		if !requireConversationOwner(c, convRepo, convID) {
			return
		}
		limit, _ := strconv.Atoi(c.DefaultQuery("limit", "10"))
		cursor := c.Query("cursor")
		direction := c.DefaultQuery("direction", "before")

		result, err := msgRepo.ListByConversationCursor(c.Request.Context(), convID, cursor, direction, limit)
		if err != nil {
			log.Printf("[conv] GetMessagesCursor error: %v", err)
			view.JSONError(c, 500, "query messages failed")
			return
		}
		result.Messages = cleanMessages(result.Messages)
		view.JSONOK(c, result)
	}
}

// cleanMessages 过滤内部消息、清理调试字段、提取 Flash reply
func cleanMessages(msgs []model.Message) []model.Message {
	out := make([]model.Message, 0, len(msgs))
	for _, m := range msgs {
		if m.IsInternal {
			continue
		}
		m.RequestJSON = nil
		m.ResponseJSON = nil
		if m.Role == "assistant" {
			if reply := extractFlashReply(m.Content); reply != "" {
				m.Content = reply
			}
		}
		out = append(out, m)
	}
	return mergeConsecutiveAssistant(out)
}

// mergeConsecutiveAssistant 合并连续的 assistant 消息（ReAct 多轮产生的碎片）。
// 取最后一条的 content，合并所有条的 tool_calls_json。
func mergeConsecutiveAssistant(msgs []model.Message) []model.Message {
	if len(msgs) <= 1 {
		return msgs
	}
	out := make([]model.Message, 0, len(msgs))
	i := 0
	for i < len(msgs) {
		m := msgs[i]
		if m.Role != "assistant" {
			out = append(out, m)
			i++
			continue
		}
		// 收集连续的 assistant 消息
		group := []model.Message{m}
		j := i + 1
		for j < len(msgs) && msgs[j].Role == "assistant" {
			group = append(group, msgs[j])
			j++
		}
		i = j

		if len(group) == 1 {
			out = append(out, m)
			continue
		}

		// 合并：取最后一条的 content/reasoning，合并所有条的 tool_calls
		merged := group[len(group)-1]
		var allTCs []json.RawMessage
		for _, gm := range group {
			if gm.ToolCallsJSON == nil || *gm.ToolCallsJSON == "" {
				continue
			}
			var tcs []json.RawMessage
			if err := json.Unmarshal([]byte(*gm.ToolCallsJSON), &tcs); err != nil {
				continue
			}
			allTCs = append(allTCs, tcs...)
		}
		if len(allTCs) > 0 {
			b, _ := json.Marshal(allTCs)
			s := string(b)
			merged.ToolCallsJSON = &s
		}
		// 合并 reasoning：取最后一条有值的
		for k := len(group) - 1; k >= 0; k-- {
			if group[k].ReasoningContent != nil && *group[k].ReasoningContent != "" {
				merged.ReasoningContent = group[k].ReasoningContent
				break
			}
		}
		out = append(out, merged)
	}
	return out
}

// extractFlashReply 从 Flash LLM 的 JSON 输出中提取 reply 字段 + JSON 前的纯文本
func extractFlashReply(content string) string {
	var parsed struct {
		Reply string `json:"reply"`
	}
	prefix, err := agent.ParseFlashJSON(content, &parsed)
	if err != nil || parsed.Reply == "" {
		return prefix
	}
	if prefix != "" {
		return prefix + "\n\n" + parsed.Reply
	}
	return parsed.Reply
}

func deleteMessage(msgRepo *service.MessageRepo, convRepo *service.ConversationRepo) gin.HandlerFunc {
	return func(c *gin.Context) {
		convID := c.Param("id")
		msgID := c.Param("msgId")
		if !requireConversationOwner(c, convRepo, convID) {
			return
		}
		userID := c.GetString("user_id")
		if err := msgRepo.SoftDelete(c.Request.Context(), msgID, userID); err != nil {
			log.Printf("[conv] DeleteMessage error: %v", err)
			view.JSONError(c, 500, "delete message failed")
			return
		}
		view.JSONOK(c, gin.H{"deleted": true})
	}
}

func renameConversation(convRepo *service.ConversationRepo, agentRepo *service.AgentRepo) gin.HandlerFunc {
	return func(c *gin.Context) {
		convID := c.Param("id")
		if !requireConversationOwner(c, convRepo, convID) {
			return
		}
		var req struct {
			Title   string          `json:"title"`
			AgentID json.RawMessage `json:"agent_id,omitempty"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			view.JSONError(c, 400, "invalid request")
			return
		}

		userID := c.GetString("user_id")
		resp := gin.H{"id": convID}
		updated := false

		// 处理 agent_id
		if len(req.AgentID) > 0 {
			agentIDStr := string(req.AgentID)
			if agentIDStr == "null" {
				if err := convRepo.UpdateAgentID(c.Request.Context(), convID, nil); err != nil {
					log.Printf("[conv] UpdateAgentID (unbind) error: %v", err)
					view.JSONError(c, 500, "update agent_id failed")
					return
				}
				resp["agent_id"] = nil
				resp["agent_name"] = ""
				updated = true
			} else {
				var agentID string
				if err := json.Unmarshal(req.AgentID, &agentID); err != nil || agentID == "" {
					view.JSONError(c, 400, "invalid agent_id")
					return
				}
				if agentRepo != nil {
					agents, err := agentRepo.FindByIDs(c.Request.Context(), []string{agentID})
					if err != nil || len(agents) == 0 {
						log.Printf("[conv] agent not found: id=%s err=%v", agentID, err)
						view.JSONError(c, 400, "agent not found")
						return
					}
					if agents[0].UserID != userID {
						view.JSONError(c, 403, "access denied")
						return
					}
					resp["agent_name"] = agents[0].Name
				}
				if err := convRepo.UpdateAgentID(c.Request.Context(), convID, &agentID); err != nil {
					log.Printf("[conv] UpdateAgentID error: %v", err)
					view.JSONError(c, 500, "update agent_id failed")
					return
				}
				resp["agent_id"] = agentID
				updated = true
			}
		}

		// 处理 title
		if req.Title != "" {
			if err := convRepo.Rename(c.Request.Context(), convID, req.Title); err != nil {
				log.Printf("[conv] Rename error: %v", err)
				view.JSONError(c, 500, "rename failed")
				return
			}
			resp["title"] = req.Title
			updated = true
		}

		if !updated {
			view.JSONError(c, 400, "nothing to update")
			return
		}

		view.JSONOK(c, resp)
	}
}

func deleteConversation(convRepo *service.ConversationRepo) gin.HandlerFunc {
	return func(c *gin.Context) {
		convID := c.Param("id")
		if !requireConversationOwner(c, convRepo, convID) {
			return
		}
		if err := convRepo.SoftDelete(c.Request.Context(), convID); err != nil {
			log.Printf("[conv] Delete error: %v", err)
			view.JSONError(c, 500, "delete failed")
			return
		}
		view.JSONOK(c, gin.H{"status": "deleted"})
	}
}

func extractMemories(convRepo *service.ConversationRepo, extractor *agent.MemoryExtractor) gin.HandlerFunc {
	return func(c *gin.Context) {
		convID := c.Param("id")
		if !requireConversationOwner(c, convRepo, convID) {
			return
		}
		conv, err := convRepo.FindByID(c.Request.Context(), convID)
		if err != nil {
			log.Printf("[conv] FindByID error: %v", err)
		}
		if conv == nil {
			view.JSONError(c, 404, "conversation not found")
			return
		}
		go extractor.ExtractFromConversation(context.Background(), conv)
		view.JSONOK(c, gin.H{"status": "extracting"})
	}
}

func extractMemoriesStream(convRepo *service.ConversationRepo, extractor *agent.MemoryExtractor) gin.HandlerFunc {
	return func(c *gin.Context) {
		convID := c.Param("id")
		if !requireConversationOwner(c, convRepo, convID) {
			return
		}
		conv, err := convRepo.FindByID(c.Request.Context(), convID)
		if err != nil {
			log.Printf("[conv] FindByID error: %v", err)
		}
		if conv == nil {
			view.JSONError(c, 404, "conversation not found")
			return
		}

		w := c.Writer
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		flusher, ok := w.(http.Flusher)
		if !ok {
			view.JSONError(c, 500, "streaming not supported")
			return
		}

		cb := func(evt agent.ExtractEvent) {
			data, _ := json.Marshal(evt)
			view.WriteSSE(w, flusher, evt.Type, string(data))
		}

		extractor.ExtractFromConversationWithCallback(c.Request.Context(), conv, cb)
	}
}
