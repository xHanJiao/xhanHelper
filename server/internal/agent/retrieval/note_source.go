package retrieval

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"

	"xhan-helper/internal/app"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// NoteSource — 笔记上下文数据源
type NoteSource struct {
	vectorStore   VectorStore
	noteRepo      *service.NoteRepo
	embeddingRepo *service.EmbeddingRepo
	chunkRepo     *service.EmbeddingChunkRepo
	chunkBuilder  app.ChunkEmbedder
	decayer       *TimeDecay
}

func NewNoteSource(vs VectorStore, noteRepo *service.NoteRepo, embeddingRepo *service.EmbeddingRepo,
	chunkRepo *service.EmbeddingChunkRepo, chunkBuilder app.ChunkEmbedder, decayer *TimeDecay) *NoteSource {
	if decayer == nil {
		decayer = NewTimeDecay(0)
	}
	return &NoteSource{
		vectorStore: vs, noteRepo: noteRepo, embeddingRepo: embeddingRepo,
		chunkRepo: chunkRepo, chunkBuilder: chunkBuilder, decayer: decayer,
	}
}

func (s *NoteSource) Name() string { return "note" }

func (s *NoteSource) Fetch(ctx context.Context, opts SearchOptions) ([]ContextPacket, []NearMissItem, error) {
	queryVec := opts.Embedding
	userID := opts.UserID
	threshold := opts.Note.Threshold
	limit := opts.Note.Limit

	var nearMiss []NearMissItem

	// 1. Chunk 级向量检索，按 noteID 去重取最高分
	chunkCandidates := make(map[string]float64) // noteID → best chunk score

	chunkResults := s.vectorStore.Search(queryVec, limit*8, func(e Entry) bool {
		return e.Metadata["type"] == "chunk" &&
			e.Metadata["parent_type"] == "note" &&
			e.Metadata["user_id"] == userID
	})
	for _, res := range chunkResults {
		noteID := parseChunkParentID(res.ID)
		if noteID == "" {
			nearMiss = append(nearMiss, NearMissItem{ID: res.ID, Text: res.ID, VectorScore: res.Score, Reason: "bad_chunk_id"})
			continue
		}
		if old, ok := chunkCandidates[noteID]; !ok || res.Score > old {
			chunkCandidates[noteID] = res.Score
		}
	}

	// 2. 全量 note 嵌入搜索，排除已在 chunk 中命中的
	fullCandidates := make(map[string]float64)
	fullResults := s.vectorStore.Search(queryVec, limit*2, func(e Entry) bool {
		return e.Metadata["type"] == "note" && e.Metadata["user_id"] == userID
	})
	for _, res := range fullResults {
		if !strings.HasPrefix(res.ID, "note:") {
			nearMiss = append(nearMiss, NearMissItem{ID: res.ID, Text: res.ID, VectorScore: res.Score, Reason: "wrong_prefix"})
			continue
		}
		noteID := strings.TrimPrefix(res.ID, "note:")
		if _, exists := chunkCandidates[noteID]; exists {
			continue
		}
		if _, exists := fullCandidates[noteID]; !exists {
			fullCandidates[noteID] = res.Score
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

	// 4. 加载所有 note
	noteIDs := make([]string, 0, len(chunkHits)+len(fullHits))
	scoreMap := make(map[string]float64, len(chunkHits)+len(fullHits))
	fromChunk := make(map[string]bool, len(chunkHits))
	for _, h := range chunkHits {
		noteIDs = append(noteIDs, h.id)
		scoreMap[h.id] = h.score
		fromChunk[h.id] = true
	}
	for _, h := range fullHits {
		noteIDs = append(noteIDs, h.id)
		scoreMap[h.id] = h.score
	}

	notes, err := s.noteRepo.GetByIDs(ctx, noteIDs)
	if err != nil {
		return nil, nil, err
	}
	if len(notes) == 0 {
		return nil, nil, fmt.Errorf("all hit note ids not found, ids: %v", noteIDs)
	}

	// 5. 新鲜度过滤：V×F >= threshold，分组记分
	type scoredHit struct {
		note     model.Note
		vecScore float64
	}
	var chunkScored []scoredHit
	var fullScored []scoredHit
	for _, n := range notes {
		vs := scoreMap[n.ID]
		fresh := s.decayer.Freshness(n.CreatedAt)
		if vs*fresh >= threshold {
			sh := scoredHit{note: n, vecScore: vs}
			if fromChunk[n.ID] {
				chunkScored = append(chunkScored, sh)
			} else {
				fullScored = append(fullScored, sh)
			}
		} else {
			nearMiss = append(nearMiss, NearMissItem{
				ID: n.ID, Text: n.Content, VectorScore: vs,
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
				ID:          s.note.ID,
				Text:        s.note.Content,
				VectorScore: s.vecScore,
				Reason:      "over_limit",
			})
		}
		scored = scored[:limit]
	}

	packets := make([]ContextPacket, 0, len(scored))
	for _, sc := range scored {
		n := sc.note
		text := fmt.Sprintf("- [%s] %s (相关度 %.2f)\n", n.Topic, n.Content, sc.vecScore)
		packets = append(packets, ContextPacket{
			ID:    sc.note.ID,
			Type:  "note",
			Text:  text,
			Score: sc.vecScore,
		})
	}
	return packets, nearMiss, nil
}

func (s *NoteSource) Warmup(ctx context.Context, embedder *llm.Embedder) {
	if s.vectorStore == nil || embedder == nil || s.embeddingRepo == nil {
		log.Printf("[vector] note warmup: skipped (no config)")
		return
	}
	log.Printf("[vector] note warmup: loading existing notes...")

	const pageSize = 100
	loaded, embedded, chunked := 0, 0, 0

	// 加载持久化的全量嵌入
	for offset := 0; ; offset += pageSize {
		embeddings, err := s.embeddingRepo.ListByType(ctx, model.EntityTypeNote, pageSize, offset)
		if err != nil {
			log.Printf("[vector] note warmup: list embeddings error: %v", err)
			break
		}
		if len(embeddings) == 0 {
			break
		}
		for _, emb := range embeddings {
			if emb.Embedding != nil {
				if vec, err := llm.StringToEmbedding(*emb.Embedding); err == nil && len(vec) > 0 {
					s.vectorStore.Upsert("note:"+emb.EntityID, vec, map[string]string{"type": "note", "user_id": emb.UserID})
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
		notes, err := s.noteRepo.ListAllPaginated(ctx, pageSize, offset)
		if err != nil {
			log.Printf("[vector] note warmup: list notes error: %v", err)
			break
		}
		if len(notes) == 0 {
			break
		}

		noteIDs := make([]string, len(notes))
		for i, n := range notes {
			noteIDs[i] = n.ID
		}

		existingChunks, _ := s.chunkRepo.BatchGetByParentIDs(ctx, "note", noteIDs)
		hasChunks := make(map[string]bool)
		for _, c := range existingChunks {
			hasChunks[c.ParentID] = true
		}

		for _, n := range notes {
			existingEmb, err := s.embeddingRepo.GetByEntity(ctx, model.EntityTypeNote, n.ID)
			if err != nil {
				log.Printf("[vector] note warmup: GetByEntity error for %s: %v", n.ID, err)
			}
			if existingEmb == nil || existingEmb.Embedding == nil {
				vec, err := embedder.Embed(ctx, n.Content)
				if err != nil {
					log.Printf("[vector] note warmup: embed %s error: %v", n.ID[:8], err)
					continue
				}
				s.vectorStore.Upsert("note:"+n.ID, vec, map[string]string{"type": "note", "user_id": n.UserID})
				embStr := llm.EmbeddingToString(vec)
				s.embeddingRepo.Upsert(ctx, &model.Embedding{
					ID: n.ID, EntityType: model.EntityTypeNote, EntityID: n.ID,
					UserID: n.UserID, Model: embedder.ModelName(), Embedding: &embStr,
				})
				embedded++
			}

			if !hasChunks[n.ID] && s.chunkBuilder != nil {
				if cnt, err := s.chunkBuilder.BuildChunks(ctx, n.UserID, "note", n.ID, n.Content); err != nil {
					log.Printf("[vector] note warmup: chunk build %s error: %v", n.ID[:8], err)
				} else if cnt > 0 {
					chunked++
				}
			}
		}
		if len(notes) < pageSize {
			break
		}
	}

	log.Printf("[vector] note warmup: %d from persisted, %d new, %d chunked, total=%d in store",
		loaded, embedded, chunked, s.vectorStore.Size())
}

// parseChunkParentID 从 chunk ID 提取 parent note/memory ID
// chunk ID 格式: chunk:<parentType>:<parentID>/<index>
func parseChunkParentID(chunkID string) string {
	rest := chunkID
	if idx := strings.Index(chunkID, ":"); idx > 0 {
		rest = chunkID[idx+1:]
	}
	if idx := strings.Index(rest, ":"); idx > 0 {
		rest = rest[idx+1:]
	}
	if idx := strings.LastIndex(rest, "/"); idx > 0 {
		return rest[:idx]
	}
	return rest
}
