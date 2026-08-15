package retrieval

import (
	"context"
	"fmt"
	"log"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/service"
)

// ThemeSource — 主题上下文数据源，负责向量检索活跃主题。
// Phase 15: 三路搜索 — name+desc 向量 + 多历史消息向量 + fact chunk 向量。
type ThemeSource struct {
	themeStateRepo *service.ThemeStateRepo
	vectorStore    VectorStore
}

func NewThemeSource(themeStateRepo *service.ThemeStateRepo, vs VectorStore) *ThemeSource {
	return &ThemeSource{themeStateRepo: themeStateRepo, vectorStore: vs}
}

func (s *ThemeSource) Name() string { return "theme" }

func (s *ThemeSource) Fetch(ctx context.Context, opts SearchOptions) ([]ContextPacket, []NearMissItem, error) {
	userID := opts.UserID
	threshold := opts.Theme.Threshold

	activeThemes, err := s.themeStateRepo.ListActive(ctx, userID)
	if err != nil {
		return nil, nil, fmt.Errorf("list active themes: %w", err)
	}
	if len(activeThemes) == 0 {
		return nil, nil, nil
	}

	// 并发搜索所有向量
	var allVecs [][]float64
	if len(opts.Embedding) > 0 {
		allVecs = append(allVecs, opts.Embedding)
	}
	allVecs = append(allVecs, opts.HistoryEmbeddings...)
	themeTopK := opts.Theme.Limit * 2
	chunkTopK := opts.Theme.Limit * 3
	if themeTopK == 0 {
		themeTopK = 20
	}
	if chunkTopK == 0 {
		chunkTopK = 30
	}
	searchRes := s.searchAll(allVecs, userID, themeTopK, chunkTopK)

	log.Printf("[theme] vector search: user_id=%s, active=%d, matched=%d, threshold=%.2f",
		userID, len(activeThemes), len(searchRes), threshold)

	var selected []ContextPacket
	var nearMiss []NearMissItem

	for _, t := range activeThemes {
		vecScore, hasVec := searchRes[t.ID]
		matched := hasVec && vecScore >= threshold

		meta := ThemeMeta{Description: t.Description}
		if matched {
			meta.StateText = t.StateText
		}

		// 只注入匹配的主题（带 StateText 详情）。未匹配主题不再进 selected——
		// 实测 77% 的消息 0 个匹配、最多 3 个，全量注入 25-41 个主题名是 token 浪费。
		// 主题名/状态文本不匹配时仅进 nearMiss 供调试；LLM 可用 theme_search.list_active_themes 主动查全量。
		if matched {
			selected = append(selected, ContextPacket{
				ID:    t.ID,
				Type:  "theme",
				Text:  t.Name,
				Score: vecScore,
				Meta:  meta,
			})
		}

		if !hasVec || vecScore < threshold {
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

// searchAll 并发执行所有向量对 theme+chunk 的搜索，合并取每个主题最高分
func (s *ThemeSource) searchAll(vectors [][]float64, userID string, themeTopK, chunkTopK int) map[string]float64 {
	type searchJob struct {
		vec      []float64
		searcher func([]float64, string, int) map[string]float64
		topK     int
	}
	var jobs []searchJob

	for _, v := range vectors {
		if len(v) > 0 {
			jobs = append(jobs, searchJob{v, s.searchThemes, themeTopK})
			jobs = append(jobs, searchJob{v, s.searchChunks, chunkTopK})
		}
	}

	type result struct{ m map[string]float64 }
	ch := make(chan result, len(jobs))
	for _, j := range jobs {
		go func(job searchJob) {
			ch <- result{m: job.searcher(job.vec, userID, job.topK)}
		}(j)
	}

	searchRes := make(map[string]float64)
	for range jobs {
		for id, score := range (<-ch).m {
			if existing, ok := searchRes[id]; !ok || score > existing {
				searchRes[id] = score
			}
		}
	}
	return searchRes
}
func (s *ThemeSource) searchThemes(queryVec []float64, userID string, topK int) map[string]float64 {
	res := make(map[string]float64)
	if len(queryVec) == 0 {
		return res
	}
	results := s.vectorStore.Search(queryVec, topK, func(e Entry) bool {
		return e.Metadata["type"] == "theme" && e.Metadata["user_id"] == userID
	})
	for _, r := range results {
		themeID := r.Metadata["theme_id"]
		if themeID == "" {
			continue
		}
		if existing, ok := res[themeID]; !ok || r.Score > existing {
			res[themeID] = r.Score
		}
	}
	return res
}

// searchChunks 搜索 type=chunk && parent_type=theme 向量，返回 parent_id(=theme_id) → score
func (s *ThemeSource) searchChunks(queryVec []float64, userID string, topK int) map[string]float64 {
	res := make(map[string]float64)
	if len(queryVec) == 0 {
		return res
	}
	results := s.vectorStore.Search(queryVec, topK, func(e Entry) bool {
		return e.Metadata["type"] == "chunk" &&
			e.Metadata["parent_type"] == "theme" &&
			e.Metadata["user_id"] == userID
	})
	for _, r := range results {
		themeID := r.Metadata["parent_id"]
		if themeID == "" {
			continue
		}
		if existing, ok := res[themeID]; !ok || r.Score > existing {
			res[themeID] = r.Score
		}
	}
	return res
}

func (s *ThemeSource) Warmup(ctx context.Context, embedder *llm.Embedder) {
	log.Printf("[vector] theme warmup: handled externally (see warmupThemeEmbeddings)")
}
