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

// DiffAndBuildChunks 对比新旧文本，仅对变化的句子重新嵌入。
// 保留文本完全相同的旧 chunk，只嵌入新增和修改的句子。
func (b *ChunkBuilder) DiffAndBuildChunks(ctx context.Context, userID, parentType, parentID, newText string, meta map[string]string) (int, int, error) {
	if b.disabledParents[parentType] {
		return 0, 0, nil
	}
	sentences := splitSentences(newText)
	if len(sentences) == 0 {
		return 0, 0, nil
	}

	// 读旧 chunk 建索引用
	oldChunks, err := b.chunkRepo.ListByParent(ctx, parentType, parentID)
	if err != nil {
		log.Printf("[chunk] DiffAndBuildChunks list old error: %v", err)
	}

	oldByContent := make(map[string]*model.EmbeddingChunk)
	for i := range oldChunks {
		c := &oldChunks[i]
		oldByContent[c.Content] = c
	}

	// 找出需要新增和保留的句子
	type keepItem struct {
		chunk model.EmbeddingChunk
		embID string
	}
	var toKeep []keepItem
	var toEmbed []struct {
		idx  int
		text string
	}

	keptContent := make(map[string]bool)
	for _, s := range sentences {
		if old, ok := oldByContent[s]; ok && !keptContent[s] {
			toKeep = append(toKeep, keepItem{
				chunk: *old,
				embID: model.ChunkVectorKey(parentType, parentID, old.ChunkIndex),
			})
			keptContent[s] = true
		} else if !keptContent[s] {
			toEmbed = append(toEmbed, struct {
				idx  int
				text string
			}{idx: len(toEmbed), text: s})
			keptContent[s] = true
		}
	}

	// 删除旧 chunk 中不再需要的（DB + 向量库）
	keepIDs := make(map[string]bool)
	for _, k := range toKeep {
		keepIDs[k.chunk.ID] = true
	}
	for _, old := range oldChunks {
		if !keepIDs[old.ID] {
			b.vectorStore.Delete(old.ID)
		}
	}

	// 嵌入新句子
	var newChunks []model.EmbeddingChunk
	var newEmbeddings []model.Embedding
	for _, e := range toEmbed {
		vec, err := b.embedder.Embed(ctx, e.text)
		if err != nil {
			log.Printf("[chunk] embed %s/%s[%d] error: %v", parentType, parentID[:8], e.idx, err)
			continue
		}
		chunkID := model.ChunkVectorKey(parentType, parentID, e.idx)
		newChunks = append(newChunks, model.EmbeddingChunk{
			ID:         uuid.New().String(),
			ParentType: parentType,
			ParentID:   parentID,
			ChunkIndex: e.idx,
			UserID:     userID,
			Content:    e.text,
		})
		embStr := llm.EmbeddingToString(vec)
		newEmbeddings = append(newEmbeddings, model.Embedding{
			ID:         chunkID,
			EntityType: model.EntityTypeChunk,
			EntityID:   chunkID,
			UserID:     userID,
			Model:      b.embedder.ModelName(),
			Embedding:  &embStr,
		})
		b.vectorStore.Upsert(chunkID, vec, buildChunkMeta(parentType, parentID, userID, meta))
	}

	// 删除 DB 中不再需要的旧 chunk
	if len(oldChunks) > 0 {
		var delIDs []string
		for _, old := range oldChunks {
			if !keepIDs[old.ID] {
				delIDs = append(delIDs, old.ID)
			}
		}
		if len(delIDs) > 0 {
			if err := b.chunkRepo.DeleteByParent(ctx, parentType, parentID); err != nil {
				log.Printf("[chunk] DiffAndBuildChunks delete old error: %v", err)
			}
		}
	}

	// 写入新 chunk
	if len(newChunks) > 0 {
		if err := b.chunkRepo.BatchCreate(ctx, newChunks); err != nil {
			log.Printf("[chunk] DiffAndBuildChunks batch create error: %v", err)
			return len(toKeep), 0, err
		}
	}
	if len(newEmbeddings) > 0 {
		if err := b.embeddingRepo.BatchUpsert(ctx, newEmbeddings); err != nil {
			log.Printf("[chunk] DiffAndBuildChunks batch upsert error: %v", err)
			return len(toKeep), 0, err
		}
	}

	log.Printf("[chunk] DiffAndBuild: %s/%s kept=%d new=%d (total old=%d)",
		parentType, parentID[:8], len(toKeep), len(newChunks), len(oldChunks))
	return len(toKeep), len(newChunks), nil
}
