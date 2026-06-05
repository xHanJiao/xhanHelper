package agent

import (
	"context"
	"fmt"
	"log"

	"xhan-helper/internal/app"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// MissionEmbedder — Mission 向量嵌入
type MissionEmbedder struct {
	embedder      *llm.Embedder
	vectorStore   app.VectorStore
	embeddingRepo *service.EmbeddingRepo
}

func NewMissionEmbedder(infra *app.Infra, embeddingRepo *service.EmbeddingRepo) *MissionEmbedder {
	return &MissionEmbedder{
		embedder:      infra.Embedder,
		vectorStore:   infra.VectorStore,
		embeddingRepo: embeddingRepo,
	}
}

// Upsert 嵌入 mission goal，写入向量库和 Embedding 表
func (e *MissionEmbedder) Upsert(ctx context.Context, m *model.Mission) error {
	if m.Goal == "" {
		return nil
	}

	// 先删除旧向量（如果存在）
	e.vectorStore.Delete(fmt.Sprintf("mission:%s", m.ID))

	vec, err := e.embedder.Embed(ctx, m.Goal)
	if err != nil {
		return fmt.Errorf("[mission_embedder] embed mission goal %q: %w", m.Goal[:min(60, len(m.Goal))], err)
	}

	meta := map[string]string{
		"type":       "mission",
		"user_id":    m.UserID,
		"mission_id": m.ID,
		"goal":       m.Goal,
	}
	e.vectorStore.Upsert(fmt.Sprintf("mission:%s", m.ID), vec, meta)

	embStr := llm.EmbeddingToString(vec)
	if err := e.embeddingRepo.Upsert(ctx, &model.Embedding{
		ID:         m.ID,
		EntityType: "mission",
		EntityID:   m.ID,
		UserID:     m.UserID,
		Model:      e.embedder.ModelName(),
		Embedding:  &embStr,
	}); err != nil {
		log.Printf("[mission_embedder] save embedding error: %v", err)
		return err
	}

	log.Printf("[mission_embedder] embedded mission %s goal=%s", m.ID[:8], m.Goal[:min(40, len(m.Goal))])
	return nil
}

// Delete 从向量库删除 mission 的向量
func (e *MissionEmbedder) Delete(ctx context.Context, missionID string) {
	e.vectorStore.Delete(fmt.Sprintf("mission:%s", missionID))
	if err := e.embeddingRepo.DeleteByEntity(ctx, "mission", missionID); err != nil {
		log.Printf("[mission_embedder] delete embedding error: %v", err)
	}
	log.Printf("[mission_embedder] deleted mission embedding %s", missionID[:8])
}

// UpsertEmbedding 实现 tool.MissionEmbedder 接口（供 tool 包避免循环依赖）
func (e *MissionEmbedder) UpsertEmbedding(missionID, userID, goal string) error {
	return e.Upsert(context.Background(), &model.Mission{
		ID:     missionID,
		UserID: userID,
		Goal:   goal,
	})
}

// DeleteEmbedding 实现 tool.MissionEmbedder 接口
func (e *MissionEmbedder) DeleteEmbedding(missionID string) {
	e.Delete(context.Background(), missionID)
}
