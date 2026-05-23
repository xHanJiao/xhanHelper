package agent

import (
	"context"

	"xhan-helper/internal/llm"
)

// ContextPacket — 上下文片段，来自任意数据源
type ContextPacket struct {
	ID                     string
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
