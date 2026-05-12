package agent

import (
	"context"
	"fmt"
	"log"
	"strings"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// MemorySource — 记忆上下文数据源
type MemorySource struct {
	vectorStore   *VectorStore
	memRepo       *service.UserMemoryRepo
	embeddingRepo *service.EmbeddingRepo
}

func NewMemorySource(vs *VectorStore, memRepo *service.UserMemoryRepo, embeddingRepo *service.EmbeddingRepo) *MemorySource {
	return &MemorySource{vectorStore: vs, memRepo: memRepo, embeddingRepo: embeddingRepo}
}

func (s *MemorySource) Name() string { return "memory" }

func (s *MemorySource) Fetch(ctx context.Context, queryVec []float64, userID, convID string,
	threshold float64, limit int) ([]ContextPacket, error) {
	if s.vectorStore == nil || s.memRepo == nil {
		return nil, nil
	}

	results := s.vectorStore.Search(queryVec, limit, func(e vectorEntry) bool {
		return e.Metadata["type"] == "mem" && e.Metadata["user_id"] == userID
	})
	if len(results) == 0 {
		return nil, nil
	}

	var memIDs []string
	for _, res := range results {
		if res.Score < threshold || !strings.HasPrefix(res.ID, "mem:") {
			continue
		}
		memIDs = append(memIDs, strings.TrimPrefix(res.ID, "mem:"))
		log.Printf("[vector] memory hit: id=%s, score=%.4f", res.ID, res.Score)
	}
	if len(memIDs) == 0 {
		return nil, nil
	}

	memories, err := s.memRepo.GetByIDs(ctx, memIDs)
	if err != nil || len(memories) == 0 {
		return nil, nil
	}

	packets := make([]ContextPacket, 0, len(memories))
	for i, m := range memories {
		text := fmt.Sprintf("- %s\n", m.MemoryValue)
		if i == 0 {
			text = "===== 相关记忆 =====\n" + text
		}
		packets = append(packets, ContextPacket{
			ID:   m.ID,
			Text: text,
		})
	}
	return packets, nil
}

func (s *MemorySource) Warmup(ctx context.Context, embedder *llm.Embedder) {
	if s.vectorStore == nil || embedder == nil || s.embeddingRepo == nil {
		log.Printf("[vector] memory warmup: skipped (no config)")
		return
	}
	log.Printf("[vector] memory warmup: loading existing memories...")

	const pageSize = 100
	loaded, embedded := 0, 0

	// 先从 embeddings 表加载已有的向量
	for offset := 0; ; offset += pageSize {
		embeddings, err := s.embeddingRepo.ListByType(ctx, model.EntityTypeMemory, pageSize, offset)
		if err != nil {
			log.Printf("[vector] memory warmup: list embeddings error: %v", err)
			break
		}
		if len(embeddings) == 0 {
			break
		}
		for _, emb := range embeddings {
			if emb.Embedding != nil {
				if vec, err := llm.StringToEmbedding(*emb.Embedding); err == nil && len(vec) > 0 {
					s.vectorStore.Upsert("mem:"+emb.EntityID, vec, map[string]string{"type": "mem", "user_id": emb.UserID})
					loaded++
				}
			}
		}
		if len(embeddings) < pageSize {
			break
		}
	}

	// 再检查是否有记忆没有向量，需要新生成
	for offset := 0; ; offset += pageSize {
		memories, err := s.memRepo.ListAllUsersMemoriesPaginated(ctx, pageSize, offset)
		if err != nil {
			log.Printf("[vector] memory warmup: list memories error: %v", err)
			break
		}
		if len(memories) == 0 {
			break
		}
		for _, m := range memories {
			// 检查是否已有向量
			existingEmb, err := s.embeddingRepo.GetByEntity(ctx, model.EntityTypeMemory, m.ID)
			if err != nil {
				log.Printf("[vector] mem warmup: GetByEntity error for %s: %v", m.ID, err)
			}
			if existingEmb != nil && existingEmb.Embedding != nil {
				continue // 已有，跳过
			}
			// 需要生成新向量
			vec, err := embedder.Embed(ctx, m.MemoryValue)
			if err != nil {
				log.Printf("[vector] memory warmup: embed %s error: %v", m.ID[:8], err)
				continue
			}
			s.vectorStore.Upsert("mem:"+m.ID, vec, map[string]string{"type": "mem", "user_id": m.UserID})
			embStr := llm.EmbeddingToString(vec)
			err = s.embeddingRepo.Upsert(ctx, &model.Embedding{
				ID:         m.ID, // 使用记忆 ID 作为 embedding ID
				EntityType: model.EntityTypeMemory,
				EntityID:   m.ID,
				UserID:     m.UserID,
				Model:      embedder.ModelName(),
				Embedding:  &embStr,
			})
			if err != nil {
				log.Printf("[vector] memory warmup: save embedding %s error: %v", m.ID[:8], err)
			}
			embedded++
		}
		if len(memories) < pageSize {
			break
		}
	}

	log.Printf("[vector] memory warmup: %d from persisted, %d new, total=%d in store",
		loaded, embedded, s.vectorStore.Size())
}
