package agent

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"xhan-helper/internal/app"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// chunkEmbedder 嵌入接口，供 ChunkBuilder 使用（*llm.Embedder 满足）
type chunkEmbedder interface {
	Embed(ctx context.Context, text string) ([]float64, error)
	ModelName() string
}

// ChunkBuilder — 将 note/memory 文本拆分为句子级 chunk 并嵌入
type ChunkBuilder struct {
	embedder      chunkEmbedder
	chunkRepo     *service.EmbeddingChunkRepo
	embeddingRepo *service.EmbeddingRepo
	vectorStore   app.VectorStore
	// disabledParents 为不再建 chunk 的父类型（短文本由 full 向量覆盖）。
	// nil = 全部允许（保持既有测试/调用语义）。
	disabledParents map[string]bool
}

func NewChunkBuilder(infra *app.Infra, chunkRepo *service.EmbeddingChunkRepo, embeddingRepo *service.EmbeddingRepo, disabledParents []string) *ChunkBuilder {
	var disabled map[string]bool
	if len(disabledParents) > 0 {
		disabled = make(map[string]bool, len(disabledParents))
		for _, p := range disabledParents {
			disabled[p] = true
		}
	}
	return &ChunkBuilder{
		embedder:        infra.Embedder,
		chunkRepo:       chunkRepo,
		embeddingRepo:   embeddingRepo,
		vectorStore:     infra.VectorStore,
		disabledParents: disabled,
	}
}

var sentenceSplitter = regexp.MustCompile(`[。！？\n]+`)

// splitSentences 中文分句
func splitSentences(text string) []string {
	parts := sentenceSplitter.Split(text, -1)
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if len(p) > 1 {
			result = append(result, p)
		}
	}
	return result
}

var chunkMetaBaseKeys = map[string]bool{
	"type": true, "parent_type": true, "parent_id": true, "user_id": true,
}

func buildChunkMeta(parentType, parentID, userID string, extra map[string]string) map[string]string {
	md := map[string]string{
		"type":        model.VectorTypeChunk,
		"parent_type": parentType,
		"parent_id":   parentID,
		"user_id":     userID,
	}
	for k, v := range extra {
		if chunkMetaBaseKeys[k] {
			continue
		}
		md[k] = v
	}
	return md
}

// BuildChunks 将文本拆分为句子并嵌入，写入 chunk 表 + embedding 表 + 向量库
// 返回创建的 chunk 数量
func (b *ChunkBuilder) BuildChunks(ctx context.Context, userID, parentType, parentID, text string, meta map[string]string) (int, error) {
	if b.disabledParents[parentType] {
		return 0, nil
	}
	sentences := splitSentences(text)
	if len(sentences) == 0 {
		return 0, nil
	}

	chunks := make([]model.EmbeddingChunk, 0, len(sentences))
	embeddings := make([]model.Embedding, 0, len(sentences))

	for i, s := range sentences {
		chunkID := model.ChunkVectorKey(parentType, parentID, i)
		vec, err := b.embedder.Embed(ctx, s)
		if err != nil {
			log.Printf("[chunk] embed %s/%s[%d] error: %v", parentType, parentID[:8], i, err)
			return len(chunks), fmt.Errorf("embed chunk %d: %w", i, err)
		}

		chunks = append(chunks, model.EmbeddingChunk{
			ID:         uuid.New().String(),
			ParentType: parentType,
			ParentID:   parentID,
			ChunkIndex: i,
			UserID:     userID,
			Content:    s,
		})

		embStr := llm.EmbeddingToString(vec)
		embeddings = append(embeddings, model.Embedding{
			ID:         chunkID,
			EntityType: model.EntityTypeChunk, // 不使用旧 entity_type
			EntityID:   chunkID,
			UserID:     userID,
			Model:      b.embedder.ModelName(),
			Embedding:  &embStr,
		})

		b.vectorStore.Upsert(chunkID, vec, buildChunkMeta(parentType, parentID, userID, meta))
	}

	if err := b.chunkRepo.BatchCreate(ctx, chunks); err != nil {
		log.Printf("[chunk] batch create chunks for %s/%s error: %v", parentType, parentID[:8], err)
		return 0, err
	}

	if err := b.embeddingRepo.BatchUpsert(ctx, embeddings); err != nil {
		log.Printf("[chunk] batch upsert embeddings for %s/%s error: %v", parentType, parentID[:8], err)
		return 0, err
	}

	log.Printf("[chunk] built %d chunks for %s/%s", len(chunks), parentType, parentID[:8])
	return len(chunks), nil
}
