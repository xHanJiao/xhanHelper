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
		log.Printf("[vector] history warmup: skipped (no config)")
		return
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
	threshold float64, limit int) ([]ContextPacket, error) {
	if s.vectorStore == nil || s.msgRepo == nil {
		return nil, nil
	}

	results := s.vectorStore.Search(queryVec, limit*2, func(e Entry) bool {
		return e.Metadata["type"] == "msg" && e.Metadata["user_id"] == userID
	})
	if len(results) == 0 {
		return nil, nil
	}

	var hits []historyHit
	for _, res := range results {
		if res.Score < threshold || !strings.HasPrefix(res.ID, "msg:") {
			continue
		}
		msgID := strings.TrimPrefix(res.ID, "msg:")
		msg, err := s.msgRepo.GetByIDCols(ctx, msgID)
		if err != nil || msg == nil {
			continue
		}
		if msg.ConversationID == convID || msg.UserID != userID {
			continue
		}
		hits = append(hits, historyHit{msg: msg, vecScore: res.Score})
	}

	if len(hits) == 0 {
		return nil, nil
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
			log.Printf("[history] GetMessagesSince error: conv=%s, msg=%s, %v",
				msg.ConversationID[:min(8, len(msg.ConversationID))], msg.ID[:8], err)
			continue
		}
		filtered := FilterMessageRound(roundMsgs, msg.ID)
		if len(filtered) == 0 {
			continue
		}

		packets = append(packets, ContextPacket{
			ID:                     msg.ID,
			Type:                   "history",
			Text:                   "### 历史相关对话\n" + FormatMessageRound(filtered, msg.ConversationID),
			CorrelationCoefficient: sr.VectorScore,
			Score:                  sr.FinalScore,
			KeywordScore:           sr.KeywordScore,
		})
	}

	return packets, nil
}
