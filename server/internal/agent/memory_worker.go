package agent

import (
	"context"

	"xhan-helper/internal/llmcost"
)

// MemoryWorker — 记忆提取子任务（PostChatWorker）。
// 包装 MemoryExtractor，从 session 快照的 user 消息中提取长期记忆。
// memoryAutoExtract 关闭时直接跳过（不消费 LLM）。
type MemoryWorker struct {
	extractor         *MemoryExtractor
	memoryAutoExtract bool
}

// MemoryWorkerConfig — MemoryWorker 依赖配置
type MemoryWorkerConfig struct {
	Extractor         *MemoryExtractor
	MemoryAutoExtract bool
}

// NewMemoryWorker 创建记忆提取 worker。
func NewMemoryWorker(cfg *MemoryWorkerConfig) *MemoryWorker {
	return &MemoryWorker{
		extractor:         cfg.Extractor,
		memoryAutoExtract: cfg.MemoryAutoExtract,
	}
}

// Name 实现 PostChatWorker。
func (w *MemoryWorker) Name() string { return "memory" }

// Process 从 session 快照提取记忆。关闭时跳过。
func (w *MemoryWorker) Process(ctx context.Context, sess *SessionSnapshot) error {
	if !w.memoryAutoExtract || w.extractor == nil {
		return nil
	}
	ctx = llmcost.WithScenario(ctx, llmcost.Scenario{
		Category:    llmcost.CategoryPostChat,
		SubCategory: llmcost.SubMemoryExtract,
		UserID:      sess.UserID,
		ConvID:      sess.ConvID,
	})
	_, err := w.extractor.ExtractFromMessages(ctx, sess.UserID, sess.GetUserMessages())
	return err
}
