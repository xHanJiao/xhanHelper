package controller

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"

	"xhan-helper/internal/agent"
	"xhan-helper/internal/agent/ctxflow"
	"xhan-helper/internal/agent/replay"
	"xhan-helper/internal/agent/retrieval"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
	"xhan-helper/internal/view"
)

// ReplayHandler — 对话重放对比（管理端）
type ReplayHandler struct {
	msgRepo  *service.MessageRepo
	runner   *replay.Runner
	embedder *llm.Embedder
}

// NewReplayHandler 创建 ReplayHandler
func NewReplayHandler(msgRepo *service.MessageRepo, runner *replay.Runner, embedder *llm.Embedder) *ReplayHandler {
	return &ReplayHandler{msgRepo: msgRepo, runner: runner, embedder: embedder}
}

// —— 请求/响应类型 ——

type replayPreviewReq struct {
	AnchorMessageID    string                 `json:"anchor_message_id" binding:"required"`
	HistoryCount       int                    `json:"history_count"`
	SkipRetrieval      bool                   `json:"skip_retrieval"`
	MaxToolResultChars int                    `json:"max_tool_result_chars"`
	DeepMode           bool                   `json:"deep_mode"`
	Temperature        *float64               `json:"temperature"`
	Memory             retrieval.SourceConfig `json:"memory"`
	Note               retrieval.SourceConfig `json:"note"`
	History            retrieval.SourceConfig `json:"history"`
	Theme              retrieval.SourceConfig `json:"theme"`
	Summary            retrieval.SourceConfig `json:"summary"`
}

type replayCompareReq struct {
	AnchorMessageID    string        `json:"anchor_message_id" binding:"required"`
	HistoryCount       int           `json:"history_count"`
	ConfigB            replay.Config `json:"config_b"`
	SystemPromptSource string        `json:"system_prompt_source"` // "historical" | "live"
}

// —— A 侧（原始响应，来自 DB）——

type sideAMsg struct {
	Role             string          `json:"role"`
	Content          string          `json:"content"`
	ReasoningContent string          `json:"reasoning_content,omitempty"`
	ToolCalls        json.RawMessage `json:"tool_calls,omitempty"`
	ToolCallID       string          `json:"tool_call_id,omitempty"`
}

type sideAData struct {
	Messages     []sideAMsg `json:"messages"`
	SystemPrompt string     `json:"system_prompt"`
	ModelName    string     `json:"model_name"`
	Temperature  *float64   `json:"temperature,omitempty"`
}

// —— B 侧（新跑的结果）——

type sideBData struct {
	Result         *replay.Result         `json:"result"`
	SystemPrompt   string                 `json:"system_prompt"`
	RetrievalTrace *ctxflow.RetrieveTrace `json:"retrieval_trace,omitempty"`
}

// PreviewReplay 预览重放时发给 LLM 的完整请求（含 system prompt + 消息列表）
// POST /api/admin/replay/preview
func (h *ReplayHandler) PreviewReplay(c *gin.Context) {
	var req replayPreviewReq
	if err := c.ShouldBindJSON(&req); err != nil {
		view.JSONError(c, 400, "请求格式错误: "+err.Error())
		return
	}
	if req.HistoryCount <= 0 {
		req.HistoryCount = 10
	}
	if req.MaxToolResultChars < 0 {
		req.MaxToolResultChars = 2000
	}

	anchor, history := h.loadAnchorAndHistory(c, req.AnchorMessageID, req.HistoryCount)
	if anchor == nil {
		return
	}

	preview, err := h.runner.Preview(c.Request.Context(), anchor, history, &replay.Config{
		SkipRetrieval:      req.SkipRetrieval,
		MaxToolResultChars: req.MaxToolResultChars,
		Memory:             req.Memory,
		Note:               req.Note,
		History:            req.History,
		Theme:              req.Theme,
		Summary:            req.Summary,
	})
	if err != nil {
		log.Printf("[replay] preview error: %v", err)
	}

	systemPrompt := ""
	messages := agent.BuildMessagesForFlash(history, req.MaxToolResultChars, false, false)
	messages = append(messages, llm.Message{Role: "user", Content: anchor.Content})
	var trace *ctxflow.RetrieveTrace

	if preview != nil {
		systemPrompt = preview.SystemPrompt
		messages = preview.Messages
		trace = preview.Trace
	}

	// 找锚点之后第一条 assistant 的 request_json，提取历史 system prompt 供前端选择
	historicalSP, _, _ := h.extractHistoricalPrompt(c, anchor)

	view.JSONOK(c, gin.H{
		"anchor_message":    cleanForDebug(anchor),
		"system_prompt":     systemPrompt,
		"messages":          messages,
		"history_count":     len(history),
		"trace":             trace,
		"historical_prompt": historicalSP,
	})
}

