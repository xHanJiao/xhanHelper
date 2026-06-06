package retrieval

import (
	"context"
	"fmt"
	"log"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/service"
)

// ThemeSource — 主题上下文数据源，负责向量检索活跃主题。
type ThemeSource struct {
	themeStateRepo *service.ThemeStateRepo
	vectorStore    VectorStore
}

func NewThemeSource(themeStateRepo *service.ThemeStateRepo, vs VectorStore) *ThemeSource {
	return &ThemeSource{themeStateRepo: themeStateRepo, vectorStore: vs}
}

func (s *ThemeSource) Name() string { return "theme" }

func (s *ThemeSource) Fetch(ctx context.Context, opts SearchOptions) ([]ContextPacket, []NearMissItem, error) {
	queryVec := opts.Embedding
	userID := opts.UserID
	threshold := opts.Memory.Threshold

	activeThemes, err := s.themeStateRepo.ListActive(ctx, userID)
	if err != nil {
		return nil, nil, fmt.Errorf("list active themes: %w", err)
	}
	if len(activeThemes) == 0 {
		return nil, nil, nil
	}

	// 向量搜索
	vectorScores := make(map[string]float64)
	if s.vectorStore != nil && len(queryVec) > 0 {
		results := s.vectorStore.Search(queryVec, 20, func(e Entry) bool {
			return e.Metadata["type"] == "theme" && e.Metadata["user_id"] == userID
		})
		for _, r := range results {
			themeID := r.Metadata["theme_id"]
			if themeID == "" {
				continue
			}
			vectorScores[themeID] = r.Score
		}
		log.Printf("[theme] vector search: user_id=%s, matched=%d, threshold=%.2f",
			userID, len(results), threshold)
	}

	var selected []ContextPacket
	var nearMiss []NearMissItem

	for _, t := range activeThemes {
		vecScore, hasVec := vectorScores[t.ID]

		if hasVec && vecScore >= threshold {
			selected = append(selected, ContextPacket{
				ID:         t.ID,
				Type:       "theme",
				Text:       t.Name,
				Score:      vecScore,
				FinalScore: vecScore,
			})
		} else {
			reason := "other_active"
			nmScore := 0.0
			if hasVec {
				reason = fmt.Sprintf("vector_below_threshold(%.3f)", vecScore)
				nmScore = vecScore
			}
			nearMiss = append(nearMiss, NearMissItem{
				ID:          t.ID,
				Text:        t.Name,
				VectorScore: nmScore,
				Reason:      reason,
			})
		}
	}

	return selected, nearMiss, nil
}

func (s *ThemeSource) Warmup(ctx context.Context, embedder *llm.Embedder) {
	log.Printf("[vector] theme warmup: handled externally (see warmupThemeEmbeddings)")
}
