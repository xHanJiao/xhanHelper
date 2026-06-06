package retrieval

import (
	"context"

	"xhan-helper/internal/llm"
)

// ContextPacket — 上下文片段，来自任意数据源（也是 SearchProvider 的返回类型）。
type ContextPacket struct {
	ID           string  // 唯一标识
	Type         string  // memory | note | history | theme | mission
	Text         string  // 注入 prompt 的文本
	Score        float64 // 向量检索原始分数
	KeywordScore float64 // bigram 关键词匹配分数
	FinalScore   float64 // ranker 综合分数（向量*W + 关键词*W + 新鲜度*W），用于跨 provider 排序
	Meta         any     `json:"-"` // provider 自定义元数据（memory_ids, note_ids, trace_items）
}

// NearMissItem — 被阈值或规则过滤掉的候选项，用于 trace 调试
type NearMissItem struct {
	ID          string  `json:"id"`
	Text        string  `json:"text,omitempty"` // 截断文本
	VectorScore float64 `json:"vector_score"`
	Reason      string  `json:"reason"` // "below_threshold" | "wrong_prefix" | "same_conversation" | "db_not_found"
}

// SourceConfig 单个检索源的阈值和数量限制。
type SourceConfig struct {
	Threshold float64 `json:"threshold"`
	Limit     int     `json:"limit"`
}

// Resolve 用 cfg 填充当前值为 0 的字段。
func (s *SourceConfig) Resolve(cfg SourceConfig) {
	if s.Threshold <= 0 {
		s.Threshold = cfg.Threshold
	}
	if s.Limit <= 0 {
		s.Limit = cfg.Limit
	}
}

// SearchOptions — 检索参数，供 ContextSource.Fetch 和 SearchProvider.Search 共用。
type SearchOptions struct {
	Query          string // 用户原始文本（ranker 关键词打分用）
	UserID         string
	ConversationID string
	Embedding      []float64 // 用户消息的 embedding（向量检索用）

	Memory  SourceConfig // 记忆检索
	Note    SourceConfig // 笔记检索
	History SourceConfig // 历史对话检索
	Theme   SourceConfig // 主题检索

	// 排除已在当前消息列表中的消息 ID（避免系统提示词与历史消息重复）
	ExcludeMsgIDs map[string]bool
}

// ContextSource — 上下文数据源接口
type ContextSource interface {
	Name() string
	Fetch(ctx context.Context, opts SearchOptions) (selected []ContextPacket, nearMiss []NearMissItem, err error)
	Warmup(ctx context.Context, embedder *llm.Embedder)
}

// Entry — 向量条目筛选视图
type Entry struct {
	ID       string
	Vec      []float64
	Metadata map[string]string
}

// SearchResult — 向量检索结果
type SearchResult struct {
	ID       string
	Score    float64
	Metadata map[string]string
}

// VectorStore — 向量存储接口（agent.VectorStore 满足此接口）
type VectorStore interface {
	Search(query []float64, topK int, filter func(Entry) bool) []SearchResult
	Upsert(id string, vec []float64, metadata map[string]string)
	Size() int
}
