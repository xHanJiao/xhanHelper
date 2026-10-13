package agent

import (
	"context"
	"fmt"
	"log"

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
	embedder      app.Embedder
}

func NewConceptEmbedService(infra *app.Infra, conceptRepo *service.ConceptEmbeddingRepo, embeddingRepo *service.EmbeddingRepo) *ConceptEmbedService {
	return &ConceptEmbedService{
		conceptRepo:   conceptRepo,
		embeddingRepo: embeddingRepo,
		embedder:      infra.Embedder,
	}
}

// EnsureTopicEmbedded 确保 topic 已嵌入（不存在则异步创建）
func (s *ConceptEmbedService) EnsureTopicEmbedded(ctx context.Context, userID, topic, parentType, parentID string) {
	s.ensureEmbedded(ctx, "topic", topic, userID, parentType, parentID)
}

// EnsureTagsEmbedded 已废弃：tag 的向量不再被任何检索消费（tag 已以逗号字符串存在 note/memory 上），
// 保留空实现以兼容接口，避免继续产生无用 concept/embedding 行。
func (s *ConceptEmbedService) EnsureTagsEmbedded(ctx context.Context, userID, tags, parentType, parentID string) {
}

// GetOrEmbed 获取 concept 的嵌入向量：优先读缓存，缓存未命中**或向量行缺失/损坏**时重新嵌入并写回。
//
// 「概念行在、向量行不在」曾经是个不自愈死局——那时返回 (nil, nil)，调用方只能跳过，
// 于是那条 concept 永远参与不了相似度计算（topic 复用 / topic 命名 / 主题发现全受影响）。
// 孤儿概念行的成因与批量清理见 docs/bugs/017；本 bug（不自愈 + 日志撒谎）见 docs/bugs/037。
// 现在缺失与损坏一律补嵌，Upsert 幂等覆盖。
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
			vec, err := llm.StringToEmbedding(*emb.Embedding)
			switch {
			case err != nil:
				log.Printf("[concept] %s/%s 存量向量解析失败，重新嵌入: %v", conceptType, value, err)
			case len(vec) > 0:
				return vec, nil
			default:
				log.Printf("[concept] %s/%s 存量向量为空，重新嵌入", conceptType, value)
			}
		} else {
			log.Printf("[concept] %s/%s 有概念行但无向量行，补嵌入", conceptType, value)
		}
		return s.embedAndSave(ctx, concept, userID)
	}

	// 未缓存：建 concept 行 + 嵌入
	concept = &model.ConceptEmbedding{
		ID:     idgen.Next(),
		Type:   conceptType,
		Value:  value,
		UserID: userID,
	}
	if err := s.conceptRepo.Create(ctx, concept); err != nil {
		return nil, fmt.Errorf("create concept: %w", err)
	}
	log.Printf("[concept] get-or-embed %s/%s", conceptType, value)
	return s.embedAndSave(ctx, concept, userID)
}

// embedAndSave 嵌入 concept.Value 并把向量写回该 concept 行（Upsert 幂等，缺失/损坏都能被覆盖修好）。
//
// 空向量必须拦下：Embedder 在 API 返 200 但没带向量时返回 ([]，nil)、**不报错**
// （llm/embedding.go 的 Embed 末尾直接 return result.Data.Embedding, nil）。若照写，
// 库里是 "[]"，下次读出来还是空 → 判「向量为空，重新嵌入」→ **每次调用都重打一次网络请求**。
//
// 向量写不回去**不阻断调用方**：本次调用已经拿到向量，下次丢了再补——记日志说明这是有意降级。
func (s *ConceptEmbedService) embedAndSave(ctx context.Context, concept *model.ConceptEmbedding, userID string) ([]float64, error) {
	vec, err := s.embedder.Embed(ctx, concept.Value)
	if err != nil {
		return nil, fmt.Errorf("embed: %w", err)
	}
	if len(vec) == 0 {
		return nil, fmt.Errorf("embedder 返回空向量（dim=0）: %s/%s", concept.Type, concept.Value)
	}
	embStr := llm.EmbeddingToString(vec)
	if err := s.embeddingRepo.Upsert(ctx, &model.Embedding{
		ID:         concept.ID,
		EntityType: model.EntityTypeConcept,
		EntityID:   concept.ID,
		UserID:     userID,
		Model:      s.embedder.ModelName(),
		Embedding:  &embStr,
	}); err != nil {
		log.Printf("[concept] save embedding %s/%s error: %v", concept.Type, concept.Value, err)
	}
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
	if len(vec) == 0 {
		// 空向量不写回（别往库里灌 "[]"）。这里会留下一条无向量的概念行，
		// 但读侧 GetOrEmbed 会补嵌修好——见 docs/bugs/037。
		log.Printf("[concept] embed %s/%s 返回空向量，跳过写回", conceptType, value)
		return
	}

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
