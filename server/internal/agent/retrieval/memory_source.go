package retrieval

import (
	"context"
	"fmt"
	"log"
	"strings"

	"xhan-helper/internal/config"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// MemorySource — 记忆上下文数据源
type MemorySource struct {
	vectorStore   VectorStore
	memRepo       *service.UserMemoryRepo
	embeddingRepo *service.EmbeddingRepo
	ranker        *Ranker
}

func NewMemorySource(vs VectorStore, memRepo *service.UserMemoryRepo, embeddingRepo *service.EmbeddingRepo, ranker *Ranker) *MemorySource {
	if ranker == nil {
		ranker = DefaultRanker()
	}
	return &MemorySource{vectorStore: vs, memRepo: memRepo, embeddingRepo: embeddingRepo, ranker: ranker}
}

func (s *MemorySource) Name() string { return "memory" }

func (s *MemorySource) Fetch(ctx context.Context, queryVec []float64, query, userID, convID string,
	threshold float64, limit int) ([]ContextPacket, []NearMissItem, error) {

	results := s.vectorStore.Search(queryVec, limit, func(e Entry) bool {
		return e.Metadata["type"] == "mem" && e.Metadata["user_id"] == userID
	})
	if len(results) == 0 {
		return nil, nil, nil
	}

	type hit struct {
		id          string
		vectorScore float64
	}
	var hits []hit
	var nearMiss []NearMissItem
	for _, res := range results {
		if !strings.HasPrefix(res.ID, "mem:") {
			nearMiss = append(nearMiss, NearMissItem{ID: res.ID, Text: res.ID, VectorScore: res.Score, Reason: "wrong_prefix"})
			continue
		}
		id := strings.TrimPrefix(res.ID, "mem:")
		if res.Score < threshold {
			nearMiss = append(nearMiss, NearMissItem{ID: id, Text: id, VectorScore: res.Score, Reason: "below_threshold"})
			continue
		}
		hits = append(hits, hit{id: id, vectorScore: res.Score})
		log.Printf("[vector] memory hit: id=%s, score=%.4f", res.ID, res.Score)
	}
	if len(hits) == 0 {
		return nil, nearMiss, nil
	}

	memIDs := make([]string, len(hits))
	scoreMap := make(map[string]float64, len(hits))
	for i, h := range hits {
		memIDs[i] = h.id
		scoreMap[h.id] = h.vectorScore
	}

	memories, err := s.memRepo.GetByIDs(ctx, memIDs)
	if err != nil {
		return nil, nil, err
	}
	if len(memories) == 0 {
		return nil, nil, fmt.Errorf("all hit memories ids not found, ids: %v", memIDs)
	}

	// 异步批量更新确认计数
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
	scored := s.ranker.Score(query, inputs)

	packets := make([]ContextPacket, 0, len(scored))
	for _, sc := range scored {
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
		packets = append(packets, ContextPacket{
			ID:           sc.ID,
			Type:         "memory",
			Text:         text,
			Score:        sc.VectorScore,
			KeywordScore: sc.KeywordScore,
			FinalScore:   sc.FinalScore,
		})
	}
	return packets, nearMiss, nil
}

func (s *MemorySource) Warmup(ctx context.Context, embedder *llm.Embedder) {
	if s.vectorStore == nil || embedder == nil || s.embeddingRepo == nil {
		panic("[vector] memory warmup: skipped (no config)")
	}
	log.Printf("[vector] memory warmup: loading existing memories...")

	const pageSize = 100
	loaded, embedded := 0, 0

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
			existingEmb, err := s.embeddingRepo.GetByEntity(ctx, model.EntityTypeMemory, m.ID)
			if err != nil {
				log.Printf("[vector] mem warmup: GetByEntity error for %s: %v", m.ID, err)
			}
			if existingEmb != nil && existingEmb.Embedding != nil {
				continue
			}
			vec, err := embedder.Embed(ctx, m.MemoryValue)
			if err != nil {
				log.Printf("[vector] memory warmup: embed %s error: %v", m.ID[:8], err)
				continue
			}
			s.vectorStore.Upsert("mem:"+m.ID, vec, map[string]string{"type": "mem", "user_id": m.UserID})
			embStr := llm.EmbeddingToString(vec)
			err = s.embeddingRepo.Upsert(ctx, &model.Embedding{
				ID:         m.ID,
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
