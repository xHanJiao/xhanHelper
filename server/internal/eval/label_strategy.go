package eval

import (
	"context"
)

// QueryRecord is a minimal view of a raw_queries row for label processing.
type QueryRecord struct {
	ID      string
	Content string
}

// CorpusRecord is a minimal view of an eval_corpus row for label processing.
type CorpusRecord struct {
	ID      string
	Content string
}

// LabelResult carries a single corpus relevance score (0~4).
type LabelResult struct {
	CorpusID  string `json:"corpus_id"`
	Relevance int    `json:"relevance"`
}

// LabelStrategy defines how to pre-label queries against corpus candidates.
type LabelStrategy interface {
	Name() string

	// PreLabel scores each candidate corpus against the query (0~4).
	PreLabel(ctx context.Context, llm ChatLLM, query string, candidates []CorpusRecord) ([]LabelResult, error)
}

// DefaultLabelStrategy uses DeepSeek Flash via prompt engineering.
type DefaultLabelStrategy struct{}

func (s *DefaultLabelStrategy) Name() string { return "default" }
