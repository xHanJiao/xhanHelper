package service

import (
	"context"

	"gorm.io/gorm"

	"xhan-helper/internal/model"
)

// PendingFindingRepo — 待投递发现数据访问
type PendingFindingRepo struct {
	db *gorm.DB
}

func NewPendingFindingRepo(db *gorm.DB) *PendingFindingRepo {
	return &PendingFindingRepo{db: db}
}

// Create 创建一条待投递发现
func (r *PendingFindingRepo) Create(ctx context.Context, f *model.PendingFinding) error {
	return r.db.WithContext(ctx).Create(f).Error
}

// ListPending 列出一个主题下待投递的发现
func (r *PendingFindingRepo) ListPending(ctx context.Context, userID string) ([]model.PendingFinding, error) {
	var list []model.PendingFinding
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND status = ?", userID, "pending").
		Order("created_at ASC").
		Find(&list).Error
	return list, err
}

// ListByTheme 列出指定主题下待投递的发现
func (r *PendingFindingRepo) ListByTheme(ctx context.Context, themeID string) ([]model.PendingFinding, error) {
	var list []model.PendingFinding
	err := r.db.WithContext(ctx).
		Where("theme_id = ? AND status = ?", themeID, "pending").
		Find(&list).Error
	return list, err
}

// IncrementRetry 增加重试计数
func (r *PendingFindingRepo) IncrementRetry(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Model(&model.PendingFinding{}).
		Where("id = ?", id).
		UpdateColumn("retry_count", gorm.Expr("retry_count + 1")).Error
}

// MarkDelivered 标记为已投递
func (r *PendingFindingRepo) MarkDelivered(ctx context.Context, ids []string) error {
	return r.db.WithContext(ctx).Model(&model.PendingFinding{}).
		Where("id IN ?", ids).
		Update("status", "delivered").Error
}

// MarkFailed 标记投递失败的发现（超重试上限）
func (r *PendingFindingRepo) MarkFailed(ctx context.Context, ids []string) error {
	return r.db.WithContext(ctx).Model(&model.PendingFinding{}).
		Where("id IN ?", ids).
		Update("status", "failed").Error
}
