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

// FindByIDs 按 ID 列表批量查询
func (r *AgentRepo) FindByIDs(ctx context.Context, ids []string) ([]model.Agent, error) {
	var list []model.Agent
	err := r.db.WithContext(ctx).Where("id IN ?", ids).Find(&list).Error
	return list, err
}

// ListByUser 分页获取用户智能体
func (r *AgentRepo) ListByUser(ctx context.Context, userID string, limit, offset int) ([]model.Agent, int64, error) {
	if limit <= 0 {
		limit = 50
	}
	q := r.db.WithContext(ctx).Where("user_id = ?", userID)

	var total int64
	if err := q.Model(&model.Agent{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var list []model.Agent
	err := q.Order("created_at DESC").Limit(limit).Offset(offset).Find(&list).Error
	return list, total, err
}

// Update 更新智能体（仅非零值字段）
func (r *AgentRepo) Update(ctx context.Context, a *model.Agent) error {
	return r.db.WithContext(ctx).Model(&model.Agent{}).
		Where("id = ? AND user_id = ?", a.ID, a.UserID).
		Updates(a).Error
}

// FindByName 按名称+用户 查找智能体
func (r *AgentRepo) FindByName(ctx context.Context, name, userID string) (*model.Agent, error) {
	var a model.Agent
	err := r.db.WithContext(ctx).Where("name = ? AND user_id = ?", name, userID).First(&a).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &a, err
}

// Delete 删除智能体
func (r *AgentRepo) Delete(ctx context.Context, id, userID string) error {
	return r.db.WithContext(ctx).
		Where("id = ? AND user_id = ?", id, userID).
		Delete(&model.Agent{}).Error
}

// inputBarPageSize 输入栏智能体选择器的分页大小
const inputBarPageSize = 10

// ListByCursor 按创建时间倒序游标分页获取用户智能体（cursor 为 offset）。
// 返回 InputBarAgentDTO，供输入栏智能体选择器使用。
func (r *AgentRepo) ListByCursor(ctx context.Context, userID string, cursor int) ([]*model.InputBarAgentDTO, error) {
	if cursor < 0 {
		cursor = 0
	}
	var agents []model.Agent
	err := r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("created_at DESC").
		Limit(inputBarPageSize).
		Offset(cursor).
		Find(&agents).Error
	if err != nil {
		return nil, err
	}
	dtos := make([]*model.InputBarAgentDTO, 0, len(agents))
	for i := range agents {
		dtos = append(dtos, &model.InputBarAgentDTO{Agent: agents[i]})
	}
	return dtos, nil
}

// GetActiveAgent 获取会话当前绑定的智能体（Active=true），未绑定返回 nil。
func (r *AgentRepo) GetActiveAgent(ctx context.Context, userID, convID string) (*model.InputBarAgentDTO, error) {
	var conv model.Conversation
	err := r.db.WithContext(ctx).
		Where("id = ? AND user_id = ?", convID, userID).
		First(&conv).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if conv.AgentID == nil || *conv.AgentID == "" {
		return nil, nil
	}
	var agent model.Agent
	err = r.db.WithContext(ctx).
		Where("id = ? AND user_id = ?", *conv.AgentID, userID).
		First(&agent).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &model.InputBarAgentDTO{Agent: agent, Active: true}, nil
}

// ListByCursorWithActiveSetTop 按创建时间倒序游标分页获取用户智能体，
// 排除已激活的智能体（activeID 非空时），避免与首位激活项重复。
func (r *AgentRepo) ListByCursorWithActiveSetTop(ctx context.Context, userID, activeID string, cursor int) ([]*model.InputBarAgentDTO, error) {
	if cursor < 0 {
		cursor = 0
	}
	q := r.db.WithContext(ctx).Where("user_id = ?", userID)
	if activeID != "" {
		q = q.Where("id != ?", activeID)
	}
	var agents []model.Agent
	err := q.
		Order("created_at DESC").
		Limit(inputBarPageSize).
		Offset(cursor).
		Find(&agents).Error
	if err != nil {
		return nil, err
	}
	dtos := make([]*model.InputBarAgentDTO, 0, len(agents))
	for i := range agents {
		dtos = append(dtos, &model.InputBarAgentDTO{Agent: agents[i]})
	}
	return dtos, nil
}
