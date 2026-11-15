package service

import (
	"context"
	"errors"
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

// FindByID 按 ID 查任务，未找到返回 (nil, nil)
func (r *DelayedTaskRepo) FindByID(ctx context.Context, id string) (*model.DelayedTask, error) {
	var t model.DelayedTask
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&t).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
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

// FindPendingByUserAndType 查某用户某类型的全部 pending 任务
func (r *DelayedTaskRepo) FindPendingByUserAndType(ctx context.Context, userID, taskType string) ([]model.DelayedTask, error) {
	var list []model.DelayedTask
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND task_type = ? AND status = ?", userID, taskType, "pending").
		Find(&list).Error
	return list, err
}

// ScheduleRetry 安排重试：保持 pending 状态，retry_count+1, process_at 推后 delay。
// 与 MarkFailed 不同：不改变 status，任务会被下一轮 ListDue 再次扫到。
func (r *DelayedTaskRepo) ScheduleRetry(ctx context.Context, id string, retryCount int, delay time.Duration) error {
	return r.db.WithContext(ctx).Model(&model.DelayedTask{}).Where("id = ?", id).
		Updates(map[string]interface{}{
			"retry_count": retryCount + 1,
			"process_at":  time.Now().Add(delay),
		}).Error
}

// UpdateProcessAtAndMeta 更新任务的处理时间和 TaskMeta（用于 debounce 时同步更新 LastMsgID）
func (r *DelayedTaskRepo) UpdateProcessAtAndMeta(ctx context.Context, id string, t time.Time, meta string) error {
	return r.db.WithContext(ctx).Model(&model.DelayedTask{}).Where("id = ?", id).
		Updates(map[string]interface{}{
			"process_at": t,
			"task_meta":  meta,
		}).Error
}

// CountPending 统计全局 pending 任务数
func (r *DelayedTaskRepo) CountPending(ctx context.Context) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.DelayedTask{}).
		Where("status = ?", "pending").Count(&count).Error
	return count, err
}

// DelayedTaskQueueStat — 按 task_type 聚合的延迟任务队列统计（HookStatus 看板用，非表模型）。
type DelayedTaskQueueStat struct {
	TaskType string    `json:"task_type"`
	Pending  int64     `json:"pending"`
	NextAt   time.Time `json:"next_at,omitempty"`
}

// StatsByType 按 task_type 聚合 pending 任务数与该类型最早到期时间。
// 新增 task_type 无需改代码，自动出现在结果里。
func (r *DelayedTaskRepo) StatsByType(ctx context.Context) ([]DelayedTaskQueueStat, error) {
	var stats []DelayedTaskQueueStat
	err := r.db.WithContext(ctx).Model(&model.DelayedTask{}).
		Select("task_type, COUNT(*) AS pending, MIN(process_at) AS next_at").
		Where("status = ?", "pending").
		Group("task_type").
		Scan(&stats).Error
	return stats, err
}
