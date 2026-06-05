package retrieval

import (
	"context"
	"fmt"
	"log"
	"strings"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/service"
)

// MissionSource — 任务上下文数据源，向量检索与当前对话相关的 Mission
type MissionSource struct {
	missionRepo *service.MissionRepo
	vectorStore VectorStore
}

func NewMissionSource(missionRepo *service.MissionRepo, vs VectorStore) *MissionSource {
	return &MissionSource{missionRepo: missionRepo, vectorStore: vs}
}

func (s *MissionSource) Name() string { return "mission" }

func (s *MissionSource) Fetch(ctx context.Context, opts SearchOptions) ([]ContextPacket, []NearMissItem, error) {
	queryVec := opts.Embedding
	userID := opts.UserID
	threshold := opts.MemoryThreshold

	if s.vectorStore == nil || len(queryVec) == 0 || s.missionRepo == nil {
		return nil, nil, nil
	}

	results := s.vectorStore.Search(queryVec, 20, func(e Entry) bool {
		return e.Metadata["type"] == "mission" && e.Metadata["user_id"] == userID
	})
	if len(results) == 0 {
		return nil, nil, nil
	}

	log.Printf("[mission_source] vector search: user_id=%s matched=%d threshold=%.2f",
		userID[:8], len(results), threshold)

	// 收集 mission ID + 向量分数
	type hit struct {
		id    string
		score float64
	}
	var hits []hit
	idSet := make(map[string]float64) // 去重，保留最高分
	for _, r := range results {
		mid := r.Metadata["mission_id"]
		if mid == "" {
			mid = strings.TrimPrefix(r.ID, "mission:")
		}
		if existing, ok := idSet[mid]; !ok || r.Score > existing {
			idSet[mid] = r.Score
		}
	}

	ids := make([]string, 0, len(idSet))
	for mid, score := range idSet {
		ids = append(ids, mid)
		hits = append(hits, hit{id: mid, score: score})
	}

	// 一次 DB 查询
	missions, err := s.missionRepo.ListByIDs(ctx, ids, userID)
	if err != nil {
		log.Printf("[mission_source] ListByIDs error: %v", err)
		return nil, nil, err
	}

	// 按 score 排序后处理
	var selected []ContextPacket
	var nearMiss []NearMissItem

	for _, h := range hits {
		m, ok := missions[h.id]
		if !ok {
			nearMiss = append(nearMiss, NearMissItem{
				ID:          h.id,
				VectorScore: h.score,
				Reason:      "not_active_or_deleted",
			})
			continue
		}

		text := fmt.Sprintf("追踪任务: %s\n最近状态: %s", m.Goal, m.LastSummary)
		if m.LastSummary == "" {
			text = fmt.Sprintf("追踪任务: %s", m.Goal)
		}

		if h.score >= threshold {
			selected = append(selected, ContextPacket{
				ID:         m.ID,
				Type:       "mission",
				Text:       text,
				Score:      h.score,
				FinalScore: h.score,
			})
		} else {
			nearMiss = append(nearMiss, NearMissItem{
				ID:          m.ID,
				Text:        m.Goal,
				VectorScore: h.score,
				Reason:      fmt.Sprintf("vector_below_threshold(%.3f)", h.score),
			})
		}
	}

	return selected, nearMiss, nil
}

func (s *MissionSource) Warmup(ctx context.Context, embedder *llm.Embedder) {
	log.Printf("[mission_source] warmup: handled externally (see warmupMissionEmbeddings)")
}
