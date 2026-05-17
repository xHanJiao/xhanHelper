package service

import (
	"context"
	"time"

	"gorm.io/gorm"

	"xhan-helper/internal/model"
)

// CommitmentRepo — 待办/承诺数据访问
type CommitmentRepo struct {
	db *gorm.DB
}

func NewCommitmentRepo(db *gorm.DB) *CommitmentRepo {
	return &CommitmentRepo{db: db}
}

// Create 创建一条承诺
func (r *CommitmentRepo) Create(ctx context.Context, c *model.Commitment) error {
	return r.db.WithContext(ctx).Create(c).Error
}

// ListPendingByDeadline 列出截止时间在指定范围内的待推送承诺。userID 为空时扫所有用户。
func (r *CommitmentRepo) ListPendingByDeadline(ctx context.Context, userID string, before time.Time, limit int) ([]model.Commitment, error) {
	var list []model.Commitment
	q := r.db.WithContext(ctx).
		Where("status = ? AND next_deadline IS NOT NULL AND next_deadline <= ?", "pending", before)
	if userID != "" {
		q = q.Where("user_id = ?", userID)
	}
	err := q.Order("next_deadline ASC").Limit(limit).Find(&list).Error
	return list, err
}

// ListPendingByUser 列出用户所有待处理的承诺
func (r *CommitmentRepo) ListPendingByUser(ctx context.Context, userID string) ([]model.Commitment, error) {
	var list []model.Commitment
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND status = ?", userID, "pending").
		Find(&list).Error
	return list, err
}

// UpdateNextDeadline 更新下一次触发时间
func (r *CommitmentRepo) UpdateNextDeadline(ctx context.Context, id string, next time.Time) error {
	return r.db.WithContext(ctx).Model(&model.Commitment{}).
		Where("id = ?", id).
		Update("next_deadline", next).Error
}

// MarkTriggered 标记为已触发（一次性承诺）
func (r *CommitmentRepo) MarkTriggered(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Model(&model.Commitment{}).
		Where("id = ?", id).
		Update("status", "triggered").Error
}

// MarkDone 标记为已完成
func (r *CommitmentRepo) MarkDone(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Model(&model.Commitment{}).
		Where("id = ?", id).
		Update("status", "done").Error
}
