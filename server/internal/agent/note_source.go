package agent

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
	vectorStore   *VectorStore
	noteRepo      *service.NoteRepo
	embeddingRepo *service.EmbeddingRepo
}

func NewNoteSource(vs *VectorStore, noteRepo *service.NoteRepo, embeddingRepo *service.EmbeddingRepo) *NoteSource {
	return &NoteSource{vectorStore: vs, noteRepo: noteRepo, embeddingRepo: embeddingRepo}
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
		text := fmt.Sprintf("- [%s] %s\n", n.Topic, n.Content)
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
	if s.vectorStore == nil || embedder == nil || s.embeddingRepo == nil {
		log.Printf("[vector] note warmup: skipped (no config)")
		return
	}
	log.Printf("[vector] note warmup: loading existing notes...")

	const pageSize = 100
	loaded, embedded := 0, 0

	// 先从 embeddings 表加载已有的向量
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

	// 再检查是否有笔记没有向量，需要新生成
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
			// 检查是否已有向量
			existingEmb, err := s.embeddingRepo.GetByEntity(ctx, model.EntityTypeNote, n.ID)
			if err != nil {
				log.Printf("[vector] note warmup: GetByEntity error for %s: %v", n.ID, err)
			}
			if existingEmb != nil && existingEmb.Embedding != nil {
				continue // 已有，跳过
			}
			// 需要生成新向量
			vec, err := embedder.Embed(ctx, n.Content)
			if err != nil {
				log.Printf("[vector] note warmup: embed %s error: %v", n.ID[:8], err)
				continue
			}
			s.vectorStore.Upsert("note:"+n.ID, vec, map[string]string{"type": "note", "user_id": n.UserID})
			embStr := llm.EmbeddingToString(vec)
			err = s.embeddingRepo.Upsert(ctx, &model.Embedding{
				ID:         n.ID, // 使用笔记 ID 作为 embedding ID
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
