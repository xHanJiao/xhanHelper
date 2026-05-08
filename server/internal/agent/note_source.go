package agent

import (
	"context"
	"fmt"
	"log"
	"strings"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/service"
)

// NoteSource — 笔记上下文数据源
type NoteSource struct {
	vectorStore *VectorStore
	noteRepo    *service.NoteRepo
}

func NewNoteSource(vs *VectorStore, noteRepo *service.NoteRepo) *NoteSource {
	return &NoteSource{vectorStore: vs, noteRepo: noteRepo}
}

func (s *NoteSource) Name() string { return "note" }

func (s *NoteSource) Fetch(ctx context.Context, queryVec []float64, userID, convID string,
	threshold float64, limit int) ([]ContextPacket, error) {
	if s.vectorStore == nil || s.noteRepo == nil {
		return nil, nil
	}

	results := s.vectorStore.Search(queryVec, limit, func(e vectorEntry) bool {
		return e.Metadata["type"] == "note"
	})
	if len(results) == 0 {
		return nil, nil
	}

	var noteIDs []string
	for _, res := range results {
		if res.Score < threshold || !strings.HasPrefix(res.ID, "note:") {
			continue
		}
		noteIDs = append(noteIDs, strings.TrimPrefix(res.ID, "note:"))
		log.Printf("[vector] note hit: id=%s, score=%.4f", res.ID, res.Score)
	}
	if len(noteIDs) == 0 {
		return nil, nil
	}

	notes, err := s.noteRepo.GetByIDs(ctx, noteIDs)
	if err != nil || len(notes) == 0 {
		return nil, nil
	}

	packets := make([]ContextPacket, 0, len(notes))
	for i, n := range notes {
		text := fmt.Sprintf("- [%s] %s\n", n.Topic, n.Content[:min(200, len(n.Content))])
		if i == 0 {
			text = "===== 相关笔记 =====\n" + text
		}
		packets = append(packets, ContextPacket{
			ID:   n.ID,
			Text: text,
		})
	}
	return packets, nil
}

func (s *NoteSource) Warmup(ctx context.Context, embedder *llm.Embedder) {
	if s.vectorStore == nil || embedder == nil || s.noteRepo == nil {
		log.Printf("[vector] note warmup: skipped (no config)")
		return
	}
	log.Printf("[vector] note warmup: loading existing notes...")

	const pageSize = 100
	loaded, embedded := 0, 0
	for offset := 0; ; offset += pageSize {
		notes, err := s.noteRepo.ListAllPaginated(ctx, pageSize, offset)
		if err != nil {
			log.Printf("[vector] note warmup: list error: %v", err)
			return
		}
		if len(notes) == 0 {
			break
		}
		for _, n := range notes {
			if n.Embedding != nil {
				if vec, err := llm.StringToEmbedding(*n.Embedding); err == nil && len(vec) > 0 {
					s.vectorStore.Upsert("note:"+n.ID, vec, map[string]string{"type": "note", "user_id": n.UserID})
					loaded++
					continue
				}
			}
			vec, err := embedder.Embed(ctx, n.Content)
			if err != nil {
				log.Printf("[vector] note warmup: embed %s error: %v", n.ID[:8], err)
				continue
			}
			s.vectorStore.Upsert("note:"+n.ID, vec, map[string]string{"type": "note", "user_id": n.UserID})
			embStr := llm.EmbeddingToString(vec)
			s.noteRepo.UpdateEmbedding(ctx, n.ID, &embStr)
			embedded++
		}
		if len(notes) < pageSize {
			break
		}
	}

	log.Printf("[vector] note warmup: %d from persisted, %d new, total=%d in store",
		loaded, embedded, s.vectorStore.Size())
}
