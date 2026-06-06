// Package promptbuilder — system prompt 构建器
//
// 采用三层模型：Layer 1 公共规则 / Layer 2 角色定义 / Layer 3 动态上下文
package promptbuilder

import (
	"context"

	"xhan-helper/internal/agent/retrieval"
)

// PromptResolver — 查找用户自定义 system prompt，由 agent 包实现。
type PromptResolver interface {
	ResolveActive(ctx context.Context, userID, promptType string) string
}

// ==================== Interfaces ====================

// StaticProvider — Layer 1/2 使用，不需要检索，直接返回文本块。
// 实现示例：核心原则、工具定义、当前时间。
type StaticProvider interface {
	Name() string
	SectionTitle() string // 章节标题，Builder 自动加 ## 前缀
	Render(ctx context.Context) string
}

// SearchResult — SearchProvider 的检索结果。
type SearchResult struct {
	Items      []retrieval.ContextPacket // 注入 prompt 的条目，按 FinalScore 参与跨 provider 排序
	TraceItems []TraceItem               // 全部候选项得分明细（含 near-miss），供调试面板展示
}

// SearchProvider — Layer 3 动态上下文 provider 接口。
//
// 每个 provider 独立完成「检索 → 打分 → 返回 SearchResult」：
//   - Items: 每条命中为一个 ContextPacket，FinalScore 为 ranker 综合分
//   - TraceItems: 全部候选项（已选中 + near-miss），写入 message.extra 供调试
//
// Items 由 Builder 统一按 FinalScore 降序排列后注入 prompt。
type SearchProvider interface {
	Name() string
	SectionHeader() string // 注入 prompt 时的 section 标题，空字符串=不分组输出
	Search(ctx context.Context, opts retrieval.SearchOptions) (*SearchResult, error)
}

// ==================== Prompt Trace ====================

// PromptTrace — system prompt 构建的完整追溯，写入 assistant message 的 Extra 字段。
type PromptTrace struct {
	Providers []ProviderTrace `json:"providers"`
}

// ProviderTrace — 单个 provider 的构建记录
type ProviderTrace struct {
	Name  string      `json:"name"`
	Type  string      `json:"type"`            // "static" | "search"
	Input *TraceInput `json:"input,omitempty"` // 检索参数（仅 search）
	Items []TraceItem `json:"items,omitempty"` // 检索结果（仅 search）
	Error string      `json:"error,omitempty"` // 错误信息（仅失败时）
}

// TraceInput — 检索输入参数
type TraceInput struct {
	Query     string  `json:"query"`
	Threshold float64 `json:"threshold,omitempty"`
	Limit     int     `json:"limit,omitempty"`
}

// TraceItem — 单条候选资源的得分明细，写入 message.extra 供调试面板展示。
//
// 已选中项：Score = FinalScore（ranker 综合分），DiscardReason 为空。
// 未选中项（near-miss）：VectorScore 有值，DiscardReason 说明丢弃原因（below_threshold / same_conversation / ...）。
type TraceItem struct {
	ID             string  `json:"id"`
	Text           string  `json:"text,omitempty"`            // 截断的文本摘要
	Score          float64 `json:"score"`                     // 综合得分（已选中）/ 0（未选中）
	VectorScore    float64 `json:"vector_score,omitempty"`    // 向量相似度
	KeywordScore   float64 `json:"keyword_score,omitempty"`   // 关键词匹配分
	FreshnessScore float64 `json:"freshness_score,omitempty"` // 新鲜度衰减
	DiscardReason  string  `json:"discard_reason,omitempty"`  // 丢弃原因，空=已选中
}

// addStaticTrace 将 StaticProvider 的渲染记录追加到 trace
func (t *PromptTrace) addStatic(name string) {
	t.Providers = append(t.Providers, ProviderTrace{
		Name: name,
		Type: "static",
	})
}

func (t *PromptTrace) addSearch(name string, input *TraceInput, items []TraceItem, err error) {
	pt := ProviderTrace{
		Name:  name,
		Type:  "search",
		Input: input,
		Items: items,
	}
	if err != nil {
		pt.Error = err.Error()
	}
	t.Providers = append(t.Providers, pt)
}

// ==================== Config ====================

// PromptConfig — 集中管理所有阈值、数量、开关。
type PromptConfig struct {
	Memory  retrieval.SourceConfig // 记忆检索
	Note    retrieval.SourceConfig // 笔记检索
	History retrieval.SourceConfig // 历史消息检索
	Theme   retrieval.SourceConfig // 主题检索
}

func DefaultPromptConfig() PromptConfig {
	return PromptConfig{
		Memory:  retrieval.SourceConfig{Threshold: 0.5, Limit: 8},
		Note:    retrieval.SourceConfig{Threshold: 0.5, Limit: 5},
		History: retrieval.SourceConfig{Threshold: 0.8, Limit: 1},
		Theme:   retrieval.SourceConfig{Threshold: 0.5, Limit: 10},
	}
}
