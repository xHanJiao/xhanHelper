package agent

import (
	"context"
	"fmt"
	"log"
	"math"
	"strings"

	"xhan-helper/internal/config"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// MemorySource — 记忆上下文数据源
type MemorySource struct {
	vectorStore   *VectorStore
	memRepo       *service.UserMemoryRepo
	embeddingRepo *service.EmbeddingRepo
	decayRate     float64
}

func NewMemorySource(vs *VectorStore, memRepo *service.UserMemoryRepo, embeddingRepo *service.EmbeddingRepo, decayRate float64) *MemorySource {
	if decayRate <= 0 {
		decayRate = 0.02
	}
	return &MemorySource{vectorStore: vs, memRepo: memRepo, embeddingRepo: embeddingRepo, decayRate: decayRate}
}

func (s *MemorySource) Name() string { return "memory" }

func (s *MemorySource) Fetch(ctx context.Context, queryVec []float64, query, userID, convID string,
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

	// 收集通过阈值的记忆 ID 和向量分
	type hit struct {
		id          string
		vectorScore float64
	}
	var hits []hit
	for _, res := range results {
		if res.Score < threshold || !strings.HasPrefix(res.ID, "mem:") {
			continue
		}
		id := strings.TrimPrefix(res.ID, "mem:")
		hits = append(hits, hit{id: id, vectorScore: res.Score})
		log.Printf("[vector] memory hit: id=%s, score=%.4f", res.ID, res.Score)
	}
	if len(hits) == 0 {
		return nil, nil
	}

	memIDs := make([]string, len(hits))
	scoreMap := make(map[string]float64, len(hits))
	for i, h := range hits {
		memIDs[i] = h.id
		scoreMap[h.id] = h.vectorScore
	}

	memories, err := s.memRepo.GetByIDs(ctx, memIDs)
	if err != nil || len(memories) == 0 {
		return nil, nil
	}

	// 异步批量更新确认计数（不用请求 ctx，避免请求结束后 ctx 取消导致更新丢失）
	go func() {
		if err := s.memRepo.BatchUpdateConfirm(context.Background(), memIDs); err != nil {
			log.Printf("[vector] batch update confirm error: %v", err)
		}
	}()

	inputs := make([]scoreInput, len(memories))
	memByID := make(map[string]model.UserMemory, len(memories))
	for i, m := range memories {
		inputs[i] = scoreInput{ID: m.ID, Content: m.MemoryValue, CreatedAt: m.CreatedAt, VectorScore: scoreMap[m.ID]}
		memByID[m.ID] = m
	}
	scored := scoreItems(query, inputs, s.decayRate)

	packets := make([]ContextPacket, 0, len(scored))
	for idx, sc := range scored {
		m := memByID[sc.ID]
		var text string
		topicPart := ""
		if m.Topic != "" {
			topicPart = fmt.Sprintf("[%s] ", m.Topic)
		}
		text = fmt.Sprintf("- [%s] %s%s (已唤起%d次，最近唤起 %s)\n",
			m.CreatedAt.In(config.BJ).Format("2006-01-02"),
			topicPart,
			m.MemoryValue,
			m.ConfirmCount,
			m.LastConfirmedAt.In(config.BJ).Format("01-02"))
		if idx == 0 {
			text = "===== 相关记忆 =====\n" + text
		}
		packets = append(packets, ContextPacket{
			ID:                     sc.ID,
			Text:                   text,
			Score:                  sc.VectorScore,
			KeywordScore:           sc.KeywordScore,
			CorrelationCoefficient: sc.FinalScore,
			TokenCount:             int(math.Round(sc.Freshness * 100)),
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
