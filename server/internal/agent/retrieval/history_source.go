package retrieval

import (
	"context"
	"log"
	"strings"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// HistorySource — 跨会话历史消息上下文数据源
type HistorySource struct {
	vectorStore   VectorStore
	msgRepo       *service.MessageRepo
	embeddingRepo *service.EmbeddingRepo
	ranker        *Ranker
}

func NewHistorySource(vs VectorStore, msgRepo *service.MessageRepo, embeddingRepo *service.EmbeddingRepo, ranker *Ranker) *HistorySource {
	return &HistorySource{vectorStore: vs, msgRepo: msgRepo, embeddingRepo: embeddingRepo, ranker: ranker}
}

func (s *HistorySource) Name() string { return "history" }

func (s *HistorySource) Warmup(ctx context.Context, embedder *llm.Embedder) {
	if s.vectorStore == nil || s.embeddingRepo == nil {
		panic("[vector] history warmup: skipped (no config)")
	}
	log.Printf("[vector] history warmup: loading persisted message embeddings...")

	const pageSize = 100
	loaded := 0

	for offset := 0; ; offset += pageSize {
		embeddings, err := s.embeddingRepo.ListByType(ctx, model.EntityTypeMessage, pageSize, offset)
		if err != nil {
			log.Fatalf("[vector] history warmup: list embeddings error: %v", err)
		}
		if len(embeddings) == 0 {
			break
		}
		for _, emb := range embeddings {
			if emb.Embedding == nil {
				continue
			}
			vec, err := llm.StringToEmbedding(*emb.Embedding)
			if err != nil || len(vec) == 0 {
				continue
			}
			s.vectorStore.Upsert("msg:"+emb.EntityID, vec, map[string]string{
				"type":    "msg",
				"user_id": emb.UserID,
			})
			loaded++
		}
		if len(embeddings) < pageSize {
			break
		}
	}

	log.Printf("[vector] history warmup: loaded=%d, total=%d in store", loaded, s.vectorStore.Size())
}

type historyHit struct {
	msg      *model.Message
	vecScore float64
}

func (s *HistorySource) Fetch(ctx context.Context, queryVec []float64, query, userID, convID string,
	threshold float64, limit int) ([]ContextPacket, []NearMissItem, error) {

	results := s.vectorStore.Search(queryVec, limit*2, func(e Entry) bool {
		return e.Metadata["type"] == "msg" && e.Metadata["user_id"] == userID
	})
	if len(results) == 0 {
		return nil, nil, nil
	}

	var hits []historyHit
	var nearMiss []NearMissItem
	for _, res := range results {
		if !strings.HasPrefix(res.ID, "msg:") {
			nearMiss = append(nearMiss, NearMissItem{ID: res.ID, Text: res.ID, VectorScore: res.Score, Reason: "wrong_prefix"})
			continue
		}
		msgID := strings.TrimPrefix(res.ID, "msg:")
		if res.Score < threshold {
			nearMiss = append(nearMiss, NearMissItem{ID: msgID, Text: msgID, VectorScore: res.Score, Reason: "below_threshold"})
			continue
		}
		msg, err := s.msgRepo.GetByIDCols(ctx, msgID)
		if err != nil || msg == nil {
			nearMiss = append(nearMiss, NearMissItem{ID: msgID, Text: msgID, VectorScore: res.Score, Reason: "db_not_found"})
			continue
		}
		if msg.ConversationID == convID {
			nearMiss = append(nearMiss, NearMissItem{ID: msgID, Text: msgID, VectorScore: res.Score, Reason: "same_conversation"})
			continue
		}
		if msg.UserID != userID {
			nearMiss = append(nearMiss, NearMissItem{ID: msgID, Text: msgID, VectorScore: res.Score, Reason: "not_owner"})
			continue
		}
		hits = append(hits, historyHit{msg: msg, vecScore: res.Score})
	}

	if len(hits) == 0 {
		return nil, nearMiss, nil
	}

	inputs := make([]scoreInput, len(hits))
	for i, h := range hits {
		inputs[i] = scoreInput{
			ID:          h.msg.ID,
			Content:     h.msg.Content,
			CreatedAt:   h.msg.CreatedAt,
			VectorScore: h.vecScore,
		}
	}
	scored := s.ranker.Score(query, inputs)

	hitMap := make(map[string]*model.Message, len(hits))
	for _, h := range hits {
		hitMap[h.msg.ID] = h.msg
	}

	var packets []ContextPacket
	for _, sr := range scored {
		msg := hitMap[sr.ID]
		if msg == nil {
			continue
		}
		roundMsgs, err := s.msgRepo.GetMessagesSince(ctx, msg.ConversationID, msg.CreatedAt, 100)
		if err != nil {
			nearMiss = append(nearMiss, NearMissItem{ID: msg.ID, Text: msg.ID, VectorScore: sr.VectorScore, Reason: "round_fetch_error"})
			continue
		}
		filtered := FilterMessageRound(roundMsgs, msg.ID)
		if len(filtered) == 0 {
			nearMiss = append(nearMiss, NearMissItem{ID: msg.ID, Text: msg.ID, VectorScore: sr.VectorScore, Reason: "round_empty"})
			continue
		}

		packets = append(packets, ContextPacket{
			ID:           msg.ID,
			Type:         "history",
			Text:         FormatMessageRound(filtered, msg.ConversationID),
			Score:        sr.VectorScore,
			FinalScore:   sr.FinalScore,
			KeywordScore: sr.KeywordScore,
		})
	}

	return packets, nearMiss, nil
}
