package service

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"xhan-helper/internal/model"
)

// ToolRepo — 动态工具 + 调用数据访问
type ToolRepo struct {
	db *gorm.DB
}

// NewToolRepo 创建 ToolRepo
func NewToolRepo(db *gorm.DB) *ToolRepo {
	return &ToolRepo{db: db}
}

// —— tool_registry ——

// ListActiveTools 获取所有 status=active 的工具
func (r *ToolRepo) ListActiveTools(ctx context.Context) ([]model.ToolRegistry, error) {
	var list []model.ToolRegistry
	err := r.db.WithContext(ctx).Where("status = ?", "active").Find(&list).Error
	return list, err
}

// ListTools 获取用户的工具（可按状态筛选）
func (r *ToolRepo) ListTools(ctx context.Context, userID, status string, limit, offset int) ([]model.ToolRegistry, int64, error) {
	if limit <= 0 {
		limit = 50
	}
	q := r.db.WithContext(ctx).Where("user_id = ?", userID)
	if status != "" {
		q = q.Where("status = ?", status)
	}
	var total int64
	if err := q.Model(&model.ToolRegistry{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.ToolRegistry
	err := q.Order("created_at DESC").Limit(limit).Offset(offset).Find(&list).Error
	return list, total, err
}

// FindToolByID 按 ID+用户 查找
func (r *ToolRepo) FindToolByID(ctx context.Context, id, userID string) (*model.ToolRegistry, error) {
	var t model.ToolRegistry
	err := r.db.WithContext(ctx).Where("id = ? AND user_id = ?", id, userID).First(&t).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &t, err
}

// CreateTool 注册新工具
func (r *ToolRepo) CreateTool(ctx context.Context, t *model.ToolRegistry) error {
	return r.db.WithContext(ctx).Create(t).Error
}

// UpdateTool 更新工具（仅非零值字段）
func (r *ToolRepo) UpdateTool(ctx context.Context, t *model.ToolRegistry) error {
	return r.db.WithContext(ctx).Model(&model.ToolRegistry{}).
		Where("id = ? AND user_id = ?", t.ID, t.UserID).
		Updates(t).Error
}

// DeleteTool 删除工具注册
func (r *ToolRepo) DeleteTool(ctx context.Context, id, userID string) error {
	return r.db.WithContext(ctx).
		Where("id = ? AND user_id = ?", id, userID).
		Delete(&model.ToolRegistry{}).Error
}
