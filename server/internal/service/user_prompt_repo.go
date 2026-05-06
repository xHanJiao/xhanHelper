package service

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"xhan-helper/internal/model"
)

// UserPromptRepo — 用户自定义 System Prompt 数据访问
type UserPromptRepo struct {
	db *gorm.DB
}

// NewUserPromptRepo 创建 UserPromptRepo
func NewUserPromptRepo(db *gorm.DB) *UserPromptRepo {
	return &UserPromptRepo{db: db}
}

// Create 创建新 prompt，同时将同类型的旧 prompt 设为 inactive
func (r *UserPromptRepo) Create(ctx context.Context, p *model.UserPrompt) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		tx.Model(&model.UserPrompt{}).
			Where("user_id = ? AND prompt_type = ? AND status = ?", p.UserID, p.PromptType, "active").
			Update("status", "inactive")
		return tx.Create(p).Error
	})
}

// FindActive 查找用户当前 active 的 prompt
func (r *UserPromptRepo) FindActive(ctx context.Context, userID, promptType string) (*model.UserPrompt, error) {
	var p model.UserPrompt
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND prompt_type = ? AND status = ?", userID, promptType, "active").
		First(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &p, err
}

// ListByUser 列表，支持按 type/status 筛选
func (r *UserPromptRepo) ListByUser(ctx context.Context, userID, promptType, status string) ([]model.UserPrompt, error) {
	q := r.db.WithContext(ctx).Where("user_id = ?", userID)
	if promptType != "" {
		q = q.Where("prompt_type = ?", promptType)
	}
	if status != "" {
		q = q.Where("status = ?", status)
	}
	var list []model.UserPrompt
	err := q.Order("created_at DESC").Find(&list).Error
	return list, err
}

// Rollback 将指定 prompt 设为 active，其他同类型 inactive
func (r *UserPromptRepo) Rollback(ctx context.Context, id, userID, promptType string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		tx.Model(&model.UserPrompt{}).
			Where("user_id = ? AND prompt_type = ?", userID, promptType).
			Update("status", "inactive")
		return tx.Model(&model.UserPrompt{}).
			Where("id = ? AND user_id = ?", id, userID).
			Update("status", "active").Error
	})
}

// Delete 永久删除
func (r *UserPromptRepo) Delete(ctx context.Context, id, userID string) error {
	return r.db.WithContext(ctx).Where("id = ? AND user_id = ?", id, userID).Delete(&model.UserPrompt{}).Error
}

// ResolveActive 适配 agent.PromptResolver 接口
func (r *UserPromptRepo) ResolveActive(ctx context.Context, userID, promptType string) string {
	p, err := r.FindActive(ctx, userID, promptType)
	if err != nil || p == nil {
		return ""
	}
	return p.SystemPrompt
}
