package agent

import (
	"context"
	"log"

	"xhan-helper/internal/llmcost"
)

// FactWorker — 事实抽取子任务（PostChatWorker）。
// 本阶段只做抽取编排：开关判断 → 场景标注 → 调 FactExtractor → 记日志/计数。
// 判重 / 检索 / 决策 / 写入在后续阶段补进 Process。
type FactWorker struct {
	extractor *FactExtractor
	enabled   bool
}

// FactWorkerConfig — FactWorker 依赖配置。
type FactWorkerConfig struct {
	Extractor *FactExtractor
	Enabled   bool
}

// NewFactWorker 创建事实抽取 worker。
func NewFactWorker(cfg *FactWorkerConfig) *FactWorker {
	return &FactWorker{
		extractor: cfg.Extractor,
		enabled:   cfg.Enabled,
	}
}

// Name 实现 PostChatWorker。
func (w *FactWorker) Name() string { return "fact" }

// Process 从 session 快照抽取候选事实。关闭时直接跳过（不消费 LLM）。
// extractor 为必需依赖，不 nil 检查：为 nil 是接线 bug，应炸在明处。
func (w *FactWorker) Process(ctx context.Context, sess *SessionSnapshot) error {
	if !w.enabled {
		return nil
	}
	ctx = llmcost.WithScenario(ctx, llmcost.Scenario{
		Category:    llmcost.CategoryPostChat,
		SubCategory: llmcost.SubFactExtract,
		UserID:      sess.UserID,
		ConvID:      sess.ConvID,
	})
	cands, err := w.extractor.Extract(ctx, sess.GetAllMessages())
	if err != nil {
		return err
	}
	log.Printf("[fact] conv=%.8s extracted %d candidate(s)", sess.ConvID, len(cands))
	return nil
}
