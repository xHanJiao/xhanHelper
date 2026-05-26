package retrieval

import (
	"context"

	"xhan-helper/internal/llm"
)

// ContextPacket — 上下文片段，来自任意数据源
type ContextPacket struct {
	ID                     string
	Type                   string // memory | note | history
	Text                   string
	TokenCount             int
	CorrelationCoefficient float64
	Score                  float64 // 向量检索原始分数
	KeywordScore           float64 // bigram 关键词匹配分数
}

// ContextSource — 上下文数据源接口
type ContextSource interface {
	Name() string
	Fetch(ctx context.Context, queryVec []float64, query, userID, convID string,
		threshold float64, limit int) ([]ContextPacket, error)
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
