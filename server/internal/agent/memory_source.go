package agent

import (
	"context"
	"fmt"
	"log"
	"strings"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/service"
)

// MemorySource — 记忆上下文数据源
type MemorySource struct {
	vectorStore *VectorStore
	memRepo     *service.UserMemoryRepo
}

func NewMemorySource(vs *VectorStore, memRepo *service.UserMemoryRepo) *MemorySource {
	return &MemorySource{vectorStore: vs, memRepo: memRepo}
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
		text := fmt.Sprintf("- %s: %s\n", m.MemoryKey, m.MemoryValue)
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
	if s.vectorStore == nil || embedder == nil || s.memRepo == nil {
		log.Printf("[vector] memory warmup: skipped (no config)")
		return
	}
	log.Printf("[vector] memory warmup: loading existing memories...")

	const pageSize = 100
	loaded, embedded := 0, 0
	for offset := 0; ; offset += pageSize {
		memories, err := s.memRepo.ListAllPaginated(ctx, pageSize, offset)
		if err != nil {
			log.Printf("[vector] memory warmup: list error: %v", err)
			return
		}
		if len(memories) == 0 {
			break
		}
		for _, m := range memories {
			if m.Embedding != nil {
				if vec, err := llm.StringToEmbedding(*m.Embedding); err == nil && len(vec) > 0 {
					s.vectorStore.Upsert("mem:"+m.ID, vec, map[string]string{"type": "mem", "user_id": m.UserID})
					loaded++
					continue
				}
			}
			text := m.MemoryKey + ": " + m.MemoryValue
			vec, err := embedder.Embed(ctx, text)
			if err != nil {
				log.Printf("[vector] memory warmup: embed %s error: %v", m.ID[:8], err)
				continue
			}
			s.vectorStore.Upsert("mem:"+m.ID, vec, map[string]string{"type": "mem", "user_id": m.UserID})
			embStr := llm.EmbeddingToString(vec)
			s.memRepo.UpdateEmbedding(ctx, m.ID, &embStr)
			embedded++
		}
		if len(memories) < pageSize {
			break
		}
	}

	log.Printf("[vector] memory warmup: %d from persisted, %d new, total=%d in store",
		loaded, embedded, s.vectorStore.Size())
}
