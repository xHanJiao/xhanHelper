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

// Retriever — 向量记忆检索器。负责：
//   - 将用户消息嵌入向量 → 搜索向量库 → 分拣记忆/历史/笔记 → 格式化上下文文本
//   - 启动时 warmup：把 DB 中已有记忆加载到向量库
type Retriever struct {
	vectorStore *VectorStore
	embedder    *llm.Embedder
	memRepo     *service.UserMemoryRepo
	msgRepo     *service.MessageRepo
	noteRepo    *service.NoteRepo
}

// NewRetriever 创建检索器
func NewRetriever(vs *VectorStore, emb *llm.Embedder, memRepo *service.UserMemoryRepo,
	msgRepo *service.MessageRepo, noteRepo *service.NoteRepo) *Retriever {
	return &Retriever{
		vectorStore: vs,
		embedder:    emb,
		memRepo:     memRepo,
		msgRepo:     msgRepo,
		noteRepo:    noteRepo,
	}
}

// Retrieve 检索与用户消息相关的上下文（记忆 + 历史对话 + 笔记）
// 返回拼好的文本，直接注入 system prompt。为空表示无匹配。
func (r *Retriever) Retrieve(ctx context.Context, userMessage, userID, convID string, deepMode bool) string {
	if r.vectorStore == nil || r.embedder == nil {
		return ""
	}

	vec, err := r.embedder.Embed(ctx, userMessage)
	if err != nil {
		log.Printf("[vector] embed user msg error: %v", err)
		return ""
	}

	results := r.vectorStore.Search(vec, 8, func(e vectorEntry) bool {
		if e.Metadata["user_id"] != "" && e.Metadata["user_id"] != userID {
			return false
		}
		if e.Metadata["type"] != "msg" {
			return true
		}
		return e.Metadata["conv_id"] != convID
	})
	// 搜索完成后再写入当前消息 embedding，避免被自己检索到
	go r.saveMsgEmbedding(ctx, userID, convID, vec)

	if len(results) == 0 {
		return ""
	}

	var parts []string

	if memText := r.searchMemories(ctx, results); memText != "" {
		parts = append(parts, memText)
	}
	if msgText := r.searchHistoryMessages(ctx, convID, results); msgText != "" {
		parts = append(parts, msgText)
	}
	if deepMode {
		if noteText := r.searchNotes(ctx, results); noteText != "" {
			parts = append(parts, noteText)
		}
	}

	return strings.Join(parts, "\n")
}

// Warmup 启动时加载已有记忆到向量库
func (r *Retriever) Warmup(ctx context.Context) {
	if r.vectorStore == nil || r.embedder == nil || r.memRepo == nil {
		log.Printf("[vector] warmup: skipped (no config)")
		return
	}
	log.Printf("[vector] warmup: loading existing memories...")

	memories, err := r.memRepo.ListAll(ctx)
	if err != nil {
		log.Printf("[vector] warmup: list memories error: %v", err)
		return
	}

	loaded, embedded := 0, 0
	for _, m := range memories {
		if m.Embedding != nil {
			if vec, err := llm.StringToEmbedding(*m.Embedding); err == nil && len(vec) > 0 {
				r.vectorStore.Upsert("mem:"+m.ID, vec, map[string]string{"type": "mem", "user_id": m.UserID})
				loaded++
				continue
			}
		}
		text := m.MemoryKey + ": " + m.MemoryValue
		vec, err := r.embedder.Embed(ctx, text)
		if err != nil {
			log.Printf("[vector] warmup: embed memory %s error: %v", m.ID[:8], err)
			continue
		}
		r.vectorStore.Upsert("mem:"+m.ID, vec, map[string]string{"type": "mem", "user_id": m.UserID})
		embStr := llm.EmbeddingToString(vec)
		r.memRepo.UpdateEmbedding(ctx, m.ID, &embStr)
		embedded++
	}

	log.Printf("[vector] warmup: %d from persisted embeddings, %d new, total=%d in store",
		loaded, embedded, r.vectorStore.Size())
}

// —— 内部检索路由 ——

func (r *Retriever) searchMemories(ctx context.Context, results []struct {
	ID    string
	Score float64
}) string {
	var memIDs []string
	for _, res := range results {
		if res.Score < 0.5 || !strings.HasPrefix(res.ID, "mem:") {
			continue
		}
		memIDs = append(memIDs, strings.TrimPrefix(res.ID, "mem:"))
		log.Printf("[vector] memory hit: id=%s, score=%.4f", res.ID, res.Score)
	}
	if len(memIDs) == 0 {
		return ""
	}
	memories, err := r.memRepo.GetByIDs(ctx, memIDs)
	if err != nil || len(memories) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, m := range memories {
		sb.WriteString(fmt.Sprintf("- %s: %s\n", m.MemoryKey, m.MemoryValue))
	}
	return "===== 相关记忆 =====\n" + sb.String()
}

func (r *Retriever) searchHistoryMessages(ctx context.Context, currentConvID string, results []struct {
	ID    string
	Score float64
}) string {
	for _, res := range results {
		if res.Score < 0.5 || !strings.HasPrefix(res.ID, "msg:") {
			continue
		}
		msgID := strings.TrimPrefix(res.ID, "msg:")
		msg, err := r.msgRepo.GetByID(ctx, msgID)
		if err != nil || msg == nil {
			continue
		}
		if msg.ConversationID == currentConvID {
			continue
		}
		log.Printf("[vector] msg hit: conv=%s, score=%.4f",
			msg.ConversationID[:min(8, len(msg.ConversationID))], res.Score)

		history, _ := r.msgRepo.ListByConversation(ctx, msg.ConversationID, 20)
		return "===== 历史相关对话 =====\n" + formatHistoryContext(history, msgID)
	}
	return ""
}

func (r *Retriever) searchNotes(ctx context.Context, results []struct {
	ID    string
	Score float64
}) string {
	var noteIDs []string
	for _, res := range results {
		if res.Score < 0.5 || !strings.HasPrefix(res.ID, "note:") {
			continue
		}
		noteIDs = append(noteIDs, strings.TrimPrefix(res.ID, "note:"))
		log.Printf("[vector] note hit: id=%s, score=%.4f", res.ID, res.Score)
	}
	if len(noteIDs) == 0 || r.noteRepo == nil {
		return ""
	}
	notes, err := r.noteRepo.GetByIDs(ctx, noteIDs)
	if err != nil || len(notes) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("===== 相关笔记 =====\n")
	for _, n := range notes {
		sb.WriteString(fmt.Sprintf("- [%s] %s\n", n.Topic, n.Content[:min(200, len(n.Content))]))
	}
	return sb.String()
}

func (r *Retriever) saveMsgEmbedding(ctx context.Context, userID, convID string, vec []float64) {
	if r.msgRepo == nil || convID == "" {
		return
	}
	history, err := r.msgRepo.ListByConversation(ctx, convID, 1)
	if err != nil || len(history) == 0 {
		return
	}
	lastMsg := history[len(history)-1]
	if lastMsg.Role != "user" {
		return
	}
	r.vectorStore.Upsert("msg:"+lastMsg.ID, vec, map[string]string{
		"type":    "msg",
		"conv_id": convID,
		"user_id": userID,
	})
	embStr := llm.EmbeddingToString(vec)
	r.msgRepo.UpdateLastUserEmbedding(ctx, convID, &embStr)
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
		sb.WriteString(fmt.Sprintf("%s [%s]: %s\n", marker, m.Role, m.Content[:min(200, len(m.Content))]))
	}
	return sb.String()
}
