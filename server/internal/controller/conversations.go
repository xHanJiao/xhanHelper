package controller

import (
	"encoding/json"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"xhan-helper/internal/agent"
	"xhan-helper/internal/config"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
	"xhan-helper/internal/view"
)

// getDefaultConversation 获取或创建用户的默认会话
func getDefaultConversation(convRepo *service.ConversationRepo, agentRepo *service.AgentRepo) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetString("user_id")

		conv, err := convRepo.FindDefaultByUser(c.Request.Context(), userID)
		if err != nil {
			log.Printf("[conv] FindDefaultByUser error: %v", err)
		}
		if conv == nil {
			conv = &model.Conversation{
				ID:        uuid.New().String(),
				UserID:    userID,
				IsDefault: true,
			}
			if err := convRepo.Create(c.Request.Context(), conv); err != nil {
				log.Printf("[conv] CreateDefault error: %v", err)
				view.JSONError(c, 500, "create default conversation failed")
				return
			}
		}

		resp := map[string]interface{}{
			"id":         conv.ID,
			"title":      conv.Title,
			"is_default": conv.IsDefault,
			"agent_id":   conv.AgentID,
			"created_at": conv.CreatedAt,
			"updated_at": conv.UpdatedAt,
		}
		if conv.AgentID != nil && *conv.AgentID != "" && agentRepo != nil {
			a, err := agentRepo.FindByID(c.Request.Context(), *conv.AgentID, userID)
			if err != nil {
				log.Printf("[conv] default conv agent lookup error: %v", err)
			} else if a != nil {
				resp["agent_name"] = a.Name
			}
		}
		view.JSONOK(c, resp)
	}
}

