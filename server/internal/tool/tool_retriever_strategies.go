package tool

import (
	"context"
	"log"

	"xhan-helper/internal/llm"
)

// ==================== 向量检索策略（示例） ====================

// VectorRetrievalStrategy 向量检索策略
type VectorRetrievalStrategy struct {
	embedder     *llm.Embedder
	vectorStore  VectorStore
	toolRepo     ToolRepo
}

// VectorStore 向量存储接口（适配层）
type VectorStore interface {
	Search(vec []float64, topK int, filter func(meta map[string]string) bool) []VectorMatch
}

// VectorMatch 向量匹配结果
type VectorMatch struct {
	ID       string
	Score    float64
	Metadata map[string]string
}

// NewVectorRetrievalStrategy 创建向量检索策略
func NewVectorRetrievalStrategy(
	embedder *llm.Embedder,
	vectorStore VectorStore,
	toolRepo ToolRepo,
) *VectorRetrievalStrategy {
	return &VectorRetrievalStrategy{
		embedder:    embedder,
		vectorStore: vectorStore,
		toolRepo:    toolRepo,
	}
}

func (s *VectorRetrievalStrategy) Name() string {
	return "vector"
}

func (s *VectorRetrievalStrategy) Search(ctx context.Context, query string, limit int) ([]ToolInfo, error) {
	log.Printf("[ToolRetriever] vector search: query=%q", query)

	// 1. 生成 query embedding
	vec, err := s.embedder.Embed(ctx, query)
	if err != nil {
		return nil, err
	}

	// 2. 向量检索
	matches := s.vectorStore.Search(vec, limit, func(meta map[string]string) bool {
		return meta["type"] == "tool"
	})

	log.Printf("[ToolRetriever] vector search found %d matches", len(matches))

	// 3. 从 repo 加载工具详情
	results := make([]ToolInfo, 0, len(matches))
	for _, match := range matches {
		toolID := match.ID
		// TODO: 从 repo 加载工具
		_ = toolID
	}

	return results, nil
}

// ==================== 混合策略（示例） ====================

// HybridRetrievalStrategy 混合检索策略（关键词 + 向量）
type HybridRetrievalStrategy struct {
	keywordStrategy *SimpleKeywordStrategy
	vectorStrategy  *VectorRetrievalStrategy
	vectorWeight    float64 // 向量结果权重
	keywordWeight   float64 // 关键词结果权重
}

// NewHybridRetrievalStrategy 创建混合检索策略
func NewHybridRetrievalStrategy(
	keywordStrategy *SimpleKeywordStrategy,
	vectorStrategy *VectorRetrievalStrategy,
	vectorWeight float64,
	keywordWeight float64,
) *HybridRetrievalStrategy {
	if vectorWeight <= 0 {
		vectorWeight = 0.7
	}
	if keywordWeight <= 0 {
		keywordWeight = 0.3
	}
	return &HybridRetrievalStrategy{
		keywordStrategy: keywordStrategy,
		vectorStrategy:  vectorStrategy,
		vectorWeight:    vectorWeight,
		keywordWeight:   keywordWeight,
	}
}

func (s *HybridRetrievalStrategy) Name() string {
	return "hybrid"
}

func (s *HybridRetrievalStrategy) Search(ctx context.Context, query string, limit int) ([]ToolInfo, error) {
	log.Printf("[ToolRetriever] hybrid search: query=%q (vector=%.2f, keyword=%.2f)",
		query, s.vectorWeight, s.keywordWeight)

	// 并行执行两种检索
	type result struct {
		tools []ToolInfo
		err   error
	}
	keywordCh := make(chan result, 1)
	vectorCh := make(chan result, 1)

	go func() {
		tools, err := s.keywordStrategy.Search(ctx, query, limit)
		keywordCh <- result{tools, err}
	}()

	go func() {
		tools, err := s.vectorStrategy.Search(ctx, query, limit)
		vectorCh <- result{tools, err}
	}()

	// 收集结果
	keywordRes := <-keywordCh
	vectorRes := <-vectorCh

	if keywordRes.err != nil {
		log.Printf("[ToolRetriever] keyword search error: %v", keywordRes.err)
	}
	if vectorRes.err != nil {
		log.Printf("[ToolRetriever] vector search error: %v", vectorRes.err)
	}

	// 合并结果（去重 + 加权排序）
	merged := s.mergeResults(keywordRes.tools, vectorRes.tools, limit)
	return merged, nil
}

func (s *HybridRetrievalStrategy) mergeResults(keywordTools, vectorTools []ToolInfo, limit int) []ToolInfo {
	// 简单的去重合并
	seen := make(map[string]bool)
	merged := make([]ToolInfo, 0, limit)

	// 先加向量结果（权重更高）
	for _, t := range vectorTools {
		if len(merged) >= limit {
			break
		}
		if !seen[t.ID] {
			seen[t.ID] = true
			t.Score *= s.vectorWeight
			merged = append(merged, t)
		}
	}

	// 再加关键词结果
	for _, t := range keywordTools {
		if len(merged) >= limit {
			break
		}
		if !seen[t.ID] {
			seen[t.ID] = true
			t.Score *= s.keywordWeight
			merged = append(merged, t)
		}
	}

	return merged
}
