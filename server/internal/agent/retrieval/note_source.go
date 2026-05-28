package retrieval

import (
	"context"
	"fmt"
	"log"
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
	ranker        *Ranker
}

func NewNoteSource(vs VectorStore, noteRepo *service.NoteRepo, embeddingRepo *service.EmbeddingRepo,
	chunkRepo *service.EmbeddingChunkRepo, chunkBuilder app.ChunkEmbedder, ranker *Ranker) *NoteSource {
	if ranker == nil {
		ranker = DefaultRanker()
	}
	return &NoteSource{
		vectorStore: vs, noteRepo: noteRepo, embeddingRepo: embeddingRepo,
		chunkRepo: chunkRepo, chunkBuilder: chunkBuilder, ranker: ranker,
	}
}

func (s *NoteSource) Name() string { return "note" }

func (s *NoteSource) Fetch(ctx context.Context, queryVec []float64, query, userID, convID string,
	threshold float64, limit int) ([]ContextPacket, []NearMissItem, error) {

	var nearMiss []NearMissItem

	// 1. 主路径：chunk 级向量检索，先按 noteID 去重取最高分
	candidates := make(map[string]float64) // noteID → best chunk score

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
		if old, ok := candidates[noteID]; !ok || res.Score > old {
			candidates[noteID] = res.Score
		}
	}

	// 对去重后的候选按阈值分流
	hitMap := make(map[string]float64)
	for noteID, score := range candidates {
		if score >= threshold {
			hitMap[noteID] = score
			log.Printf("[vector] note chunk hit: noteID=%s, score=%.4f", noteID, score)
		} else {
			nearMiss = append(nearMiss, NearMissItem{ID: noteID, Text: noteID, VectorScore: score, Reason: "below_threshold"})
		}
	}

	// 2. 回退路径：全量 note 嵌入（chunk 未覆盖到的）
	if len(hitMap) < limit {
		fullResults := s.vectorStore.Search(queryVec, limit*2, func(e Entry) bool {
			return e.Metadata["type"] == "note" && e.Metadata["user_id"] == userID
		})
		for _, res := range fullResults {
			if !strings.HasPrefix(res.ID, "note:") {
				nearMiss = append(nearMiss, NearMissItem{ID: res.ID, Text: res.ID, VectorScore: res.Score, Reason: "wrong_prefix"})
				continue
			}
			noteID := strings.TrimPrefix(res.ID, "note:")
			if res.Score < threshold {
				nearMiss = append(nearMiss, NearMissItem{ID: noteID, Text: noteID, VectorScore: res.Score, Reason: "below_threshold"})
				continue
			}
			if _, exists := hitMap[noteID]; !exists {
				hitMap[noteID] = res.Score
				if len(hitMap) >= limit {
					break
				}
			}
		}
	}

	if len(hitMap) == 0 {
		return nil, nearMiss, nil
	}

	// 3. 转成有序切片（按分数降序）
	type hit struct {
		id    string
		score float64
	}
	hits := make([]hit, 0, len(hitMap))
	for id, score := range hitMap {
		hits = append(hits, hit{id: id, score: score})
	}
	// 简单排序
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

	noteIDs := make([]string, len(hits))
	scoreMap := make(map[string]float64, len(hits))
	for i, h := range hits {
		noteIDs[i] = h.id
		scoreMap[h.id] = h.score
	}

	notes, err := s.noteRepo.GetByIDs(ctx, noteIDs)
	if err != nil {
		return nil, nil, err
	}
	if len(notes) == 0 {
		return nil, nil, fmt.Errorf("all hit note ids not found, ids: %v", noteIDs)
	}

	// 4. 加载匹配的 chunk 片段
	chunks, _ := s.chunkRepo.BatchGetByParentIDs(ctx, "note", noteIDs)
	chunkByParent := make(map[string][]model.EmbeddingChunk)
	for _, c := range chunks {
		chunkByParent[c.ParentID] = append(chunkByParent[c.ParentID], c)
	}

	// 5. 评分与格式化
	inputs := make([]scoreInput, len(notes))
	noteByID := make(map[string]model.Note, len(notes))
	for i, n := range notes {
		inputs[i] = scoreInput{ID: n.ID, Content: n.Content, CreatedAt: n.CreatedAt, VectorScore: scoreMap[n.ID]}
		noteByID[n.ID] = n
	}
	scored := s.ranker.Score(query, inputs)

	packets := make([]ContextPacket, 0, len(scored))
	for _, sc := range scored {
		n := noteByID[sc.ID]
		text := fmt.Sprintf("- [%s] %s (相关度 %.2f)\n", n.Topic, n.Content, sc.FinalScore)
		if matchedChunks := chunkByParent[n.ID]; len(matchedChunks) > 0 {
			text += formatChunkSnippets(matchedChunks)
		}
		packets = append(packets, ContextPacket{
			ID:           sc.ID,
			Type:         "note",
			Text:         text,
			Score:        sc.VectorScore,
			KeywordScore: sc.KeywordScore,
			FinalScore:   sc.FinalScore,
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

		// 批量查已有 chunk
		existingChunks, _ := s.chunkRepo.BatchGetByParentIDs(ctx, "note", noteIDs)
		hasChunks := make(map[string]bool)
		for _, c := range existingChunks {
			hasChunks[c.ParentID] = true
		}

		for _, n := range notes {
			// 补全量嵌入
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

			// 补 chunk
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

// parseChunkParentID 从 chunk ID 提取 parent note ID
// chunk ID 格式: chunk:note:<noteID>/<index>
func parseChunkParentID(chunkID string) string {
	// 去掉 "chunk:" 前缀
	rest := chunkID
	if idx := strings.Index(chunkID, ":"); idx > 0 {
		rest = chunkID[idx+1:]
	}
	// 去掉 parentType
	if idx := strings.Index(rest, ":"); idx > 0 {
		rest = rest[idx+1:]
	}
	// 去掉 /index 后缀
	if idx := strings.LastIndex(rest, "/"); idx > 0 {
		return rest[:idx]
	}
	return rest
}

// formatChunkSnippets 将匹配 chunk 片段格式化为上下文文本
func formatChunkSnippets(chunks []model.EmbeddingChunk) string {
	if len(chunks) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("  匹配片段: ")
	for i, c := range chunks {
		if i > 0 {
			sb.WriteString("; ")
		}
		text := c.Content
		if len(text) > 120 {
			text = text[:120]
		}
		sb.WriteString(text)
	}
	sb.WriteString("\n")
	return sb.String()
}
