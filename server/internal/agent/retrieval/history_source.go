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

// filterHits 从向量搜索结果中过滤出有效的历史消息命中，返回 hits 和 nearMiss。
func (s *HistorySource) filterHits(ctx context.Context, queryVec []float64, userID, convID string,
	threshold float64, limit int, excludeMsgIDs map[string]bool) ([]historyHit, []NearMissItem, error) {

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
		if excludeMsgIDs != nil && excludeMsgIDs[msgID] {
			nearMiss = append(nearMiss, NearMissItem{ID: msgID, Text: msgID, VectorScore: res.Score, Reason: "already_in_context"})
			continue
		}
		msg, err := s.msgRepo.GetByIDCols(ctx, msgID)
		if err != nil || msg == nil {
			nearMiss = append(nearMiss, NearMissItem{ID: msgID, Text: msgID, VectorScore: res.Score, Reason: "db_not_found"})
			continue
		}
		if msg.UserID != userID {
			nearMiss = append(nearMiss, NearMissItem{ID: msgID, Text: msgID, VectorScore: res.Score, Reason: "not_owner"})
			continue
		}
		hits = append(hits, historyHit{msg: msg, vecScore: res.Score})
	}
	return hits, nearMiss, nil
}

// buildPackets 将 scorer 结果 + 对应轮裁剪格式化为 ContextPacket（相关度降序）。
// 同时返回无法匹配到轮或裁剪后为空的 nearMiss 条目。
func buildPackets(scored []scoreResult, roundByUserMsgID map[string][]model.Message) ([]ContextPacket, []NearMissItem) {
	var packets []ContextPacket
	var nearMiss []NearMissItem
	for _, sr := range scored {
		round, ok := roundByUserMsgID[sr.ID]
		if !ok {
			nearMiss = append(nearMiss, NearMissItem{ID: sr.ID, Text: sr.ID, VectorScore: sr.VectorScore, Reason: "round_empty"})
			continue
		}
		filtered := FilterMessageRound(round, sr.ID)
		if len(filtered) == 0 {
			nearMiss = append(nearMiss, NearMissItem{ID: sr.ID, Text: sr.ID, VectorScore: sr.VectorScore, Reason: "round_empty"})
			continue
		}
		text := FormatMessageRound(filtered, sr.FinalScore)
		packets = append(packets, ContextPacket{
			ID:           sr.ID,
			Type:         "history",
			Text:         text,
			Score:        sr.VectorScore,
			FinalScore:   sr.FinalScore,
			KeywordScore: sr.KeywordScore,
		})
	}
	return packets, nearMiss
}

// fetchRounds 根据 scorer 结果批量获取完整轮，返回 ID → round 映射。
func (s *HistorySource) fetchRounds(ctx context.Context, scored []scoreResult) (map[string][]model.Message, error) {
	var ids []string
	for _, sr := range scored {
		ids = append(ids, sr.ID)
	}
	rounds, err := s.msgRepo.GetRoundsByUserMsgIDs(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("history fetch rounds: %w", err)
	}
	m := make(map[string][]model.Message, len(rounds))
	for _, r := range rounds {
		if len(r) > 0 && r[0].Role == "user" {
			m[r[0].ID] = r
		}
	}
	return m, nil
}

func (s *HistorySource) Fetch(ctx context.Context, opts SearchOptions) ([]ContextPacket, []NearMissItem, error) {
	queryVec := opts.Embedding
	query := opts.Query
	userID := opts.UserID
	convID := opts.ConversationID
	threshold := opts.HistoryThreshold
	limit := opts.HistoryLimit
	excludeMsgIDs := opts.ExcludeMsgIDs

	hits, nearMiss, err := s.filterHits(ctx, queryVec, userID, convID, threshold, limit, excludeMsgIDs)
	if err != nil {
		return nil, nil, err
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

	roundByUserMsgID, err := s.fetchRounds(ctx, scored)
	if err != nil {
		return nil, nearMiss, err
	}

	packets, extraNearMiss := buildPackets(scored, roundByUserMsgID)
	nearMiss = append(nearMiss, extraNearMiss...)
	return packets, nearMiss, nil
}