// createConversation 创建新的（非默认）会话，可选绑定 agent
func createConversation(convRepo *service.ConversationRepo, agentRepo *service.AgentRepo) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetString("user_id")

		var req struct {
			AgentID   string `json:"agent_id"`
			Ephemeral bool   `json:"ephemeral"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			// 允许空 body，不传 agent_id 也可以
			req.AgentID = ""
		}

		conv := &model.Conversation{
			ID:        uuid.New().String(),
			UserID:    userID,
			IsDefault: false,
		}
		if req.Ephemeral {
			b, _ := json.Marshal(&model.ConversationExtra{Ephemeral: true})
			conv.Extra = string(b)
		}

		if req.AgentID != "" {
			if agentRepo != nil {
				a, err := agentRepo.FindByID(c.Request.Context(), req.AgentID, userID)
				if err != nil || a == nil {
					log.Printf("[conv] createConversation agent not found: id=%s err=%v", req.AgentID, err)
					view.JSONError(c, 400, "agent not found")
					return
				}
			}
			conv.AgentID = &req.AgentID
		}

		if err := convRepo.Create(c.Request.Context(), conv); err != nil {
			log.Printf("[conv] CreateConversation error: %v", err)
			view.JSONError(c, 500, "create conversation failed")
			return
		}

		resp := map[string]interface{}{
			"id":         conv.ID,
			"user_id":    conv.UserID,
			"title":      conv.Title,
			"is_default": conv.IsDefault,
			"ephemeral":  req.Ephemeral,
			"agent_id":   conv.AgentID,
			"created_at": conv.CreatedAt,
			"updated_at": conv.UpdatedAt,
		}
		resp["agent_name"] = ""
		if conv.AgentID != nil && *conv.AgentID != "" && agentRepo != nil {
			a, err := agentRepo.FindByID(c.Request.Context(), *conv.AgentID, userID)
			if err != nil {
				log.Printf("[conv] agent lookup error: %v", err)
			} else if a != nil {
				resp["agent_name"] = a.Name
			}
		}
		view.JSONOK(c, resp)
	}
}

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
				"pinned":     conv.PinnedAt != nil,
				"created_at": conv.CreatedAt,
				"updated_at": conv.UpdatedAt,
			}
			if conv.AgentID != nil && *conv.AgentID != "" {
				m["agent_name"] = agentNames[*conv.AgentID]
			}
			// 解析主题上下文 / 无痕标记
			if conv.Extra != "" {
				var extra model.ConversationExtra
				if json.Unmarshal([]byte(conv.Extra), &extra) == nil {
					m["ephemeral"] = extra.Ephemeral
					if extra.Context != nil && extra.Context.Title != "" {
						m["theme_title"] = extra.Context.Title
					}
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
		limit, _ := strconv.Atoi(c.DefaultQuery("limit", strconv.Itoa(service.DefaultCursorRounds)))
		cursor := c.Query("cursor")
		direction := c.DefaultQuery("direction", "before")

		result, err := msgRepo.ListByConversationCursorByRounds(c.Request.Context(), convID, cursor, direction, limit)
		if err != nil {
			log.Printf("[conv] GetMessagesCursor error: %v", err)
			view.JSONError(c, 500, "query messages failed")
			return
		}
		result.Messages = cleanMessages(result.Messages)
		view.JSONOK(c, result)
	}
}

// getConversationUsage 返回会话的上下文 token 用量与消耗金额。
// GET /api/conversations/:id/usage
func getConversationUsage(
	convRepo *service.ConversationRepo,
	msgRepo *service.MessageRepo,
	costRepo *service.LlmCostRepo,
	priceRepo *service.LlmPriceRepo,
	cfg *config.Config,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		convID := c.Param("id")
		if !requireConversationOwner(c, convRepo, convID) {
			return
		}
		ctx := c.Request.Context()

		// 上下文 token：最新 assistant 消息的 prompt_tokens（主循环最大上下文）。
		contextTokens := 0
		if msgs, err := msgRepo.ListByConversationRecent(ctx, convID, 10); err != nil {
			log.Printf("[conv] usage ListByConversationRecent error: %v", err)
		} else {
			for i := len(msgs) - 1; i >= 0; i-- {
				if msgs[i].Role != "assistant" || msgs[i].Extra == nil {
					continue
				}
				var extra agent.MsgExtra
				if err := json.Unmarshal([]byte(*msgs[i].Extra), &extra); err == nil && extra.Usage != nil {
					contextTokens = extra.Usage.PromptTokens
				}
				break
			}
		}

		// 窗口起点：最新 user 消息的 session_start_msg_id 对应消息时间；缺失回退到该 user 消息自身。
		var windowStart time.Time
		if users, err := msgRepo.ListRecentUserMessages(ctx, convID, 1); err != nil {
			log.Printf("[conv] usage ListRecentUserMessages error: %v", err)
		} else if len(users) > 0 {
			windowStart = users[0].CreatedAt
			if startID := agent.GetSessionStart(&users[0]); startID != "" {
				if startMsg, err := msgRepo.GetByIDCols(ctx, startID, "id", "created_at"); err != nil {
					log.Printf("[conv] usage GetByIDCols error: %v", err)
				} else if startMsg != nil {
					windowStart = startMsg.CreatedAt
				}
			}
		}

		prices, err := priceRepo.List(ctx)
		if err != nil {
			log.Printf("[conv] usage prices error: %v", err)
			view.JSONError(c, 500, "query model prices failed")
			return
		}
		total, window, latestModel, err := costRepo.ConversationCost(ctx, convID, windowStart, prices)
		if err != nil {
			log.Printf("[conv] usage cost error: %v", err)
			view.JSONError(c, 500, "query conversation cost failed")
			return
		}

		contextWindow := 0
		if cfg != nil {
			contextWindow = cfg.LLM.ContextWindowFor(latestModel)
		}

		// 子智能体（subAgent）：按「父会话 → 子会话」关系汇总（无窗口概念，全部累计）。
		subIDs, err := convRepo.ListSubConversationIDs(ctx, convID)
		if err != nil {
			log.Printf("[conv] usage ListSubConversationIDs error: %v", err)
			subIDs = nil
		}
		subagentCost, err := costRepo.SumCostByConvIDs(ctx, subIDs, prices)
		if err != nil {
			log.Printf("[conv] usage subagent cost error: %v", err)
			subagentCost = 0
		}

		view.JSONOK(c, gin.H{
			"context_tokens":    contextTokens,
			"context_window":    contextWindow,
			"window_cost":       window,
			"conversation_cost": total,
			"subagent_cost":     subagentCost,
			"subagent_count":    len(subIDs),
			"total_cost":        total + subagentCost,
		})
	}
}

// cleanMessages 过滤内部消息、清理调试字段、提取 Flash reply。
// 工具结果消息（IsInternal + Role="tool"）的内容会被注入到前一条 assistant 消息的
// tool_calls_json 中，然后丢弃。
func cleanMessages(msgs []model.Message) []model.Message {
	out := make([]model.Message, 0, len(msgs))
	for _, m := range msgs {
		if m.IsInternal {
			if m.Role == "tool" && len(out) > 0 {
				last := &out[len(out)-1]
				if last.Role == "assistant" && last.ToolCallsJSON != nil && *last.ToolCallsJSON != "" {
					injectToolResult(last, m.Content)
				}
			}
			continue
		}
		m.RequestJSON = nil
		m.ResponseJSON = nil
		if m.Role == "user" && m.OriginalContent != nil && *m.OriginalContent != "" {
			m.Content = *m.OriginalContent
		}
		if m.Role == "assistant" {
			if reply := extractFlashReply(m.Content); reply != "" {
				m.Content = reply
			} else if isFlashJSON(m.Content) {
				m.Content = ""
			}
		}
		out = append(out, m)
	}
	merged := mergeConsecutiveAssistant(out)

	// 过滤掉 content 为空的 assistant 消息（纯工具调用包装，无可见内容）
	filtered := make([]model.Message, 0, len(merged))
	for _, m := range merged {
		if m.Role == "assistant" && m.Content == "" {
			continue
		}
		filtered = append(filtered, m)
	}
	return filtered
}

// injectToolResult 将一条工具执行结果注入到 msg.ToolCallsJSON 数组中第一个尚未有
// result 字段的 tool call 上。多条 tool 消息按出现顺序依次调用即可一一对应。
func injectToolResult(msg *model.Message, resultJSON string) {
	var tcs []map[string]interface{}
	if err := json.Unmarshal([]byte(*msg.ToolCallsJSON), &tcs); err != nil {
		return
	}
	for i := range tcs {
		if _, ok := tcs[i]["result"]; !ok {
			var result interface{}
			if err := json.Unmarshal([]byte(resultJSON), &result); err != nil {
				tcs[i]["result"] = resultJSON
			} else {
				tcs[i]["result"] = result
			}
			break
		}
	}
	b, _ := json.Marshal(tcs)
	s := string(b)
	msg.ToolCallsJSON = &s
}

// mergeConsecutiveAssistant 合并连续的 assistant 消息（ReAct 多轮产生的碎片）。
// 拼接所有条的 content 和 reasoning，合并所有条的 tool_calls_json。
func mergeConsecutiveAssistant(msgs []model.Message) []model.Message {
	if len(msgs) <= 1 {
		return msgs
	}
	out := make([]model.Message, 0, len(msgs))
	i := 0
	for i < len(msgs) {
		m := msgs[i]
		if m.Role != "assistant" || agent.GetNoMerge(&m) {
			out = append(out, m)
			i++
			continue
		}
		// 收集连续的 assistant 消息
		group := []model.Message{m}
		j := i + 1
		for j < len(msgs) && msgs[j].Role == "assistant" && !agent.GetNoMerge(&msgs[j]) {
			group = append(group, msgs[j])
			j++
		}
		i = j

		if len(group) == 1 {
			out = append(out, m)
			continue
		}

		// 合并：拼接所有条的 content/reasoning，合并所有条的 tool_calls
		merged := group[len(group)-1]

		// 拼接所有非空 content，还原实时聊天中用户看到的完整文本流
		var contents []string
		for _, gm := range group {
			if gm.Content != "" {
				contents = append(contents, gm.Content)
			}
		}
		merged.Content = strings.Join(contents, "\n\n")

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
		// 合并 reasoning：拼接所有条有值的
		var reasonings []string
		for _, gm := range group {
			if gm.ReasoningContent != nil && *gm.ReasoningContent != "" {
				reasonings = append(reasonings, *gm.ReasoningContent)
			}
		}
		if len(reasonings) > 0 {
			s := strings.Join(reasonings, "\n\n")
			merged.ReasoningContent = &s
		} else {
			merged.ReasoningContent = nil
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

// isFlashJSON 判断 content 是否为合法的 Flash JSON（以 { 开头且含有 reply 字段）
func isFlashJSON(content string) bool {
	trimmed := strings.TrimSpace(content)
	if !strings.HasPrefix(trimmed, "{") {
		return false
	}
	var parsed struct {
		Reply string `json:"reply"`
	}
	return json.Unmarshal([]byte(trimmed), &parsed) == nil
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
			Pinned  *bool           `json:"pinned,omitempty"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			view.JSONError(c, 400, "invalid request")
			return
		}

		userID := c.GetString("user_id")
		resp := gin.H{"id": convID}
		updated := false

		// 处理 pinned
		if req.Pinned != nil {
			if err := convRepo.SetPinned(c.Request.Context(), convID, *req.Pinned); err != nil {
				log.Printf("[conv] SetPinned error: %v", err)
				view.JSONError(c, 500, "update pinned failed")
				return
			}
			resp["pinned"] = *req.Pinned
			updated = true
		}

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

