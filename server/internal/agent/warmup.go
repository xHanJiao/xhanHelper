package agent

import (
	"context"
	"log"

	"xhan-helper/internal/app"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// WarmupAll 启动时将 chunk/tool 向量加载到内存 VectorStore；旧 theme 向量已停写、无检索方，不再预热。
func WarmupAll(ctx context.Context, vs app.VectorStore, embeddingRepo *service.EmbeddingRepo, chunkDisabledParents []string) {
	warmupChunkEmbeddings(ctx, embeddingRepo, vs, chunkDisabledParents)
	warmupToolEmbeddings(ctx, embeddingRepo, vs)
}

func warmupChunkEmbeddings(ctx context.Context, embeddingRepo *service.EmbeddingRepo, vectorStore app.VectorStore, disabledParents []string) {
	const pageSize = 200
	loaded, skipped := 0, 0
	disabled := make(map[string]bool, len(disabledParents))
	for _, p := range disabledParents {
		disabled[p] = true
	}
	for offset := 0; ; offset += pageSize {
		embeddings, err := embeddingRepo.ListByType(ctx, model.EntityTypeChunk, pageSize, offset)
		if err != nil {
			log.Printf("[warmup] chunk embeddings ListByType error: %v", err)
			break
		}
		if len(embeddings) == 0 {
			break
		}
		for _, emb := range embeddings {
			if emb.Embedding == nil {
				continue
			}
			parentType, parentID := model.ParseChunkVectorKey(emb.EntityID)
			if disabled[parentType] {
				skipped++
				continue
			}
			vec, err := llm.StringToEmbedding(*emb.Embedding)
			if err != nil || len(vec) == 0 {
				continue
			}
			vectorStore.Upsert(emb.EntityID, vec, map[string]string{
				"type":        model.VectorTypeChunk,
				"parent_type": parentType,
				"parent_id":   parentID,
				"user_id":     emb.UserID,
			})
			loaded++
		}
		if len(embeddings) < pageSize {
			break
		}
	}
	log.Printf("[warmup] chunk embeddings: %d loaded into vector store (skipped %d disabled parents)", loaded, skipped)
}

func warmupToolEmbeddings(ctx context.Context, embeddingRepo *service.EmbeddingRepo, vectorStore app.VectorStore) {
	const pageSize = 100
	loaded := 0
	for offset := 0; ; offset += pageSize {
		embeddings, err := embeddingRepo.ListByType(ctx, model.EntityTypeTool, pageSize, offset)
		if err != nil {
			log.Printf("[warmup] tool embeddings ListByType error: %v", err)
			break
		}
		if len(embeddings) == 0 {
			break
		}
		for _, emb := range embeddings {
			if emb.Embedding == nil {
				continue
			}
			vec, err := llm.StringToEmbedding(*emb.Embedding)
			if err != nil || len(vec) == 0 {
				continue
			}
			vectorStore.Upsert(model.VectorPrefixTool+emb.EntityID, vec, map[string]string{
				"type":    model.VectorTypeTool,
				"user_id": emb.UserID,
			})
			loaded++
		}
		if len(embeddings) < pageSize {
			break
		}
	}
	log.Printf("[warmup] tool embeddings: %d loaded into vector store", loaded)
}
