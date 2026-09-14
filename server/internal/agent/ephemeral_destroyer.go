package agent

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"

	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// TaskTypeEphemeralPurge — 无痕会话物理销毁延迟任务类型
const TaskTypeEphemeralPurge = "ephemeral_purge"

const (
	purgeBatchSize  = 300
	purgeBatchPause = 150 * time.Millisecond
)

// EphemeralDestroyer — 无痕会话销毁器（两段式）。
//
// Stage A（BeginDestroy，同步毫秒级）：软删隐藏会话/消息 + 清内存向量 + 副表清理 + 入队 Stage B。
// Stage B（RunPurge，后台分批节流）：物理删除消息/向量，最后删会话行，可幂等续跑。
type EphemeralDestroyer struct {
	convRepo        *service.ConversationRepo
	vectorStore     *VectorStore
	delayedTaskRepo *service.DelayedTaskRepo
}

// NewEphemeralDestroyer 创建销毁器。依赖为 nil 时 panic（接线 bug 炸在明处）。
func NewEphemeralDestroyer(convRepo *service.ConversationRepo, vs *VectorStore, delayedTaskRepo *service.DelayedTaskRepo) *EphemeralDestroyer {
	if convRepo == nil || vs == nil || delayedTaskRepo == nil {
		panic("ephemeral_destroyer: nil dependency (wiring bug)")
	}
	return &EphemeralDestroyer{convRepo: convRepo, vectorStore: vs, delayedTaskRepo: delayedTaskRepo}
}

// IsEphemeral 判断会话是否为无痕会话。
func (d *EphemeralDestroyer) IsEphemeral(ctx context.Context, convID string) (bool, error) {
	return d.convRepo.IsEphemeral(ctx, convID)
}

// BeginDestroy 执行 Stage A：隐藏会话与消息、清内存向量、入队物理销毁任务。
// 幂等：会话不存在则 no-op；同 conv 已有 pending 销毁任务则不重复入队。
func (d *EphemeralDestroyer) BeginDestroy(ctx context.Context, convID, userID string) error {
	scope, found, err := d.convRepo.BeginPurge(ctx, convID)
	if err != nil {
		return fmt.Errorf("begin purge: %w", err)
	}
	if !found {
		return nil
	}
	for _, id := range scope {
		d.vectorStore.DeleteByMetadata("conv_id", id)
	}
	return d.enqueuePurge(ctx, userID, convID)
}

// enqueuePurge 入队一条 ephemeral_purge 延迟任务（去重：同 conv 已有 pending 则跳过）。
func (d *EphemeralDestroyer) enqueuePurge(ctx context.Context, userID, convID string) error {
	pending, err := d.delayedTaskRepo.FindPendingByUserAndType(ctx, userID, TaskTypeEphemeralPurge)
	if err != nil {
		// 查重是优化——出错时不 return，直接走 insert 分支。
		log.Printf("[ephemeral] FindPendingByUserAndType error: %v, fallback to insert", err)
	} else {
		for _, t := range pending {
			if m, e := t.ParseMeta(); e == nil && m.ConvID == convID {
				return nil
			}
		}
	}
	job := &model.DelayedTask{
		ID:        uuid.New().String(),
		UserID:    userID,
		TaskType:  TaskTypeEphemeralPurge,
		Status:    "pending",
		ProcessAt: time.Now(),
	}
	if err := job.SetMeta(model.DelayedTaskMeta{ConvID: convID}); err != nil {
		return err
	}
	if err := d.delayedTaskRepo.Create(ctx, job); err != nil {
		return err
	}
	log.Printf("[ephemeral] purge scheduled: conv=%s task=%s", convID[:8], job.ID[:8])
	return nil
}

// RunPurge 执行 Stage B：分批节流物理删除消息/向量，最后删会话行。幂等可续跑。
func (d *EphemeralDestroyer) RunPurge(ctx context.Context, convID string) error {
	scope, err := d.convRepo.ListScopeIDs(ctx, convID)
	if err != nil {
		return fmt.Errorf("list scope: %w", err)
	}
	total := 0
	for {
		n, err := d.convRepo.PurgeMessageBatch(ctx, scope, purgeBatchSize)
		if err != nil {
			return fmt.Errorf("purge batch: %w", err)
		}
		total += n
		if n < purgeBatchSize {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(purgeBatchPause):
		}
	}
	if err := d.convRepo.FinishPurge(ctx, scope); err != nil {
		return fmt.Errorf("finish purge: %w", err)
	}
	log.Printf("[ephemeral] purged conv=%s scope=%d msgs=%d", convID[:8], len(scope), total)
	return nil
}
