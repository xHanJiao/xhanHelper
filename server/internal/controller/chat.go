package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"xhan-helper/internal/agent"
	"xhan-helper/internal/agent/pipeline"
	"xhan-helper/internal/agent/retrieval"
	"xhan-helper/internal/config"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/llmcost"
	"xhan-helper/internal/model"
	"xhan-helper/internal/monitor"
	"xhan-helper/internal/service"
	"xhan-helper/internal/view"
)

// planModeNotice — plan 模式每轮注入的提示（notice 块，仅 LLM 可见，用户不可见）。
// 动态内容绝不放 system prompt（会破坏前缀缓存），全部注入当前 user 消息的 XMLContext。
// 注意：首句必须保留 "当前处于计划模式（只读讨论状态）" 前缀（planNoticeMarker 依赖它做退出提示检测）。
//
// 【只读化（2026-09-15）】计划模式由"纯讨论"改为"读放行、写拦截"：允许只读工具
// （检索/查询类）支撑方案讨论，写操作由服务端执行级强制拦截 + prompt 级不注入写工具双保险。
//
// 【降级说明（2026-08-02）】非 coding 场景实测：计划模式讨论完 → 落文档即终点，
// todo 执行追踪用不上。降级为"方案讨论 + 落文档"两段式；plan 工具/conversation_plans
// 表/GET plans API/客户端 todo 列表保留为可选能力（LLM 主动用时仍可用）。
const planModeNotice = `[计划模式] 当前处于计划模式（只读讨论状态）：
- 本模式仅允许只读操作：可使用检索/查询类工具（记忆/笔记/历史/主题/文档/账单查询、日期、计算等）收集信息
- 禁止任何写操作：不写笔记/文档、不记账、不修改主题、不启动子任务；写操作由服务端强制拦截
- 请基于对话历史、只读工具结果与已注入的记忆/笔记/主题上下文进行方案讨论与设计
- 输出完整方案：目标、现状与信息收集、方案设计、利弊权衡
- 方案将在退出计划模式后整理为文档，本阶段只讨论
- 如用户要求直接执行写操作，请告知用户先退出计划模式`

// planExitNotice — 退出 plan 模式后首条消息注入一次（notice 块，仅 LLM 可见）。
// 降级说明同 planModeNotice：去掉 plan.save 步骤引导，落文档即终点。
const planExitNotice = `[系统] 用户已退出计划模式（只读讨论结束）。若对话中已讨论出方案且用户未放弃：
1. 用 document 工具将方案整理为 markdown 文档（正式产出，便于用户阅读）
// TODO(todo 降级)：原引导含"从方案提取实施步骤 plan.save 保存"，非 coding 场景用不上
2. 询问用户下一步（是否基于方案执行具体操作，或就此结束）
若未讨论出方案或用户已放弃：正常响应用户当前消息`

// planNoticeMarker / planExitNoticeMarker — 注入检测标记（子串匹配，区分两类提示）。
const (
	planNoticeMarker     = "当前处于计划模式（只读讨论状态）"
	planExitNoticeMarker = "用户已退出计划模式（只读讨论结束）"
)