// CompareReplay 同步对比：A 侧展示 DB 中原有响应，B 侧用新配置跑一次 ReAct
// POST /api/admin/replay/compare
func (h *ReplayHandler) CompareReplay(c *gin.Context) {
	var req replayCompareReq
	if err := c.ShouldBindJSON(&req); err != nil {
		view.JSONError(c, 400, "请求格式错误: "+err.Error())
		return
	}
	if req.HistoryCount <= 0 {
		req.HistoryCount = 10
	}

	anchor, history := h.loadAnchorAndHistory(c, req.AnchorMessageID, req.HistoryCount)
	if anchor == nil {
		return
	}

	// —— A 侧：从 DB 捞原始响应链 ——
	sideA := h.buildSideA(c, anchor)

	// —— B 侧：确定 system prompt 来源 ——
	cfgB := req.ConfigB
	if cfgB.MaxToolResultChars < 0 {
		cfgB.MaxToolResultChars = 2000
	}

	// 如果用户没有手动覆盖 system prompt，根据来源选择
	if cfgB.OverrideSystemPrompt == nil {
		if req.SystemPromptSource == "historical" && sideA.SystemPrompt != "" {
			sp := sideA.SystemPrompt
			cfgB.OverrideSystemPrompt = &sp
			cfgB.SkipRetrieval = true // 历史 prompt 已含检索结果
		}
		// "live" 或 historical 不可用时：cfgB.OverrideSystemPrompt 保持 nil，自动构建
	}

	resultB, errB := h.runner.Run(c.Request.Context(), anchor, history, &cfgB)
	if errB != nil {
		log.Printf("[replay] config B run error: %v", errB)
		view.JSONError(c, http.StatusInternalServerError, "B 侧运行失败: "+errB.Error())
		return
	}

	sideB := sideBData{
		Result:         resultB,
		SystemPrompt:   resultB.SystemPrompt,
		RetrievalTrace: resultB.RetrievalTrace,
	}

	msgs := agent.BuildMessagesForFlash(history, cfgB.MaxToolResultChars, false, false)

	view.JSONOK(c, gin.H{
		"anchor_message":   cleanForDebug(anchor),
		"history_messages": msgs,
		"side_a":           sideA,
		"side_b":           sideB,
	})
}

// buildSideA 从 DB 加载锚点之后的原始 assistant/tool 消息链，
// 并从第一条 assistant 的 request_json 提取 system prompt 和模型参数。
func (h *ReplayHandler) buildSideA(c *gin.Context, anchor *model.Message) sideAData {
	result := sideAData{}

	// 加载锚点之后的消息
	cursorResult, err := h.msgRepo.ListByConversationCursor(
		c.Request.Context(), anchor.ConversationID, anchor.ID, "after", 50,
	)
	if err != nil {
		log.Printf("[replay] load side A messages error: %v", err)
		return result
	}

	var msgs []sideAMsg
	if cursorResult != nil {
		for _, m := range cursorResult.Messages {
			if m.Role == "user" {
				break // 到达下一轮用户消息，停止
			}
			sm := sideAMsg{
				Role:    m.Role,
				Content: m.Content,
			}
			if m.Role == "assistant" {
				if m.ReasoningContent != nil {
					sm.ReasoningContent = *m.ReasoningContent
				}
				if m.ToolCallsJSON != nil && *m.ToolCallsJSON != "" {
					sm.ToolCalls = json.RawMessage(*m.ToolCallsJSON)
				}
			}
			if m.Role == "tool" && m.ToolCallID != nil {
				sm.ToolCallID = *m.ToolCallID
			}
			msgs = append(msgs, sm)
		}
	}
	result.Messages = msgs

	// 从第一条 assistant 的 request_json 提取 system prompt + 模型参数
	historicalSP, modelName, temp := h.extractHistoricalPrompt(c, anchor)
	result.SystemPrompt = historicalSP
	result.ModelName = modelName
	result.Temperature = temp

	return result
}

// extractHistoricalPrompt 从锚点之后第一条 assistant 的 request_json 中
// 提取 system prompt、模型名和 temperature。
func (h *ReplayHandler) extractHistoricalPrompt(c *gin.Context, anchor *model.Message) (systemPrompt, modelName string, temperature *float64) {
	cursorResult, err := h.msgRepo.ListByConversationCursor(
		c.Request.Context(), anchor.ConversationID, anchor.ID, "after", 20,
	)
	if err != nil {
		return "", "", nil
	}
	if cursorResult == nil {
		return "", "", nil
	}

	for _, m := range cursorResult.Messages {
		if m.Role == "assistant" && m.RequestJSON != nil && *m.RequestJSON != "" {
			var apiReq struct {
				Model    string `json:"model"`
				Messages []struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				} `json:"messages"`
				Temperature *float64 `json:"temperature"`
			}
			if err := json.Unmarshal([]byte(*m.RequestJSON), &apiReq); err != nil {
				log.Printf("[replay] parse request_json error: %v", err)
				return "", "", nil
			}
			for _, msg := range apiReq.Messages {
				if msg.Role == "system" {
					systemPrompt = msg.Content
					break
				}
			}
			return systemPrompt, apiReq.Model, apiReq.Temperature
		}
	}
	return "", "", nil
}

// loadAnchorAndHistory 加载锚点消息及其前置历史消息
func (h *ReplayHandler) loadAnchorAndHistory(c *gin.Context, anchorID string, historyCount int) (*model.Message, []model.Message) {
	anchor, err := h.msgRepo.GetByID(c.Request.Context(), anchorID)
	if err != nil || anchor == nil {
		log.Printf("[replay] get anchor msg %s error: %v", anchorID, err)
		view.JSONError(c, 404, "消息不存在")
		return nil, nil
	}
	if anchor.Role != "user" {
		view.JSONError(c, 400, "仅支持以用户消息为锚点")
		return nil, nil
	}

	cursorResult, err := h.msgRepo.ListByConversationCursor(
		c.Request.Context(), anchor.ConversationID, anchorID, "before", historyCount,
	)
	if err != nil {
		log.Printf("[replay] load history error: %v", err)
		view.JSONError(c, 500, "加载历史消息失败")
		return nil, nil
	}

	var history []model.Message
	if cursorResult != nil {
		history = cursorResult.Messages
	}

	return anchor, history
}
