package agent

import (
	"context"
	"fmt"

	"xhan-helper/internal/llmcost"
)

// MemoryWorker — 记忆写入子任务（PostChatWorker）。
// 两段式：抽取（ExtractCandidates 出 N 条候选）→ 逐候选决策循环（RecordDecisionLoop）。
// memoryAutoExtract 关闭时直接跳过（不消费 LLM）。
type MemoryWorker struct {
	extractor         *MemoryExtractor
	loop              *RecordDecisionLoop
	memoryAutoExtract bool
}

// MemoryWorkerConfig — MemoryWorker 依赖配置
type MemoryWorkerConfig struct {
	Extractor         *MemoryExtractor
	Loop              *RecordDecisionLoop
	MemoryAutoExtract bool
}

// NewMemoryWorker 创建记忆写入 worker。
func NewMemoryWorker(cfg *MemoryWorkerConfig) *MemoryWorker {
	return &MemoryWorker{
		extractor:         cfg.Extractor,
		loop:              cfg.Loop,
		memoryAutoExtract: cfg.MemoryAutoExtract,
	}
}

// Name 实现 PostChatWorker。
func (w *MemoryWorker) Name() string { return "memory" }

// Process 抽取候选并逐条决策写入。关闭时跳过。
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

	// 输入完整对话（user + assistant）：assistant 的结论/列表常是被用户确认的记忆来源，
	// 只喂 user 会丢掉一半。role 由 renderDialog 标注，不会被当成用户的话。
	cands, err := w.extractor.ExtractCandidates(ctx, sess.UserID, sess.GetAllMessages())
	if err != nil {
		return err
	}
	for _, c := range cands {
		if _, err := w.loop.Decide(ctx, sess.UserID, recordCandidate{
			Summary: c.Summary, Facts: c.Facts, Topic: c.Topic,
		}); err != nil {
			return fmt.Errorf("record loop decide for candidate %.40s: %w", c.Summary, err)
		}
	}
	return nil
}
