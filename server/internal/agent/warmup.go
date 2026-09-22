package agent

import (
	"context"
	"log"
	"strings"

	"xhan-helper/internal/app"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// WarmupAll 启动时将 embedding 表中各类型向量加载到内存 VectorStore
func WarmupAll(ctx context.Context, vs app.VectorStore, embeddingRepo *service.EmbeddingRepo, conceptEmbedRepo *service.ConceptEmbeddingRepo, themeStateRepo *service.ThemeStateRepo, factRepo *service.FactRepo, chunkDisabledParents []string) {
	warmupThemeEmbeddings(ctx, embeddingRepo, vs, conceptEmbedRepo, themeStateRepo)
	warmupChunkEmbeddings(ctx, embeddingRepo, vs, chunkDisabledParents)
	warmupToolEmbeddings(ctx, embeddingRepo, vs)
	warmupFactEmbeddings(ctx, embeddingRepo, vs, factRepo)
}

// warmupFactEmbeddings 加载 active 事实的向量。
// 以 facts 表为驱动（而非 embedding 表），软删行自然不进内存，且 metadata
// 的 subject/kind/content 直接来自 fact 行，无需回表补齐。
func warmupFactEmbeddings(ctx context.Context, embeddingRepo *service.EmbeddingRepo, vectorStore app.VectorStore, factRepo *service.FactRepo) {
	if factRepo == nil {
		return
	}
	const pageSize = 200
	loaded := 0
	for offset := 0; ; offset += pageSize {
		facts, err := factRepo.ListAllActivePaginated(ctx, pageSize, offset)
		if err != nil {
			log.Printf("[warmup] fact ListAllActivePaginated error: %v", err)
			break
		}
		if len(facts) == 0 {
			break
		}
		ids := make([]string, 0, len(facts))
		for _, f := range facts {
			ids = append(ids, f.ID)
		}
		embeddings, err := embeddingRepo.ListByEntities(ctx, model.EntityTypeFact, ids)
		if err != nil {
			log.Printf("[warmup] fact embeddings ListByEntities error: %v", err)
			break
		}
		embByID := make(map[string]*model.Embedding, len(embeddings))
		for i := range embeddings {
			embByID[embeddings[i].EntityID] = &embeddings[i]
		}
		for _, f := range facts {
			emb := embByID[f.ID]
			if emb == nil || emb.Embedding == nil {
				continue
			}
			vec, err := llm.StringToEmbedding(*emb.Embedding)
			if err != nil || len(vec) == 0 {
				continue
			}
			vectorStore.Upsert(model.VectorPrefixFact+f.ID, vec, map[string]string{
				"type":    model.VectorTypeFact,
				"user_id": f.UserID,
				"subject": f.Subject,
				"kind":    f.Kind,
				"content": f.Content,
			})
			loaded++
		}
		if len(facts) < pageSize {
			break
		}
	}
	log.Printf("[warmup] fact embeddings: %d loaded into vector store", loaded)
}

func warmupThemeEmbeddings(ctx context.Context, embeddingRepo *service.EmbeddingRepo, vectorStore app.VectorStore, conceptEmbedRepo *service.ConceptEmbeddingRepo, themeStateRepo *service.ThemeStateRepo) {
	const pageSize = 100
	loaded := 0

	conceptIDToValue := make(map[string]string)
	if conceptEmbedRepo != nil {
		allConcepts, cerr := conceptEmbedRepo.ListByTypeAll(ctx, "theme")
		if cerr != nil {
			log.Printf("[warmup] concept list error: %v", cerr)
		} else {
			for _, c := range allConcepts {
				conceptIDToValue[c.ID] = c.Value
			}
		}
	}

	userThemeMap := make(map[string]map[string]string)
	seenUsers := make(map[string]bool)
	{
		for offset := 0; ; offset += pageSize {
			embeddings, err := embeddingRepo.ListByType(ctx, model.EntityTypeTheme, pageSize, offset)
			if err != nil || len(embeddings) == 0 {
				break
			}
			for _, emb := range embeddings {
				if emb.UserID != "" && !seenUsers[emb.UserID] {
					seenUsers[emb.UserID] = true
					themes, terr := themeStateRepo.ListActive(ctx, emb.UserID)
					if terr == nil {
						nameMap := make(map[string]string)
						for _, t := range themes {
							nameMap[t.Name] = t.ID
						}
						userThemeMap[emb.UserID] = nameMap
					}
				}
			}
			if len(embeddings) < pageSize {
				break
			}
		}
	}

	for offset := 0; ; offset += pageSize {
		embeddings, err := embeddingRepo.ListByType(ctx, model.EntityTypeTheme, pageSize, offset)
		if err != nil {
			log.Printf("[warmup] theme embeddings ListByType error: %v", err)
			break
		}
		if len(embeddings) == 0 {
			break
		}
		for _, emb := range embeddings {
			if emb.Embedding != nil {
				vec, err := llm.StringToEmbedding(*emb.Embedding)
				if err == nil && len(vec) > 0 {
					meta := map[string]string{
						"type":    model.VectorTypeTheme,
						"user_id": emb.UserID,
					}
					if conceptValue, ok := conceptIDToValue[emb.EntityID]; ok {
						meta["concept_value"] = conceptValue
						nameMap := userThemeMap[emb.UserID]
						if tid, ok := nameMap[conceptValue]; ok {
							meta["theme_id"] = tid
						} else {
							for name, tid := range nameMap {
								if strings.HasPrefix(conceptValue, name+" ") || conceptValue == name {
									meta["theme_id"] = tid
									break
								}
							}
						}
					}
					vectorStore.Upsert(model.VectorPrefixTheme+emb.EntityID, vec, meta)
					loaded++
				}
			}
		}
		if len(embeddings) < pageSize {
			break
		}
	}
	log.Printf("[warmup] theme embeddings: %d loaded into vector store", loaded)
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