func chat(orch *agent.Orchestrator, cfg *config.Config, convRepo *service.ConversationRepo,
	msgRepo *service.MessageRepo, agentRepo *service.AgentRepo) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			ConversationID *string                `json:"conversation_id"`
			Message        string                 `json:"message"`
			DeepMode       bool                   `json:"deep_mode"`
			AgentID        *string                `json:"agent_id"`
			Memory         retrieval.SourceConfig `json:"memory"`
			InitMode       string                 `json:"init_mode"` // 可选：仅新建会话时应用的初始模式（work/plan）；已有会话忽略

			Temperature *float64           `json:"temperature"`
			MaxTokens   *int               `json:"max_tokens"`
			Thinking    *bool              `json:"thinking"`
			ClientTools []agent.ClientTool `json:"client_tools"`
			// 响应检验（verify）开关：nil=跟随服务端全局配置，true/false=强制覆盖
			VerifyEnabled *bool `json:"verify_enabled"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.Message == "" {
			view.JSONError(c, 400, "invalid request")
			return
		}

		userID := c.GetString("user_id")

		// 创建或加载会话
		var conv *model.Conversation
		explicitID := req.ConversationID != nil && *req.ConversationID != ""
		if explicitID {
			var err error
			conv, err = convRepo.FindByID(c.Request.Context(), *req.ConversationID)
			if err != nil {
				log.Printf("[chat] FindByID error: %v", err)
			}
			// 指定的会话不存在（如无痕会话已被销毁）→ 直接报错，不静默新建
			if conv == nil {
				view.JSONError(c, 404, "conversation not found")
				return
			}
		} else if req.ConversationID == nil {
			// 新入口：查找默认会话，不存在则创建
			conv, _ = convRepo.FindDefaultByUser(c.Request.Context(), userID)
		}
		// 空字符串 conversation_id 走这里：直接创建非默认新会话
		if conv == nil {
			conv = &model.Conversation{ID: uuid.New().String(), UserID: userID}
			if req.ConversationID == nil {
				conv.IsDefault = true
			}
			if req.AgentID != nil && *req.AgentID != "" {
				conv.AgentID = req.AgentID
			}
			// 空 tab 场景：会话尚未存在，客户端已切的模式经 init_mode 传入，创建时落库
			// （仅 plan 有意义，非法值忽略，兼容旧客户端）
			if req.InitMode == model.ConvModePlan {
				conv.Mode = model.ConvModePlan
			}
			if err := convRepo.Create(c.Request.Context(), conv); err != nil {
				log.Printf("[chat] CreateConversation error: %v", err)
				view.JSONError(c, 500, "create conversation failed")
				return
			}
		}

		// 加载智能体 system prompt（新会话和续聊都支持）
		var agentSystemPrompt string
		var agentID string
		var originMsg = req.Message
		if conv.AgentID != nil && *conv.AgentID != "" && agentRepo != nil {
			a, err := agentRepo.FindByID(c.Request.Context(), *conv.AgentID, userID)
			if err != nil {
				log.Printf("[chat] FindByID agent error: %v", err)
			} else if a != nil {
				agentSystemPrompt = a.SystemPrompt
				agentID = a.ID
			}
		} else if cfg.GlobalDefaultRole.SystemPrompt != "" {
			agentSystemPrompt = cfg.GlobalDefaultRole.SystemPrompt
		}

		// 会话模式：plan 模式下追加只读讨论约束（写操作由服务端强制拦截，prompt 只是引导）
		convMode := conv.Mode
		if convMode == "" {
			convMode = model.ConvModeWork
		}
		// 模式提示（注入当前 user 消息 XMLContext，仅 LLM 可见）：
		//   plan 模式 → 每轮注入 plan 提示；work 模式且上一条 user 消息是 plan 提示 → 注入退出提示（一次）
		modeNotice := ""
		if convMode == model.ConvModePlan {
			modeNotice = planModeNotice
		} else if containsPlanNotice(c.Request.Context(), msgRepo, conv.ID) {
			modeNotice = planExitNotice
		}

		// —— 预处理管线 ——
		userSpecifiedAgent := req.AgentID != nil && *req.AgentID != ""
		pc := &pipeline.PreContext{
			RawMsg:             req.Message,
			UserID:             userID,
			UserSpecifiedAgent: userSpecifiedAgent,
		}
		pipelineCtx := pipeline.WithPreContext(c.Request.Context(), pc)

		pl, err := pipeline.NewPipeline(
			pipeline.NewShortInputCheckStep(),
			pipeline.NewNormalizeDatesStep(),
		)
		if err != nil {
			log.Printf("[chat] pipeline init error: %v", err)
		}
		if _, pipeErr := pl.Run(pipelineCtx); pipeErr != nil {
			log.Printf("[chat] pipeline run error: %v", pipeErr)
		}

		// 读取管线结果
		if pc.ForceDeepMode {
			req.DeepMode = true
		}
		normalizedMsg := pc.NormalizedMsg
		if normalizedMsg == "" {
			normalizedMsg = req.Message
		}

		// 加载历史消息：按轮次分组，每轮保留 user + 最终 assistant 回复
		var history []model.Message
		// 计算扩张窗口和 session 状态
		wr := computeWindowSize(c.Request.Context(), msgRepo, conv.ID, cfg)
		var rounds [][]model.Message
		if !wr.SkipHistory {
			n := wr.Size
			if n <= 0 {
				n = cfg.Chat.HistoryRounds
			}
			rounds, err = msgRepo.ListRecentRounds(c.Request.Context(), conv.ID, n)
			if err != nil {
				log.Printf("[chat] ListRecentRounds error: %v", err)
				view.JSONError(c, 500, "加载历史消息失败")
				return
			}
		}
		for _, round := range rounds {
			history = append(history, agent.FlattenRound(round)...)
		}
		// 新窗口（SkipHistory）不重放完整轮（含 tool 调用，token 贵），
		// 取最近几轮的 user + 非 tool call 的 assistant 最终回复注入资源区，
		// 让 LLM 知道用户之前说了什么、自己回了什么。
		var recentUserMsgs []model.Message
		if wr.SkipHistory && cfg.Chat.RecentUserMsgs > 0 {
			recentRounds, rerr := msgRepo.ListRecentRounds(c.Request.Context(), conv.ID, cfg.Chat.RecentUserMsgs)
			if rerr != nil {
				log.Printf("[chat] ListRecentRounds (recent) error: %v", rerr)
				view.JSONError(c, 500, "加载历史消息失败")
				return
			}
			recentUserMsgs = agent.ReduceRecentRounds(recentRounds)
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
		messages = agent.BuildMessagesForFlash(history, cfg.LLM.MaxToolResultChars, cfg.LLM.UseNativeFC)

		// 注册终止信号：显式停止接口（/api/chat/stop）通过关闭 channel 取消 ReAct 循环。
		// 使用独立于 Request.Context 的运行 ctx——网络波动（SSE 断开）不会误终止正常执行。
		stopCh := chatStops.Register(conv.ID)
		defer chatStops.Unregister(conv.ID, stopCh)

		runCtx, cancelRun := context.WithCancel(context.Background())
		defer cancelRun()
		go func() {
			select {
			case <-stopCh:
				cancelRun()
			case <-runCtx.Done():
			}
		}()

		events, err := orch.Run(runCtx, &agent.RunRequest{
			UserID:             userID,
			ConversationID:     conv.ID,
			Messages:           messages,
			DeepMode:           req.DeepMode,
			UserMessageContent: normalizedMsg,
			RawUserMessage:     originMsg,
			AgentSystemPrompt:  agentSystemPrompt,
			AgentID:            agentID,
			Memory:             req.Memory,
			SkipRetrieval:      pc.SkipRetrieval,
			Window:             wr,
			RecentUserMessages: recentUserMsgs,
			ClientTools:        req.ClientTools,
			Mode:               convMode,
			ModeNotice:         modeNotice,
			VerifyEnabled:      req.VerifyEnabled,
		})
		if err != nil {
			log.Printf("[chat] orch run error: %v", err)
			view.WriteSSE(w, flusher, "error", agent.UserErrorMessage(err))
			return
		}

		// 客户端断开（SSE 断开/关客户端）时只停止转发，不中断 ReAct 运行——
		// 独立 runCtx 不受 Request.Context 影响，网络波动不误终止正常执行。
		clientGone := false
		for evt := range events {
			select {
			case <-c.Request.Context().Done():
				clientGone = true
			default:
			}
			if clientGone {
				// 继续消费 events 直到 ReAct 完成（消息完整入库），但不再写 SSE。
				// done 事件的标题合并用背景 ctx（Request.Context 已取消，DB 操作会失败）
				if evt.Type == "done" {
					mergeTitle(context.Background(), orch, convRepo, conv, req.Message, evt.Content)
				}
				continue
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
			case "reminders_changed":
				view.WriteSSE(w, flusher, "reminders_changed", evt.Content)
			case "tool_call":
				view.WriteSSE(w, flusher, "tool_call", evt.Content)
			case "subagent":
				// 子任务活动面板事件（start/end/status/tool_call/tool_result，JSON 含 conv_id）
				view.WriteSSE(w, flusher, "subagent", evt.Content)
			case "client_tool_call":
				view.WriteSSE(w, flusher, "client_tool_call", evt.Content)
			case "tool_result":
				view.WriteSSE(w, flusher, "tool_result", evt.Content)
			case "verify":
				view.WriteSSE(w, flusher, "verify", evt.Content)
			case "done":
				doneJSON := mergeTitle(c.Request.Context(), orch, convRepo, conv, req.Message, evt.Content)
				view.WriteSSE(w, flusher, "done", doneJSON)
			case "error":
				view.WriteSSE(w, flusher, "error", evt.Error)
			}
		}
	}
}

// maxClientToolResultBytes — 单次回传 result 字段大小上限，超出以错误结果唤醒等待方并拒绝
const maxClientToolResultBytes = 256 * 1024

// maxToolResultBodyBytes — /api/chat/tool-result 请求体硬上限，防止超大 body 占用内存
const maxToolResultBodyBytes = 1 << 20

// chatToolResult — 客户端工具执行结果回传（POST /api/chat/tool-result）。
// 按 call_id 唤醒 orchestrator 中阻塞等待的客户端工具调用。
func chatToolResult(bridge *agent.ClientToolBridge) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxToolResultBodyBytes)
		var req struct {
			CallID string          `json:"call_id" binding:"required"`
			Result json.RawMessage `json:"result"` // 任意 JSON 值（对象/数组/字符串均可）
			Error  string          `json:"error"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			view.JSONError(c, 400, "invalid request")
			return
		}
		userID := c.GetString("user_id")
		if len(req.Result) > maxClientToolResultBytes {
			// 结果过大：以错误结果唤醒等待方（快速失败，避免 orchestrator 白等到超时），再拒绝本次回传
			if err := bridge.OnResult(req.CallID, userID, agent.ClientToolResult{
				Error: fmt.Sprintf("客户端工具结果过大（%d 字节，上限 %d 字节），已被服务端拒绝", len(req.Result), maxClientToolResultBytes),
			}); err != nil {
				log.Printf("[chat] oversize tool-result, wake pending failed: %v", err)
			}
			view.JSONError(c, 413, "result too large")
			return
		}
		if err := bridge.OnResult(req.CallID, userID, agent.ClientToolResult{
			Result: req.Result,
			Error:  req.Error,
		}); err != nil {
			// unknown call_id / user mismatch 统一 404，避免探测
			log.Printf("[chat] tool-result rejected: %v", err)
			view.JSONError(c, 404, "call not found")
			return
		}
		view.JSONOK(c, gin.H{"status": "ok"})
	}
}

