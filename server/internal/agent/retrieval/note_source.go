package retrieval

import (
	"context"
	"fmt"
	"log"
	"strings"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// NoteSource — 笔记上下文数据源
type NoteSource struct {
	vectorStore   VectorStore
	noteRepo      *service.NoteRepo
	embeddingRepo *service.EmbeddingRepo
	ranker        *Ranker
}

func NewNoteSource(vs VectorStore, noteRepo *service.NoteRepo, embeddingRepo *service.EmbeddingRepo, ranker *Ranker) *NoteSource {
	if ranker == nil {
		ranker = DefaultRanker()
	}
	return &NoteSource{vectorStore: vs, noteRepo: noteRepo, embeddingRepo: embeddingRepo, ranker: ranker}
}

func (s *NoteSource) Name() string { return "note" }

func (s *NoteSource) Fetch(ctx context.Context, queryVec []float64, query, userID, convID string,
	threshold float64, limit int) ([]ContextPacket, error) {
	if s.vectorStore == nil || s.noteRepo == nil {
		return nil, nil
	}

	results := s.vectorStore.Search(queryVec, limit, func(e Entry) bool {
		return e.Metadata["type"] == "note"
	})
	if len(results) == 0 {
		return nil, nil
	}

	type hit struct {
		id    string
		score float64
	}
	var hits []hit
	for _, res := range results {
		if res.Score < threshold || !strings.HasPrefix(res.ID, "note:") {
			continue
		}
		id := strings.TrimPrefix(res.ID, "note:")
		hits = append(hits, hit{id: id, score: res.Score})
		log.Printf("[vector] note hit: id=%s, score=%.4f", res.ID, res.Score)
	}
	if len(hits) == 0 {
		return nil, nil
	}

	noteIDs := make([]string, len(hits))
	scoreMap := make(map[string]float64, len(hits))
	for i, h := range hits {
		noteIDs[i] = h.id
		scoreMap[h.id] = h.score
	}

	notes, err := s.noteRepo.GetByIDs(ctx, noteIDs)
	if err != nil || len(notes) == 0 {
		return nil, nil
	}

	inputs := make([]scoreInput, len(notes))
	noteByID := make(map[string]model.Note, len(notes))
	for i, n := range notes {
		inputs[i] = scoreInput{ID: n.ID, Content: n.Content, CreatedAt: n.CreatedAt, VectorScore: scoreMap[n.ID]}
		noteByID[n.ID] = n
	}
	// decayRate=0：笔记不做时间衰减
	scored := s.ranker.Score(query, inputs)

	packets := make([]ContextPacket, 0, len(scored))
	for i, sc := range scored {
		n := noteByID[sc.ID]
		text := fmt.Sprintf("- [%s] %s\n", n.Topic, n.Content)
		if i == 0 {
			text = "### 相关笔记\n" + text
		}
		packets = append(packets, ContextPacket{
			ID:           sc.ID,
			Text:         text,
			Score:        sc.VectorScore,
			KeywordScore: sc.KeywordScore,
		})
	}
	return packets, nil
}

func (s *NoteSource) Warmup(ctx context.Context, embedder *llm.Embedder) {
	if s.vectorStore == nil || embedder == nil || s.embeddingRepo == nil {
		log.Printf("[vector] note warmup: skipped (no config)")
		return
	}
	log.Printf("[vector] note warmup: loading existing notes...")

	const pageSize = 100
	loaded, embedded := 0, 0

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

	for offset := 0; ; offset += pageSize {
		notes, err := s.noteRepo.ListAllPaginated(ctx, pageSize, offset)
		if err != nil {
			log.Printf("[vector] note warmup: list notes error: %v", err)
			break
		}
		if len(notes) == 0 {
			break
		}
		for _, n := range notes {
			existingEmb, err := s.embeddingRepo.GetByEntity(ctx, model.EntityTypeNote, n.ID)
			if err != nil {
				log.Printf("[vector] note warmup: GetByEntity error for %s: %v", n.ID, err)
			}
			if existingEmb != nil && existingEmb.Embedding != nil {
				continue
			}
			vec, err := embedder.Embed(ctx, n.Content)
			if err != nil {
				log.Printf("[vector] note warmup: embed %s error: %v", n.ID[:8], err)
				continue
			}
			s.vectorStore.Upsert("note:"+n.ID, vec, map[string]string{"type": "note", "user_id": n.UserID})
			embStr := llm.EmbeddingToString(vec)
			err = s.embeddingRepo.Upsert(ctx, &model.Embedding{
				ID:         n.ID,
				EntityType: model.EntityTypeNote,
				EntityID:   n.ID,
				UserID:     n.UserID,
				Model:      embedder.ModelName(),
				Embedding:  &embStr,
			})
			if err != nil {
				log.Printf("[vector] note warmup: save embedding %s error: %v", n.ID[:8], err)
			}
			embedded++
		}
		if len(notes) < pageSize {
			break
		}
	}

	log.Printf("[vector] note warmup: %d from persisted, %d new, total=%d in store",
		loaded, embedded, s.vectorStore.Size())
}
