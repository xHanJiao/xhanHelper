package agent

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// ChunkBuilder — 将 note/memory 文本拆分为句子级 chunk 并嵌入
type ChunkBuilder struct {
	embedder      *llm.Embedder
	chunkRepo     *service.EmbeddingChunkRepo
	embeddingRepo *service.EmbeddingRepo
	vectorStore   VectorStorer
}

func NewChunkBuilder(embedder *llm.Embedder, chunkRepo *service.EmbeddingChunkRepo, embeddingRepo *service.EmbeddingRepo, vs VectorStorer) *ChunkBuilder {
	return &ChunkBuilder{
		embedder:      embedder,
		chunkRepo:     chunkRepo,
		embeddingRepo: embeddingRepo,
		vectorStore:   vs,
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

// BuildChunks 将文本拆分为句子并嵌入，写入 chunk 表 + embedding 表 + 向量库
// 返回创建的 chunk 数量
func (b *ChunkBuilder) BuildChunks(ctx context.Context, userID, parentType, parentID, text string) (int, error) {
	sentences := splitSentences(text)
	if len(sentences) == 0 {
		return 0, nil
	}

	chunks := make([]model.EmbeddingChunk, 0, len(sentences))
	embeddings := make([]model.Embedding, 0, len(sentences))

	for i, s := range sentences {
		chunkID := fmt.Sprintf("chunk:%s:%s/%d", parentType, parentID, i)
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
			EntityType: "chunk", // 不使用旧 entity_type
			EntityID:   chunkID,
			UserID:     userID,
			Model:      b.embedder.ModelName(),
			Embedding:  &embStr,
		})

		// 写入内存向量库
		b.vectorStore.Upsert(chunkID, vec, map[string]string{
			"type":        "chunk",
			"parent_type": parentType,
			"parent_id":   parentID,
			"user_id":     userID,
		})
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