func mergeTitle(ctx context.Context, orch *agent.Orchestrator, convRepo *service.ConversationRepo, conv *model.Conversation, userMsg, doneContent string) string {
	if conv.Title != "新对话" || userMsg == "" {
		return doneContent
	}
	ctx = llmcost.WithScenario(ctx, llmcost.Scenario{
		Category:    llmcost.CategoryMainChat,
		SubCategory: llmcost.SubTitle,
		UserID:      conv.UserID,
		ConvID:      conv.ID,
	})
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

// llmConfig GET /api/llm-config?agent_id=xxx
// 返回当前生效的 LLM 参数。传 agent_id 时返回 Agent 参数，否则返回配置默认值。
func llmConfig(cfg *config.Config, agentRepo *service.AgentRepo) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetString("user_id")

		resp := map[string]interface{}{
			"temperature": 1.0,
			"max_tokens":  4096,
			"thinking":    true,
			"deep_mode":   false,
		}
		if cfg.LLM.DefaultTemperature != nil {
			resp["temperature"] = *cfg.LLM.DefaultTemperature
		}
		if cfg.LLM.DefaultMaxTokens != nil {
			resp["max_tokens"] = *cfg.LLM.DefaultMaxTokens
		}

		agentID := c.Query("agent_id")
		if agentID != "" && agentRepo != nil {
			a, err := agentRepo.FindByID(c.Request.Context(), agentID, userID)
			if err != nil {
				log.Printf("[llm-config] find agent error: %v", err)
			} else if a != nil && a.ParamsJSON != "" {
				var params struct {
					Temperature *float64 `json:"temperature"`
					MaxTokens   *int     `json:"max_tokens"`
					Thinking    *bool    `json:"thinking"`
					DeepMode    *bool    `json:"deep_mode"`
				}
				if err := json.Unmarshal([]byte(a.ParamsJSON), &params); err != nil {
					log.Printf("[llm-config] parse agent params error: %v", err)
				} else {
					if params.Temperature != nil {
						resp["temperature"] = *params.Temperature
					}
					if params.MaxTokens != nil {
						resp["max_tokens"] = *params.MaxTokens
					}
					if params.Thinking != nil {
						resp["thinking"] = *params.Thinking
					}
					if params.DeepMode != nil {
						resp["deep_mode"] = *params.DeepMode
					}
				}
			}
		}

		view.JSONOK(c, resp)
	}
}

