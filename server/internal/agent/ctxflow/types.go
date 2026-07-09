// Package ctxflow — 上下文检索器
//
// 负责并发运行所有 ContextSource，返回按 type 分组的结果 map，
// 由 ContextFormatter 负责格式化。与 promptbuilder 的关系：
//   - promptbuilder：只做静态 system prompt 拼装
//   - ctxflow：上下文检索 + 格式化
package ctxflow

import "xhan-helper/internal/agent/retrieval"

// ==================== RetrieveResult ====================

// RetrieveResult — 上下文检索的完整结果。
type RetrieveResult struct {
	Packets map[string][]retrieval.ContextPacket // type → packets，key 为 "memory"/"note"/"history"/"theme"/"summary"
	Trace   *RetrieveTrace                       // 检索追溯
}

// RetrieveTrace — 检索构建的完整追溯。
type RetrieveTrace struct {
	Providers []ProviderTrace `json:"providers"`
}

// ProviderTrace — 单个检索源的构建记录。
type ProviderTrace struct {
	Name  string      `json:"name"`
	Type  string      `json:"type"` // "search"
	Input *TraceInput `json:"input,omitempty"`
	Items []TraceItem `json:"items,omitempty"`
	Error string      `json:"error,omitempty"`
}

// TraceInput — 检索输入参数。
type TraceInput struct {
	Query     string  `json:"query"`
	Threshold float64 `json:"threshold,omitempty"`
	Limit     int     `json:"limit,omitempty"`
}

// TraceItem — 单条候选资源的得分明细。
type TraceItem struct {
	ID             string  `json:"id"`
	Text           string  `json:"text,omitempty"`
	Score          float64 `json:"score"`
	FreshnessScore float64 `json:"freshness_score,omitempty"`
	DiscardReason  string  `json:"discard_reason,omitempty"`
}