// stopChat 显式终止运行中的 chat（ReAct 循环）。
// 通过关闭 chatStops 注册的 channel 传播终止信号；需会话归属校验。
func stopChat(convRepo *service.ConversationRepo) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			ConversationID string `json:"conversation_id"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.ConversationID == "" {
			view.JSONError(c, 400, "conversation_id is required")
			return
		}
		if !requireConversationOwner(c, convRepo, req.ConversationID) {
			return
		}
		if chatStops.Stop(req.ConversationID) {
			log.Printf("[chat] stop signal sent: conv=%s", req.ConversationID)
			view.JSONOK(c, gin.H{"stopped": true})
			return
		}
		view.JSONOK(c, gin.H{"stopped": false, "reason": "no running chat"})
	}
}

func deleteConversation(convRepo *service.ConversationRepo, destroyer *agent.EphemeralDestroyer) gin.HandlerFunc {
	return func(c *gin.Context) {
		convID := c.Param("id")
		if !requireConversationOwner(c, convRepo, convID) {
			return
		}

		// 无痕会话：硬销毁（Stage A 同步隐藏 + 入队 Stage B 物理删除），不做软删
		ephemeral, err := destroyer.IsEphemeral(c.Request.Context(), convID)
		if err != nil {
			log.Printf("[conv] IsEphemeral error: id=%s err=%v", convID, err)
			view.JSONError(c, 500, "query conversation failed")
			return
		}
		if ephemeral {
			if err := destroyer.BeginDestroy(c.Request.Context(), convID, c.GetString("user_id")); err != nil {
				log.Printf("[conv] ephemeral destroy error: id=%s err=%v", convID, err)
				view.JSONError(c, 500, "destroy failed")
				return
			}
			log.Printf("[conv] ephemeral conversation destroyed: id=%s", convID)
			view.JSONOK(c, gin.H{"status": "purge_scheduled"})
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

// updateConversationMode — 切换会话模式（work/plan）。
// 模式只能由用户/客户端显式切换（客户端 Tab 快捷键 / 手动调用），LLM 无任何控制权。
func updateConversationMode(convRepo *service.ConversationRepo) gin.HandlerFunc {
	return func(c *gin.Context) {
		convID := c.Param("id")
		if !requireConversationOwner(c, convRepo, convID) {
			return
		}
		var req struct {
			Mode string `json:"mode"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			view.JSONError(c, 400, "invalid request")
			return
		}
		if req.Mode != model.ConvModeWork && req.Mode != model.ConvModePlan {
			view.JSONError(c, 400, "mode must be work or plan")
			return
		}
		if err := convRepo.SetMode(c.Request.Context(), convID, req.Mode); err != nil {
			log.Printf("[conv] SetMode error: %v", err)
			view.JSONError(c, 500, "update mode failed")
			return
		}
		log.Printf("[conv] mode switched: conv=%s mode=%s (user=%s)", convID[:8], req.Mode, c.GetString("user_id"))
		view.JSONOK(c, gin.H{"id": convID, "mode": req.Mode})
	}
}

// getConversationPlan — 获取当前会话的执行计划（todo，前端展示用）。
func getConversationPlan(convRepo *service.ConversationRepo, planRepo *service.ConversationPlanRepo) gin.HandlerFunc {
	return func(c *gin.Context) {
		convID := c.Param("id")
		if !requireConversationOwner(c, convRepo, convID) {
			return
		}
		userID := c.GetString("user_id")
		plan, err := planRepo.Get(c.Request.Context(), convID, userID)
		if err != nil {
			log.Printf("[plan] get error: %v", err)
			view.JSONError(c, 500, "读取计划失败")
			return
		}
		if plan == nil {
			view.JSONOK(c, gin.H{"goal": "", "steps": []model.PlanStep{}})
			return
		}
		view.JSONOK(c, gin.H{
			"goal":  plan.Goal,
			"steps": service.ParsePlanSteps(plan),
		})
	}
}
