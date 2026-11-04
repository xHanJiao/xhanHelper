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
		sess.MemoryPhaseDone = true
		return nil
	}
	ctx = llmcost.WithScenario(ctx, llmcost.Scenario{
		Category:    llmcost.CategoryPostChat,
		SubCategory: llmcost.SubMemoryExtract,
		UserID:      sess.UserID,
		ConvID:      sess.ConvID,
	})

	// 只喂用户消息：assistant 的结论（含工具查库结果）是记忆幻觉的主要来源——
	// 「机票 10-08 已入账」「此前补记属重复记账」都是助手说的，却被记成用户事实。
	// 代价：用户依赖上文的指代（「就按这个来」）会失去上下文，宁可漏记也不让助手的话进来
	// （见 docs/design/memory-quality/design.md 第四轮）。
	cands, err := w.extractor.ExtractCandidates(ctx, sess.UserID, sess.GetUserMessages())
	if err != nil {
		return err
	}
	for _, c := range cands {
		if _, err := w.loop.Decide(ctx, sess.UserID, recordCandidate{
			Summary: c.Summary, Facts: c.Facts,
		}); err != nil {
			return fmt.Errorf("record loop decide for candidate %.40s: %w", c.Summary, err)
		}
	}
	sess.MemoryPhaseDone = true
	return nil
}