// agentParams 解析 Agent.ParamsJSON 的结构体
type agentParams struct {
	Temperature *float64 `json:"temperature"`
	MaxTokens   *int     `json:"max_tokens"`
	Thinking    *bool    `json:"thinking"`
	DeepMode    *bool    `json:"deep_mode"`
}

// parseAgentParams 解析 agent 的 ParamsJSON
func parseAgentParams(paramsJSON string) (*float64, *int, *bool, *bool, error) {
	if paramsJSON == "" {
		return nil, nil, nil, nil, fmt.Errorf("empty params")
	}
	var p agentParams
	if err := json.Unmarshal([]byte(paramsJSON), &p); err != nil {
		return nil, nil, nil, nil, err
	}
	return p.Temperature, p.MaxTokens, p.Thinking, p.DeepMode, nil
}

// mergeAgentAndUserParams 用户参数优先，agent 参数仅作为默认值
func mergeAgentAndUserParams(agentTemp *float64, agentMaxTokens *int, agentThinking *bool,
	userTemp *float64, userMaxTokens *int, userThinking *bool) (*float64, *int, *bool) {
	if userTemp != nil {
		agentTemp = userTemp
	}
	if userMaxTokens != nil {
		agentMaxTokens = userMaxTokens
	}
	if userThinking != nil {
		agentThinking = userThinking
	}
	return agentTemp, agentMaxTokens, agentThinking
}

