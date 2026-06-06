package agent

import (
	"context"
	"fmt"
	"log"
	"strings"

	"xhan-helper/internal/app"
	"xhan-helper/internal/idgen"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// ConceptEmbedService — 概念嵌入服务
type ConceptEmbedService struct {
	conceptRepo   *service.ConceptEmbeddingRepo
	embeddingRepo *service.EmbeddingRepo
	embedder      *llm.Embedder
	vectorStore   app.VectorStore
}

func NewConceptEmbedService(infra *app.Infra, conceptRepo *service.ConceptEmbeddingRepo, embeddingRepo *service.EmbeddingRepo) *ConceptEmbedService {
	return &ConceptEmbedService{
		conceptRepo:   conceptRepo,
		embeddingRepo: embeddingRepo,
		embedder:      infra.Embedder,
		vectorStore:   infra.VectorStore,
	}
}

// EnsureTopicEmbedded 确保 topic 已嵌入（不存在则异步创建）
func (s *ConceptEmbedService) EnsureTopicEmbedded(ctx context.Context, userID, topic, parentType, parentID string) {
	s.ensureEmbedded(ctx, "topic", topic, userID, parentType, parentID)
}

// EnsureTagsEmbedded 确保 tags 已嵌入（逗号分隔，不存在则异步创建）
func (s *ConceptEmbedService) EnsureTagsEmbedded(ctx context.Context, userID, tags, parentType, parentID string) {
	for _, tag := range strings.Split(tags, ",") {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		s.ensureEmbedded(ctx, "tag", tag, userID, parentType, parentID)
	}
}

// GetOrEmbed 获取 concept 的嵌入向量，优先读缓存，缓存未命中则嵌入并写缓存。
func (s *ConceptEmbedService) GetOrEmbed(ctx context.Context, conceptType, value, userID string) ([]float64, error) {
	if value == "" {
		return nil, nil
	}
	concept, err := s.conceptRepo.GetByValue(ctx, conceptType, value, userID)
	if err != nil {
		return nil, fmt.Errorf("get concept: %w", err)
	}
	if concept != nil {
		emb, err := s.embeddingRepo.GetByEntity(ctx, model.EntityTypeConcept, concept.ID)
		if err != nil {
			return nil, fmt.Errorf("get embedding: %w", err)
		}
		if emb != nil && emb.Embedding != nil {
			return llm.StringToEmbedding(*emb.Embedding)
		}
		return nil, nil
	}

	// 未缓存，嵌入 + 写缓存
	vec, err := s.embedder.Embed(ctx, value)
	if err != nil {
		return nil, fmt.Errorf("embed: %w", err)
	}

	concept = &model.ConceptEmbedding{
		ID:     idgen.Next(),
		Type:   conceptType,
		Value:  value,
		UserID: userID,
	}
	if err := s.conceptRepo.Create(ctx, concept); err != nil {
		return nil, fmt.Errorf("create concept: %w", err)
	}

	s.vectorStore.Upsert("concept:"+concept.ID, vec, map[string]string{
		"type":          "concept",
		"concept_type":  conceptType,
		"concept_value": value,
		"user_id":       userID,
	})

	embStr := llm.EmbeddingToString(vec)
	if err := s.embeddingRepo.Upsert(ctx, &model.Embedding{
		ID:         concept.ID,
		EntityType: model.EntityTypeConcept,
		EntityID:   concept.ID,
		UserID:     userID,
		Model:      s.embedder.ModelName(),
		Embedding:  &embStr,
	}); err != nil {
		log.Printf("[concept] save embedding error: %v", err)
	}

	log.Printf("[concept] get-or-embed %s/%s", conceptType, value)
	return vec, nil
}

func (s *ConceptEmbedService) ensureEmbedded(ctx context.Context, conceptType, value, userID, parentType, parentID string) {
	if value == "" {
		return
	}
	exists, err := s.conceptRepo.Exists(ctx, conceptType, value, userID)
	if err != nil {
		log.Printf("[concept] check exists error: %v", err)
		return
	}
	if exists {
		return
	}

	concept := &model.ConceptEmbedding{
		ID:         idgen.Next(),
		Type:       conceptType,
		Value:      value,
		UserID:     userID,
		ParentType: parentType,
		ParentID:   parentID,
	}
	if err := s.conceptRepo.Create(ctx, concept); err != nil {
		log.Printf("[concept] create error: %v", err)
		return
	}

	vec, err := s.embedder.Embed(ctx, value)
	if err != nil {
		log.Printf("[concept] embed %s/%s error: %v", conceptType, value, err)
		return
	}

	s.vectorStore.Upsert("concept:"+concept.ID, vec, map[string]string{
		"type":          "concept",
		"concept_type":  conceptType,
		"concept_value": value,
		"user_id":       userID,
	})

	embStr := llm.EmbeddingToString(vec)
	if err := s.embeddingRepo.Upsert(ctx, &model.Embedding{
		ID:         concept.ID,
		EntityType: model.EntityTypeConcept,
		EntityID:   concept.ID,
		UserID:     userID,
		Model:      s.embedder.ModelName(),
		Embedding:  &embStr,
	}); err != nil {
		log.Printf("[concept] save embedding %s/%s error: %v", conceptType, value, err)
		return
	}

	log.Printf("[concept] embedded %s/%s (%s)", conceptType, value, concept.ID[:8])
}
