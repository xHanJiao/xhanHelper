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

// HistorySource — 跨会话历史消息上下文数据源
type HistorySource struct {
	vectorStore *VectorStore
	msgRepo     *service.MessageRepo
	enabled     bool
}

func NewHistorySource(vs *VectorStore, msgRepo *service.MessageRepo, enabled bool) *HistorySource {
	return &HistorySource{vectorStore: vs, msgRepo: msgRepo, enabled: enabled}
}

func (s *HistorySource) Name() string { return "history" }

func (s *HistorySource) Fetch(ctx context.Context, queryVec []float64, query, userID, convID string,
	threshold float64, limit int) ([]ContextPacket, error) {
	if !s.enabled || s.vectorStore == nil || s.msgRepo == nil {
		return nil, nil
	}

	results := s.vectorStore.Search(queryVec, limit, func(e vectorEntry) bool {
		return e.Metadata["type"] == "msg" &&
			e.Metadata["user_id"] == userID &&
			e.Metadata["conv_id"] != convID
	})
	if len(results) == 0 {
		return nil, nil
	}

	for _, res := range results {
		if res.Score < threshold || !strings.HasPrefix(res.ID, "msg:") {
			continue
		}
		msgID := strings.TrimPrefix(res.ID, "msg:")
		msg, err := s.msgRepo.GetByID(ctx, msgID)
		if err != nil || msg == nil {
			continue
		}
		if msg.ConversationID == convID || msg.UserID != userID {
			continue
		}
		log.Printf("[vector] msg hit: conv=%s, score=%.4f",
			msg.ConversationID[:min(8, len(msg.ConversationID))], res.Score)

		history, err := s.msgRepo.ListByConversation(ctx, msg.ConversationID, 20)
		if err != nil {
			log.Printf("[history] ListByConversation error: %v", err)
			continue
		}
		text := formatHistoryContext(history, msgID)
		if text == "" {
			continue
		}
		return []ContextPacket{{
			ID:                     msgID,
			Text:                   "### 历史相关对话\n" + text,
			CorrelationCoefficient: res.Score,
		}}, nil
	}
	return nil, nil
}

func (s *HistorySource) Warmup(ctx context.Context, embedder *llm.Embedder) {
	// 待后续实现：分页加载 msg embedding 到向量库
}

func formatHistoryContext(history []model.Message, focusID string) string {
	var sb strings.Builder
	found := -1
	for i, m := range history {
		if m.ID == focusID {
			found = i
			break
		}
	}
	if found < 0 {
		return ""
	}
	start := found - 3
	if start < 0 {
		start = 0
	}
	end := found + 4
	if end > len(history) {
		end = len(history)
	}
	for i := start; i < end; i++ {
		m := history[i]
		marker := " "
		if m.ID == focusID {
			marker = "→"
		}
		sb.WriteString(fmt.Sprintf("%s [%s]: %s\n", marker, m.Role, m.Content))
	}
	return sb.String()
}
