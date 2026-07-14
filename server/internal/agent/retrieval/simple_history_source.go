package retrieval

import (
	"context"
	"log"
	"strings"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/service"
)

// SimpleHistorySource — 用户消息语义检索源，仅返回用户自己发送的消息，不做对话轮展开。
// 与 HistorySource 的区别：不做 round 扩展、不取 assistant 消息、不做时间衰减。
type SimpleHistorySource struct {
	vectorStore VectorStore
	msgRepo     *service.MessageRepo
}

func NewSimpleHistorySource(vs VectorStore, msgRepo *service.MessageRepo) *SimpleHistorySource {
	return &SimpleHistorySource{vectorStore: vs, msgRepo: msgRepo}
}

func (s *SimpleHistorySource) Name() string { return "simple_history" }

func (s *SimpleHistorySource) Fetch(ctx context.Context, opts SearchOptions) ([]ContextPacket, []NearMissItem, error) {
	threshold := opts.History.Threshold
	limit := opts.History.Limit
	if threshold <= 0 {
		threshold = 0.55
	}
	if limit <= 0 {
		limit = 5
	}

	results := s.vectorStore.Search(opts.Embedding, limit*2, func(e Entry) bool {
		return e.Metadata["type"] == "msg" && e.Metadata["user_id"] == opts.UserID
	})
	if len(results) == 0 {
		return nil, nil, nil
	}

	type scoredHit struct {
		id    string
		score float64
	}
	var hits []scoredHit
	var nearMiss []NearMissItem
	for _, r := range results {
		if r.Score < threshold {
			nearMiss = append(nearMiss, NearMissItem{ID: r.ID, VectorScore: r.Score, Reason: "below_threshold"})
			continue
		}
		msgID := strings.TrimPrefix(r.ID, "msg:")
		if msgID == "" || msgID == r.ID {
			nearMiss = append(nearMiss, NearMissItem{ID: r.ID, VectorScore: r.Score, Reason: "wrong_prefix"})
			continue
		}
		if opts.ExcludeMsgIDs != nil && opts.ExcludeMsgIDs[msgID] {
			nearMiss = append(nearMiss, NearMissItem{ID: msgID, VectorScore: r.Score, Reason: "already_in_context"})
			continue
		}
		hits = append(hits, scoredHit{id: msgID, score: r.Score})
	}
	if len(hits) == 0 {
		return nil, nearMiss, nil
	}

	if len(hits) > limit {
		for _, h := range hits[limit:] {
			nearMiss = append(nearMiss, NearMissItem{ID: h.id, VectorScore: h.score, Reason: "over_limit"})
		}
		hits = hits[:limit]
	}

	ids := make([]string, len(hits))
	scoreMap := make(map[string]float64, len(hits))
	for i, h := range hits {
		ids[i] = h.id
		scoreMap[h.id] = h.score
	}

	msgs, err := s.msgRepo.GetByIDs(ctx, ids, opts.UserID)
	if err != nil {
		log.Printf("[simple_history] GetByIDs error: %v", err)
		return nil, nearMiss, nil
	}

	packets := make([]ContextPacket, 0, len(msgs))
	for _, m := range msgs {
		if m.Role != "user" {
			continue
		}
		packets = append(packets, ContextPacket{
			ID:    m.ID,
			Type:  "history",
			Score: scoreMap[m.ID],
			Meta: MessageMeta{
				Content:        m.Content,
				CreatedAt:      m.CreatedAt,
				ConversationID: m.ConversationID,
			},
		})
	}
	return packets, nearMiss, nil
}

func (s *SimpleHistorySource) Warmup(_ context.Context, _ *llm.Embedder) {
	// 向量已由 HistorySource.Warmup 负责加载到 store，此处无需重复。
}
