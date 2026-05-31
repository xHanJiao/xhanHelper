package retrieval

import (
	"context"
	"fmt"
	"log"
	"strings"

	"xhan-helper/internal/app"
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
	chunkRepo     *service.EmbeddingChunkRepo
	chunkBuilder  app.ChunkEmbedder
	ranker        *Ranker
}

func NewMemorySource(vs VectorStore, memRepo *service.UserMemoryRepo, embeddingRepo *service.EmbeddingRepo,
	chunkRepo *service.EmbeddingChunkRepo, chunkBuilder app.ChunkEmbedder, ranker *Ranker) *MemorySource {
	if ranker == nil {
		ranker = DefaultRanker()
	}
	return &MemorySource{
		vectorStore: vs, memRepo: memRepo, embeddingRepo: embeddingRepo,
		chunkRepo: chunkRepo, chunkBuilder: chunkBuilder, ranker: ranker,
	}
}

func (s *MemorySource) Name() string { return "memory" }

func (s *MemorySource) Fetch(ctx context.Context, opts SearchOptions) ([]ContextPacket, []NearMissItem, error) {
	queryVec := opts.Embedding
	query := opts.Query
	userID := opts.UserID
	threshold := opts.MemoryThreshold
	limit := opts.MemoryLimit

	var nearMiss []NearMissItem

	// 1. 主路径：chunk 级向量检索，先按 memID 去重取最高分
	candidates := make(map[string]float64) // memID → best chunk score

	chunkResults := s.vectorStore.Search(queryVec, limit*8, func(e Entry) bool {
		return e.Metadata["type"] == "chunk" &&
			e.Metadata["parent_type"] == "memory" &&
			e.Metadata["user_id"] == userID
	})
	for _, res := range chunkResults {
		memID := parseChunkParentID(res.ID)
		if memID == "" {
			nearMiss = append(nearMiss, NearMissItem{ID: res.ID, Text: res.ID, VectorScore: res.Score, Reason: "bad_chunk_id"})
			continue
		}
		if old, ok := candidates[memID]; !ok || res.Score > old {
			candidates[memID] = res.Score
		}
	}

	// 对去重后的候选按阈值分流
	hitMap := make(map[string]float64)
	for memID, score := range candidates {
		if score >= threshold {
			hitMap[memID] = score
			log.Printf("[vector] memory chunk hit: memID=%s, score=%.4f", memID, score)
		} else {
			nearMiss = append(nearMiss, NearMissItem{ID: memID, Text: memID, VectorScore: score, Reason: "below_threshold"})
		}
	}

	// 2. 回退路径：全量 memory 嵌入
	if len(hitMap) < limit {
		fullResults := s.vectorStore.Search(queryVec, limit*2, func(e Entry) bool {
			return e.Metadata["type"] == "mem" && e.Metadata["user_id"] == userID
		})
		for _, res := range fullResults {
			if !strings.HasPrefix(res.ID, "mem:") {
				nearMiss = append(nearMiss, NearMissItem{ID: res.ID, Text: res.ID, VectorScore: res.Score, Reason: "wrong_prefix"})
				continue
			}
			memID := strings.TrimPrefix(res.ID, "mem:")
			if res.Score < threshold {
				nearMiss = append(nearMiss, NearMissItem{ID: memID, Text: memID, VectorScore: res.Score, Reason: "below_threshold"})
				continue
			}
			if _, exists := hitMap[memID]; !exists {
				hitMap[memID] = res.Score
				if len(hitMap) >= limit {
					break
				}
			}
		}
	}

	if len(hitMap) == 0 {
		return nil, nearMiss, nil
	}

	// 3. 转成有序切片
	type hit struct {
		id    string
		score float64
	}
	hits := make([]hit, 0, len(hitMap))
	for id, score := range hitMap {
		hits = append(hits, hit{id: id, score: score})
	}
	for i := 0; i < len(hits); i++ {
		for j := i + 1; j < len(hits); j++ {
			if hits[j].score > hits[i].score {
				hits[i], hits[j] = hits[j], hits[i]
			}
		}
	}
	if len(hits) > limit {
		hits = hits[:limit]
	}

	memIDs := make([]string, len(hits))
	scoreMap := make(map[string]float64, len(hits))
	for i, h := range hits {
		memIDs[i] = h.id
		scoreMap[h.id] = h.score
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

	// 4. 加载匹配的 chunk 片段
	chunks, _ := s.chunkRepo.BatchGetByParentIDs(ctx, "memory", memIDs)
	chunkByParent := make(map[string][]model.EmbeddingChunk)
	for _, c := range chunks {
		chunkByParent[c.ParentID] = append(chunkByParent[c.ParentID], c)
	}

	// 5. 评分与格式化
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
		topicPart := ""
		if m.Topic != "" {
			topicPart = fmt.Sprintf("[%s] ", m.Topic)
		}
		text := fmt.Sprintf("- [%s] %s%s (已唤起%d次，最近唤起 %s, 相关度 %.2f)\n",
			m.CreatedAt.In(config.BJ).Format("2006-01-02"),
			topicPart,
			m.MemoryValue,
			m.ConfirmCount,
			m.LastConfirmedAt.In(config.BJ).Format("01-02"),
			sc.FinalScore)
		if matchedChunks := chunkByParent[m.ID]; len(matchedChunks) > 0 {
			text += formatChunkSnippets(matchedChunks)
		}
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
		log.Printf("[vector] memory warmup: skipped (no config)")
		return
	}
	log.Printf("[vector] memory warmup: loading existing memories...")

	const pageSize = 100
	loaded, embedded, chunked := 0, 0, 0

	// 加载持久化的全量嵌入
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

	// 补建全量嵌入 + chunk
	for offset := 0; ; offset += pageSize {
		memories, err := s.memRepo.ListAllUsersMemoriesPaginated(ctx, pageSize, offset)
		if err != nil {
			log.Printf("[vector] memory warmup: list memories error: %v", err)
			break
		}
		if len(memories) == 0 {
			break
		}

		memIDs := make([]string, len(memories))
		for i, m := range memories {
			memIDs[i] = m.ID
		}

		existingChunks, _ := s.chunkRepo.BatchGetByParentIDs(ctx, "memory", memIDs)
		hasChunks := make(map[string]bool)
		for _, c := range existingChunks {
			hasChunks[c.ParentID] = true
		}

		for _, m := range memories {
			existingEmb, err := s.embeddingRepo.GetByEntity(ctx, model.EntityTypeMemory, m.ID)
			if err != nil {
				log.Printf("[vector] mem warmup: GetByEntity error for %s: %v", m.ID, err)
			}
			if existingEmb == nil || existingEmb.Embedding == nil {
				vec, err := embedder.Embed(ctx, m.MemoryValue)
				if err != nil {
					log.Printf("[vector] memory warmup: embed %s error: %v", m.ID[:8], err)
					continue
				}
				s.vectorStore.Upsert("mem:"+m.ID, vec, map[string]string{"type": "mem", "user_id": m.UserID})
				embStr := llm.EmbeddingToString(vec)
				s.embeddingRepo.Upsert(ctx, &model.Embedding{
					ID: m.ID, EntityType: model.EntityTypeMemory, EntityID: m.ID,
					UserID: m.UserID, Model: embedder.ModelName(), Embedding: &embStr,
				})
				embedded++
			}

			if !hasChunks[m.ID] && s.chunkBuilder != nil {
				if cnt, err := s.chunkBuilder.BuildChunks(ctx, m.UserID, "memory", m.ID, m.MemoryValue); err != nil {
					log.Printf("[vector] memory warmup: chunk build %s error: %v", m.ID[:8], err)
				} else if cnt > 0 {
					chunked++
				}
			}
		}
		if len(memories) < pageSize {
			break
		}
	}

	log.Printf("[vector] memory warmup: %d from persisted, %d new, %d chunked, total=%d in store",
		loaded, embedded, chunked, s.vectorStore.Size())
}
