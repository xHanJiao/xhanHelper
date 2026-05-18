package service

import (
	"context"
	"time"

	"gorm.io/gorm"

	"xhan-helper/internal/model"
)

// DelayedTaskRepo — 通用延迟任务数据访问
type DelayedTaskRepo struct {
	db *gorm.DB
}

func NewDelayedTaskRepo(db *gorm.DB) *DelayedTaskRepo {
	return &DelayedTaskRepo{db: db}
}

// Create 创建一条延迟任务
func (r *DelayedTaskRepo) Create(ctx context.Context, t *model.DelayedTask) error {
	return r.db.WithContext(ctx).Create(t).Error
}

// ListDue 列出指定类型的到期任务（status=pending 且 process_at <= now）
func (r *DelayedTaskRepo) ListDue(ctx context.Context, taskType string, limit int) ([]model.DelayedTask, error) {
	var list []model.DelayedTask
	err := r.db.WithContext(ctx).
		Where("task_type = ? AND status = ? AND process_at <= ?", taskType, "pending", time.Now()).
		Order("process_at ASC").Limit(limit).
		Find(&list).Error
	return list, err
}

// MarkDone 标记任务完成
func (r *DelayedTaskRepo) MarkDone(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Model(&model.DelayedTask{}).Where("id = ?", id).
		Update("status", "done").Error
}

// MarkFailed 标记失败，重试次数+1，process_at 推后
func (r *DelayedTaskRepo) MarkFailed(ctx context.Context, id string, retryCount int) error {
	return r.db.WithContext(ctx).Model(&model.DelayedTask{}).Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":      "failed",
			"retry_count": retryCount + 1,
			"process_at":  time.Now().Add(10 * time.Minute),
		}).Error
}

// EarliestPending 返回最早到期 pending 任务的 process_at（无任务时返回零值）
func (r *DelayedTaskRepo) EarliestPending(ctx context.Context) (time.Time, error) {
	var t model.DelayedTask
	err := r.db.WithContext(ctx).
		Where("status = ?", "pending").
		Order("process_at ASC").Limit(1).
		First(&t).Error
	if err != nil {
		return time.Time{}, err
	}
	return t.ProcessAt, nil
}

// CountPending 统计全局 pending 任务数
func (r *DelayedTaskRepo) CountPending(ctx context.Context) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.DelayedTask{}).
		Where("status = ?", "pending").Count(&count).Error
	return count, err
}
