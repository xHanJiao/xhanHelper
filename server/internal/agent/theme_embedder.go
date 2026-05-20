package agent

import (
	"context"
	"log"

	"xhan-helper/internal/app"
	"xhan-helper/internal/idgen"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// ThemeEmbedder — 主题向量嵌入，复用 ConceptEmbedding 先查后存模式
type ThemeEmbedder struct {
	conceptRepo   *service.ConceptEmbeddingRepo
	embeddingRepo *service.EmbeddingRepo
	embedder      *llm.Embedder
	vectorStore   app.VectorStore
}

func NewThemeEmbedder(infra *app.Infra, conceptRepo *service.ConceptEmbeddingRepo, embeddingRepo *service.EmbeddingRepo) *ThemeEmbedder {
	return &ThemeEmbedder{
		conceptRepo:   conceptRepo,
		embeddingRepo: embeddingRepo,
		embedder:      infra.Embedder,
		vectorStore:   infra.VectorStore,
	}
}

// EnsureEmbedded 确保主题向量已写入（不存在则异步创建）。themeID 用于存到 vector metadata，供 dedup 反查
func (e *ThemeEmbedder) EnsureEmbedded(ctx context.Context, userID, themeID, name, description string) {
	text := name + " " + description
	if text == " " {
		return
	}

	exists, err := e.conceptRepo.Exists(ctx, "theme", text, userID)
	if err != nil {
		log.Printf("[theme_embedder] exists check error: %v", err)
		return
	}
	if exists {
		return
	}

	vec, err := e.embedder.Embed(ctx, text)
	if err != nil {
		log.Printf("[theme_embedder] embed error: %v", err)
		return
	}

	concept := &model.ConceptEmbedding{
		ID:         idgen.Next(),
		Type:       "theme",
		Value:      text,
		UserID:     userID,
		ParentType: "theme",
	}
	if err := e.conceptRepo.Create(ctx, concept); err != nil {
		log.Printf("[theme_embedder] concept create error: %v", err)
		return
	}

	e.vectorStore.Upsert("theme:"+concept.ID, vec, map[string]string{
		"type":     "theme",
		"user_id":  userID,
		"theme_id": themeID,
	})

	embStr := llm.EmbeddingToString(vec)
	if err := e.embeddingRepo.Upsert(ctx, &model.Embedding{
		ID:         concept.ID,
		EntityType: model.EntityTypeTheme,
		EntityID:   concept.ID,
		UserID:     userID,
		Model:      e.embedder.ModelName(),
		Embedding:  &embStr,
	}); err != nil {
		log.Printf("[theme_embedder] save embedding error: %v", err)
		return
	}

	log.Printf("[theme_embedder] embedded theme %q theme_id=%s (%s)", name, themeID[:8], concept.ID[:8])
}
