package controller

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"

	"xhan-helper/internal/agent"
	"xhan-helper/internal/agent/replay"
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
	AnchorMessageID    string   `json:"anchor_message_id" binding:"required"`
	HistoryCount       int      `json:"history_count"`
	SkipRetrieval      bool     `json:"skip_retrieval"`
	MaxToolResultChars int      `json:"max_tool_result_chars"`
	DeepMode           bool     `json:"deep_mode"`
	Temperature        *float64 `json:"temperature"`
}

type replayCompareReq struct {
	AnchorMessageID string        `json:"anchor_message_id" binding:"required"`
	HistoryCount    int           `json:"history_count"`
	ConfigA         replay.Config `json:"config_a"`
	ConfigB         replay.Config `json:"config_b"`
}

// PreviewReplay 预览重放时发给 LLM 的完整请求
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
	if req.MaxToolResultChars <= 0 {
		req.MaxToolResultChars = 2000
	}

	anchor, history := h.loadAnchorAndHistory(c, req.AnchorMessageID, req.HistoryCount)
	if anchor == nil {
		return // loadAnchorAndHistory 已写错误响应
	}

	// 构建消息列表（旧消息 + 锚点用户消息）
	msgs := agent.BuildMessagesForFlash(history, req.MaxToolResultChars)
	msgs = append(msgs, llm.Message{Role: "user", Content: anchor.Content})

	// 注意: system prompt 由 orchestrator Run 时动态构建（含检索），预览不重复计算 embedding
	// 这里只返回消息历史，system prompt 在 compare 阶段由 orchestrator 填充
	view.JSONOK(c, gin.H{
		"anchor_message": cleanForDebug(anchor),
		"messages":       msgs,
		"history_count":  len(history),
	})
}

// CompareReplay 用两组配置分别重放，SSE 流式返回对比结果
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

	w := c.Writer
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		view.JSONError(c, 500, "streaming not supported")
		return
	}

	// 发送开始事件
	startJSON, _ := json.Marshal(gin.H{
		"anchor_message": cleanForDebug(anchor),
		"history_count":  len(history),
	})
	view.WriteSSE(w, flusher, "start", string(startJSON))

	// 串行执行 A 和 B（避免 LLM QPS 限制）
	resultA, errA := h.runner.Run(c.Request.Context(), anchor, history, &req.ConfigA)
	if errA != nil {
		log.Printf("[replay] config A run error: %v", errA)
		view.WriteSSE(w, flusher, "error", `{"side":"a","error":"`+view.EscapeJSON(errA.Error())+`"}`)
		return
	}
	resultAJSON, _ := json.Marshal(resultA)
	view.WriteSSE(w, flusher, "replay_done", `{"side":"a","result":`+string(resultAJSON)+`}`)

	resultB, errB := h.runner.Run(c.Request.Context(), anchor, history, &req.ConfigB)
	if errB != nil {
		log.Printf("[replay] config B run error: %v", errB)
		view.WriteSSE(w, flusher, "error", `{"side":"b","error":"`+view.EscapeJSON(errB.Error())+`"}`)
		return
	}
	resultBJSON, _ := json.Marshal(resultB)
	view.WriteSSE(w, flusher, "replay_done", `{"side":"b","result":`+string(resultBJSON)+`}`)

	// 汇总对比
	doneJSON, _ := json.Marshal(gin.H{
		"result_a": resultA,
		"result_b": resultB,
	})
	view.WriteSSE(w, flusher, "done", string(doneJSON))
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

	// 使用游标分页获取锚点之前的消息
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
