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
func WarmupAll(ctx context.Context, vs app.VectorStore, embeddingRepo *service.EmbeddingRepo, conceptEmbedRepo *service.ConceptEmbeddingRepo, themeStateRepo *service.ThemeStateRepo) {
	warmupThemeEmbeddings(ctx, embeddingRepo, vs, conceptEmbedRepo, themeStateRepo)
	warmupChunkEmbeddings(ctx, embeddingRepo, vs)
	warmupConceptEmbeddings(ctx, embeddingRepo, vs)
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
						"type":    "theme",
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
					vectorStore.Upsert("theme:"+emb.EntityID, vec, meta)
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

func warmupConceptEmbeddings(ctx context.Context, embeddingRepo *service.EmbeddingRepo, vectorStore app.VectorStore) {
	const pageSize = 200
	loaded := 0
	for offset := 0; ; offset += pageSize {
		embeddings, err := embeddingRepo.ListByType(ctx, model.EntityTypeConcept, pageSize, offset)
		if err != nil {
			log.Printf("[warmup] concept embeddings ListByType error: %v", err)
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
			vectorStore.Upsert("concept:"+emb.EntityID, vec, map[string]string{
				"type":    "concept",
				"user_id": emb.UserID,
			})
			loaded++
		}
		if len(embeddings) < pageSize {
			break
		}
	}
	log.Printf("[warmup] concept embeddings: %d loaded into vector store", loaded)
}

func warmupChunkEmbeddings(ctx context.Context, embeddingRepo *service.EmbeddingRepo, vectorStore app.VectorStore) {
	const pageSize = 200
	loaded := 0
	for offset := 0; ; offset += pageSize {
		embeddings, err := embeddingRepo.ListByType(ctx, "chunk", pageSize, offset)
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
			vec, err := llm.StringToEmbedding(*emb.Embedding)
			if err != nil || len(vec) == 0 {
				continue
			}
			parentType, parentID := parseChunkEntityID(emb.EntityID)
			vectorStore.Upsert(emb.EntityID, vec, map[string]string{
				"type":        "chunk",
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
	log.Printf("[warmup] chunk embeddings: %d loaded into vector store", loaded)
}

// parseChunkEntityID 解析 chunk EntityID，返回 parentType 和 parentID
// 格式: chunk:<parentType>:<parentID>/<index>
func parseChunkEntityID(entityID string) (parentType, parentID string) {
	rest := entityID
	if idx := strings.Index(rest, ":"); idx > 0 {
		rest = rest[idx+1:]
	}
	if idx := strings.Index(rest, ":"); idx > 0 {
		parentType = rest[:idx]
		rest = rest[idx+1:]
	}
	if idx := strings.LastIndex(rest, "/"); idx > 0 {
		parentID = rest[:idx]
	}
	return
}
