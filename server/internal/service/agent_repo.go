package service

import (
	"context"
	"errors"

	"gorm.io/gorm"
	"xhan-helper/internal/model"
)

// AgentRepo — 智能体数据访问
type AgentRepo struct {
	db *gorm.DB
}

// NewAgentRepo 创建 AgentRepo
func NewAgentRepo(db *gorm.DB) *AgentRepo {
	return &AgentRepo{db: db}
}

// Create 创建智能体
func (r *AgentRepo) Create(ctx context.Context, a *model.Agent) error {
	return r.db.WithContext(ctx).Create(a).Error
}

// FindByID 按 ID+用户 查找
func (r *AgentRepo) FindByID(ctx context.Context, id, userID string) (*model.Agent, error) {
	var a model.Agent
	err := r.db.WithContext(ctx).Where("id = ? AND user_id = ?", id, userID).First(&a).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &a, err
}

// ListByUser 分页获取用户智能体
func (r *AgentRepo) ListByUser(ctx context.Context, userID string, limit, offset int) ([]model.Agent, error) {
	if limit <= 0 {
		limit = 50
	}
	var list []model.Agent
	err := r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("created_at DESC").
		Limit(limit).Offset(offset).
		Find(&list).Error
	return list, err
}

// Update 更新智能体（仅非零值字段）
func (r *AgentRepo) Update(ctx context.Context, a *model.Agent) error {
	return r.db.WithContext(ctx).Model(&model.Agent{}).
		Where("id = ? AND user_id = ?", a.ID, a.UserID).
		Updates(a).Error
}

// Delete 删除智能体
func (r *AgentRepo) Delete(ctx context.Context, id, userID string) error {
	return r.db.WithContext(ctx).
		Where("id = ? AND user_id = ?", id, userID).
		Delete(&model.Agent{}).Error
}
