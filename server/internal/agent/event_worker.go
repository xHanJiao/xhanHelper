package agent

import (
	"context"
	"fmt"
	"log"

	"xhan-helper/internal/llmcost"
	"xhan-helper/internal/service"
)

// EventWorker — 事件拼装子任务（PostChatWorker）。
//
// 严格等记忆提取完（MemoryPhaseDone）才开工；输入是 session 起点之后 updated_at 有变化的记忆，
// 不设条数上限（EventAssembler 内部按 batch_size 分批，仍然全部处理）。
// 失败只记日志：记忆照旧留在普通池注入，事件只是增强层。
type EventWorker struct {
	memRepo   *service.UserMemoryRepo
	assembler *EventAssembler
}

// EventWorkerConfig — EventWorker 依赖配置。
type EventWorkerConfig struct {
	MemRepo   *service.UserMemoryRepo
	Assembler *EventAssembler
}

// NewEventWorker 创建事件拼装 worker。必需依赖为 nil 时 panic（接线 bug 暴露在明处）。
func NewEventWorker(cfg *EventWorkerConfig) *EventWorker {
	if cfg.MemRepo == nil || cfg.Assembler == nil {
		panic("event worker: required dependency nil (wiring bug)")
	}
	return &EventWorker{memRepo: cfg.MemRepo, assembler: cfg.Assembler}
}

// Name 实现 PostChatWorker。
func (w *EventWorker) Name() string { return "event" }

// Process 拼装本 session 新增/变更的记忆。
func (w *EventWorker) Process(ctx context.Context, sess *SessionSnapshot) error {
	if !sess.MemoryPhaseDone {
		log.Printf("[event] skip: memory phase not done, conv=%s", shortConvID(sess.ConvID))
		return nil
	}
	if len(sess.msgs) == 0 {
		return nil
	}
	since := sess.msgs[0].CreatedAt
	memories, err := w.memRepo.ListUpdatedSince(ctx, sess.UserID, since)
	if err != nil {
		return fmt.Errorf("list memories updated since %s: %w", since.Format("2006-01-02 15:04:05"), err)
	}
	if len(memories) == 0 {
		return nil
	}

	ctx = llmcost.WithScenario(ctx, llmcost.Scenario{
		Category:    llmcost.CategoryPostChat,
		SubCategory: llmcost.SubEventAssemble,
		UserID:      sess.UserID,
		ConvID:      sess.ConvID,
	})
	stats, err := w.assembler.Assemble(ctx, sess.UserID, memories)
	if err != nil {
		return fmt.Errorf("assemble events: %w", err)
	}
	log.Printf("[event] assemble done: conv=%s memories=%d batches=%d created=%d assigned=%d updated=%d merged=%d rejected=%d",
		shortConvID(sess.ConvID), stats.Memories, stats.Batches, stats.Created,
		stats.Assigned, stats.Updated, stats.Merged, stats.Rejected)
	return nil
}

func shortConvID(id string) string {
	return id[:min(8, len(id))]
}
