package retrieval

import (
	"context"
	"fmt"
	"log"
	"sort"
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
	decayer       *TimeDecay
}

func NewHistorySource(vs VectorStore, msgRepo *service.MessageRepo, embeddingRepo *service.EmbeddingRepo, decayer *TimeDecay) *HistorySource {
	return &HistorySource{vectorStore: vs, msgRepo: msgRepo, embeddingRepo: embeddingRepo, decayer: decayer}
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
	msg       *model.Message
	vecScore  float64
	freshness float64
}

// filterHits 从向量搜索结果中过滤出有效的历史消息命中，不按阈值过滤，阈值延迟到 Fetch 中与新鲜度组合
func (s *HistorySource) filterHits(ctx context.Context, queryVec []float64, userID string,
	excludeMsgIDs map[string]bool) ([]historyHit, []NearMissItem, error) {

	results := s.vectorStore.Search(queryVec, 20, func(e Entry) bool {
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
		freshness := s.decayer.Freshness(msg.CreatedAt)
		hits = append(hits, historyHit{msg: msg, vecScore: res.Score, freshness: freshness})
	}
	return hits, nearMiss, nil
}

// buildPackets 将 historyHit 结果格式化为 ContextPacket（向量分降序）
func buildPackets(hits []historyHit, roundByUserMsgID map[string][]model.Message) ([]ContextPacket, []NearMissItem) {
	var packets []ContextPacket
	var nearMiss []NearMissItem
	for _, h := range hits {
		round, ok := roundByUserMsgID[h.msg.ID]
		if !ok {
			nearMiss = append(nearMiss, NearMissItem{ID: h.msg.ID, Text: h.msg.ID, VectorScore: h.vecScore, Reason: "round_empty"})
			continue
		}
		filtered := FilterMessageRound(round, h.msg.ID)
		if len(filtered) == 0 {
			nearMiss = append(nearMiss, NearMissItem{ID: h.msg.ID, Text: h.msg.ID, VectorScore: h.vecScore, Reason: "round_empty"})
			continue
		}
		text := FormatMessageRound(filtered, h.vecScore)
		packets = append(packets, ContextPacket{
			ID:    h.msg.ID,
			Type:  "history",
			Text:  text,
			Score: h.vecScore,
		})
	}
	return packets, nearMiss
}

// fetchRounds 根据 hits 批量获取完整轮，返回 ID → round 映射
func (s *HistorySource) fetchRounds(ctx context.Context, hits []historyHit) (map[string][]model.Message, error) {
	var ids []string
	for _, h := range hits {
		ids = append(ids, h.msg.ID)
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
	userID := opts.UserID
	threshold := opts.History.Threshold
	limit := opts.History.Limit
	excludeMsgIDs := opts.ExcludeMsgIDs

	hits, nearMiss, err := s.filterHits(ctx, queryVec, userID, excludeMsgIDs)
	if err != nil {
		return nil, nil, err
	}
	if len(hits) == 0 {
		return nil, nearMiss, nil
	}

	// 用 V×F >= threshold 过滤
	var passed []historyHit
	for _, h := range hits {
		passScore := h.vecScore * h.freshness
		if passScore >= threshold {
			passed = append(passed, h)
		} else {
			nearMiss = append(nearMiss, NearMissItem{
				ID:          h.msg.ID,
				Text:        h.msg.ID,
				VectorScore: h.vecScore,
				Reason:      "below_threshold",
			})
		}
	}
	if len(passed) == 0 {
		return nil, nearMiss, nil
	}

	// 按向量分降序
	sort.Slice(passed, func(i, j int) bool {
		return passed[i].vecScore > passed[j].vecScore
	})

	// 取 top limit
	if len(passed) > limit {
		for _, h := range passed[limit:] {
			nearMiss = append(nearMiss, NearMissItem{
				ID:          h.msg.ID,
				Text:        h.msg.ID,
				VectorScore: h.vecScore,
				Reason:      "over_limit",
			})
		}
		passed = passed[:limit]
	}

	roundByUserMsgID, err := s.fetchRounds(ctx, passed)
	if err != nil {
		return nil, nearMiss, err
	}

	packets, extraNearMiss := buildPackets(passed, roundByUserMsgID)
	nearMiss = append(nearMiss, extraNearMiss...)
	return packets, nearMiss, nil
}
