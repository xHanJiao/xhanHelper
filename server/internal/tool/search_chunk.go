package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"xhan-helper/internal/app"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
)

// searchChunks 向量搜 chunk，去重返回 parentID 列表
func searchChunks(ctx context.Context, vs app.VectorStore, embedder *llm.Embedder, userID, query, parentType string, topK int) []string {
	vec, err := embedder.Embed(ctx, query)
	if err != nil {
		log.Printf("[search_chunk] embed error: %v", err)
		return nil
	}

	hits := vs.Search(vec, topK*3, func(id string, _ []float64, metadata map[string]string) bool {
		return metadata["type"] == model.VectorTypeChunk &&
			metadata["parent_type"] == parentType &&
			metadata["user_id"] == userID
	})

	return dedupParents(hits, topK)
}

// dedupParents 从向量命中中提取 parentID 并去重，返回 topK 个
func dedupParents(hits []app.VectorMatch, topK int) []string {
	seen := make(map[string]bool)
	ordered := make([]string, 0)
	for _, h := range hits {
		_, pid := model.ParseChunkVectorKey(h.ID)
		if !seen[pid] {
			seen[pid] = true
			ordered = append(ordered, pid)
			if len(ordered) >= topK {
				break
			}
		}
	}
	return ordered
}

// formatMatchedChunks 将 chunk 匹配片段附加到结果后
func formatMatchedChunks(chunks []model.EmbeddingChunk) string {
	if len(chunks) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("  匹配片段: ")
	for i, c := range chunks {
		if i > 0 {
			sb.WriteString("; ")
		}
		sb.WriteString(c.Content[:min(120, len(c.Content))])
	}
	sb.WriteString("\n")
	return sb.String()
}

// marshalResult 序列化搜索结果
func marshalResult(results string, count int) *Result {
	data, err := json.Marshal(map[string]interface{}{
		"results": results,
		"count":   count,
	})
	if err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("marshal error: %v", err)}
	}
	return &Result{Success: true, Data: data}
}

// emptyResult 返回空搜索结果
func emptyResult(msg string) *Result {
	return marshalResult(msg, 0)
}

// groupChunksByParent 将 chunk 列表按 parentID 分组
func groupChunksByParent(chunks []model.EmbeddingChunk) map[string][]model.EmbeddingChunk {
	m := make(map[string][]model.EmbeddingChunk)
	for _, c := range chunks {
		m[c.ParentID] = append(m[c.ParentID], c)
	}
	return m
}
