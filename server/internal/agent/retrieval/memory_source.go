package retrieval

import (
	"context"
	"fmt"
	"log"
	"sort"
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
	decayer       *TimeDecay
}

func NewMemorySource(vs VectorStore, memRepo *service.UserMemoryRepo, embeddingRepo *service.EmbeddingRepo,
	chunkRepo *service.EmbeddingChunkRepo, chunkBuilder app.ChunkEmbedder, decayer *TimeDecay) *MemorySource {
	if decayer == nil {
		decayer = NewTimeDecay(0)
	}
	return &MemorySource{
		vectorStore: vs, memRepo: memRepo, embeddingRepo: embeddingRepo,
		chunkRepo: chunkRepo, chunkBuilder: chunkBuilder, decayer: decayer,
	}
}

func (s *MemorySource) Name() string { return "memory" }

func (s *MemorySource) Fetch(ctx context.Context, opts SearchOptions) ([]ContextPacket, []NearMissItem, error) {
	queryVec := opts.Embedding
	userID := opts.UserID
	threshold := opts.Memory.Threshold
	limit := opts.Memory.Limit

	var nearMiss []NearMissItem

	// 1. Chunk 级向量检索，按 memID 去重取最高分
	chunkCandidates := make(map[string]float64) // memID → best chunk score

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
		if old, ok := chunkCandidates[memID]; !ok || res.Score > old {
			chunkCandidates[memID] = res.Score
		}
	}

	// 2. 全量 memory 嵌入搜索，排除已在 chunk 中命中的
	fullCandidates := make(map[string]float64)
	fullResults := s.vectorStore.Search(queryVec, limit*2, func(e Entry) bool {
		return e.Metadata["type"] == "mem" && e.Metadata["user_id"] == userID
	})
	for _, res := range fullResults {
		if !strings.HasPrefix(res.ID, "mem:") {
			nearMiss = append(nearMiss, NearMissItem{ID: res.ID, Text: res.ID, VectorScore: res.Score, Reason: "wrong_prefix"})
			continue
		}
		memID := strings.TrimPrefix(res.ID, "mem:")
		if _, exists := chunkCandidates[memID]; exists {
			continue
		}
		if _, exists := fullCandidates[memID]; !exists {
			fullCandidates[memID] = res.Score
		}
	}

	if len(chunkCandidates) == 0 && len(fullCandidates) == 0 {
		return nil, nearMiss, nil
	}

	// 3. 分组排序、裁剪（各组取 top limit*2 进入新鲜度评定）
	type hit struct {
		id    string
		score float64
	}

	chunkHits := make([]hit, 0, len(chunkCandidates))
	for id, score := range chunkCandidates {
		chunkHits = append(chunkHits, hit{id: id, score: score})
	}
	sort.Slice(chunkHits, func(i, j int) bool { return chunkHits[i].score > chunkHits[j].score })
	if len(chunkHits) > limit*2 {
		chunkHits = chunkHits[:limit*2]
	}

	fullHits := make([]hit, 0, len(fullCandidates))
	for id, score := range fullCandidates {
		fullHits = append(fullHits, hit{id: id, score: score})
	}
	sort.Slice(fullHits, func(i, j int) bool { return fullHits[i].score > fullHits[j].score })
	if len(fullHits) > limit*2 {
		fullHits = fullHits[:limit*2]
	}

	// 4. 加载所有 memory
	memIDs := make([]string, 0, len(chunkHits)+len(fullHits))
	scoreMap := make(map[string]float64, len(chunkHits)+len(fullHits))
	fromChunk := make(map[string]bool, len(chunkHits))
	for _, h := range chunkHits {
		memIDs = append(memIDs, h.id)
		scoreMap[h.id] = h.score
		fromChunk[h.id] = true
	}
	for _, h := range fullHits {
		memIDs = append(memIDs, h.id)
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

	// 5. 新鲜度过滤：V×F >= threshold，分组记分
	type scoredHit struct {
		mem      model.UserMemory
		vecScore float64
	}
	var chunkScored []scoredHit
	var fullScored []scoredHit
	for _, m := range memories {
		vs := scoreMap[m.ID]
		fresh := s.decayer.Freshness(m.CreatedAt)
		if vs*fresh >= threshold {
			sh := scoredHit{mem: m, vecScore: vs}
			if fromChunk[m.ID] {
				chunkScored = append(chunkScored, sh)
			} else {
				fullScored = append(fullScored, sh)
			}
		} else {
			nearMiss = append(nearMiss, NearMissItem{
				ID: m.ID, Text: m.MemoryValue, VectorScore: vs,
				Reason: "below_threshold",
			})
		}
	}

	// 组内按向量分降序
	sort.Slice(chunkScored, func(i, j int) bool { return chunkScored[i].vecScore > chunkScored[j].vecScore })
	sort.Slice(fullScored, func(i, j int) bool { return fullScored[i].vecScore > fullScored[j].vecScore })

	// 合并：chunk 组在前
	scored := append(chunkScored, fullScored...)
	if len(scored) > limit {
		for _, s := range scored[limit:] {
			nearMiss = append(nearMiss, NearMissItem{
				ID:          s.mem.ID,
				Text:        s.mem.MemoryValue,
				VectorScore: s.vecScore,
				Reason:      "over_limit",
			})
		}
		scored = scored[:limit]
	}

	packets := make([]ContextPacket, 0, len(scored))
	for _, sc := range scored {
		m := sc.mem
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
			sc.vecScore)
		packets = append(packets, ContextPacket{
			ID:    sc.mem.ID,
			Type:  "memory",
			Text:  text,
			Score: sc.vecScore,
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
