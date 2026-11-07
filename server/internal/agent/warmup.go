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
// 图片描述向量另走 WarmupImageEmbeddings（有外呼回填，不绑就绪信号）。
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

// WarmupImageEmbeddings 加载图片描述向量、清孤儿、回填「有描述但没向量」的老图。
// 有外呼回填，调用方应放在 ready 之后，别绑住健康检查的就绪信号。
// 语义搜索缺结果时（warmup 未就绪 / 索引失败）由 image.search_images 的 hint 引导用户换关键词。
func WarmupImageEmbeddings(ctx context.Context, vs app.VectorStore, imageRepo *service.ImageAssetRepo, embeddingRepo *service.EmbeddingRepo, embedder *llm.Embedder) {
	if imageRepo == nil || embedder == nil || embeddingRepo == nil {
		panic("warmup images: nil dependency (wiring bug)")
	}
	const pageSize = 200
	loaded := 0
	indexed := make(map[string]bool)
	var allIDs []string

	for offset := 0; ; offset += pageSize {
		embeddings, err := embeddingRepo.ListByType(ctx, model.EntityTypeImage, pageSize, offset)
		if err != nil {
			log.Printf("[warmup] image embeddings ListByType error: %v", err)
			break
		}
		if len(embeddings) == 0 {
			break
		}
		for _, emb := range embeddings {
			allIDs = append(allIDs, emb.EntityID)
			if emb.Embedding == nil {
				continue
			}
			vec, err := llm.StringToEmbedding(*emb.Embedding)
			if err != nil || len(vec) == 0 {
				continue
			}
			vs.Upsert(model.VectorPrefixImage+emb.EntityID, vec, map[string]string{
				"type":    model.VectorTypeImage,
				"user_id": emb.UserID,
			})
			indexed[emb.EntityID] = true
			loaded++
		}
		if len(embeddings) < pageSize {
			break
		}
	}

	// 孤儿对账：记录已不存在的向量行删掉（删图时清理失败的兜底）。
	pruned := 0
	for start := 0; start < len(allIDs); start += pageSize {
		end := min(start+pageSize, len(allIDs))
		batch := allIDs[start:end]
		existing, err := imageRepo.ExistingIDs(ctx, batch)
		if err != nil {
			log.Printf("[warmup] image embeddings ExistingIDs error: %v", err)
			continue
		}
		exists := make(map[string]bool, len(existing))
		for _, id := range existing {
			exists[id] = true
		}
		var stale []string
		for _, id := range batch {
			if !exists[id] {
				stale = append(stale, id)
			}
		}
		if len(stale) == 0 {
			continue
		}
		if _, err := embeddingRepo.DeleteByEntities(ctx, model.EntityTypeImage, stale); err != nil {
			log.Printf("[warmup] delete stale image embeddings error: %v", err)
			continue
		}
		for _, id := range stale {
			vs.Delete(model.VectorPrefixImage + id)
		}
		pruned += len(stale)
	}

	// 回填：有描述但没有向量的老图（本功能上线前的存量）。
	indexer := NewImageIndexer(embedder, vs, embeddingRepo)
	backfilled := 0
	afterID := ""
	for {
		images, err := imageRepo.ListDescribedAfter(ctx, afterID, pageSize)
		if err != nil {
			log.Printf("[warmup] image backfill ListDescribedAfter error: %v", err)
			break
		}
		if len(images) == 0 {
			break
		}
		for i := range images {
			a := &images[i]
			afterID = a.ID
			if indexed[a.ID] {
				continue
			}
			if err := indexer.IndexImage(ctx, a.UserID, a.ID, a.Description); err != nil {
				log.Printf("[warmup] image backfill %s error: %v", shortID(a.ID), err)
				continue
			}
			backfilled++
		}
		if len(images) < pageSize {
			break
		}
	}

	log.Printf("[warmup] image embeddings: %d loaded, %d backfilled, %d stale pruned", loaded, backfilled, pruned)
}