// buildLLMConfigResponse 构建 llm-config 响应。
// agent 参数覆盖配置默认值（agent 优先级更高）。
func buildLLMConfigResponse(agent *model.Agent, defaultTemp *float64, defaultMaxTokens *int) map[string]interface{} {
	resp := map[string]interface{}{
		"temperature": 1.0,
		"max_tokens":  4096,
		"thinking":    true,
		"deep_mode":   false,
	}
	if defaultTemp != nil {
		resp["temperature"] = *defaultTemp
	}
	if defaultMaxTokens != nil {
		resp["max_tokens"] = *defaultMaxTokens
	}
	if agent != nil && agent.ParamsJSON != "" {
		if temp, tokens, thinking, deepMode, err := parseAgentParams(agent.ParamsJSON); err == nil {
			if temp != nil {
				resp["temperature"] = *temp
			}
			if tokens != nil {
				resp["max_tokens"] = *tokens
			}
			if thinking != nil {
				resp["thinking"] = *thinking
			}
			if deepMode != nil {
				resp["deep_mode"] = *deepMode
			}
		}
	}
	return resp
}

// computeWindowSize 计算扩张窗口和 session 状态。
//
// 规则：
//  1. 查同会话最近一条 user 消息的 created_at 和 Extra
//  2. gap > SessionGapMinutes → 新 session，重置窗口
//  3. gap ≤ SessionGapMinutes → 同一 session，窗口扩张，继承 SessionStartMsgID
//  4. lastWindow ≥ MaxWindowRounds → 折半
//  5. 没有上一条消息 → 新 session
//
// containsPlanNotice 判断会话最近一条 user 消息的 XMLContext 是否含 plan 提示标记。
// 用于"退出计划模式后首条消息"注入退出提示（仅一次）：上一条是 plan 提示 → 注入退出；
// 上一条已是退出提示（或更早）→ 不再注入。
func containsPlanNotice(ctx context.Context, msgRepo *service.MessageRepo, convID string) bool {
	msgs, err := msgRepo.ListRecentUserMessages(ctx, convID, 1)
	if err != nil || len(msgs) == 0 {
		return false
	}
	if msgs[0].XMLContext == nil {
		return false
	}
	return strings.Contains(*msgs[0].XMLContext, planNoticeMarker)
}

func computeWindowSize(ctx context.Context, msgRepo *service.MessageRepo, convID string, cfg *config.Config) agent.WindowResult {
	defaultSize := cfg.Chat.HistoryRounds
	gapMinutes := cfg.Chat.SessionGapMinutes

	// 查最近一条 user 消息
	lastUserMsgs, err := msgRepo.ListRecentUserMessages(ctx, convID, 1)
	if err != nil || len(lastUserMsgs) == 0 {
		return agent.WindowResult{Size: 3, IsNewSession: true}
	}
	lastUserMsg := &lastUserMsgs[0]

	lastWindow := defaultSize
	if ws := agent.GetWindowSize(lastUserMsg); ws > 0 {
		lastWindow = ws
	}

	gap := time.Since(lastUserMsg.CreatedAt)
	if gap > time.Duration(gapMinutes)*time.Minute {
		// 新窗口：不重放旧轮（tool 调用 token 贵），改为最近用户消息注入 system prompt；
		// Size 记 1 供下一条窗口扩张。
		return agent.WindowResult{Size: 1, IsNewSession: true, SkipHistory: true}
	}

	maxRounds := cfg.Chat.MaxWindowRounds
	newWindow := lastWindow + 1
	if newWindow >= maxRounds {
		newWindow = maxRounds / 2
	}
	prevXML := ""
	if lastUserMsg.XMLContext != nil {
		prevXML = *lastUserMsg.XMLContext
	}
	return agent.WindowResult{
		Size:              newWindow,
		IsNewSession:      false,
		SessionStartMsgID: agent.GetSessionStart(lastUserMsg),
		PrevUserXML:       prevXML,
	}
}
