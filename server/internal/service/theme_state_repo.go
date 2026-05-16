package service

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"xhan-helper/internal/model"
)

// ThemeStateRepo — 主题状态页数据访问
type ThemeStateRepo struct {
	db *gorm.DB
}

func NewThemeStateRepo(db *gorm.DB) *ThemeStateRepo {
	return &ThemeStateRepo{db: db}
}

// Create 创建主题
func (r *ThemeStateRepo) Create(ctx context.Context, t *model.ThemeState) error {
	return r.db.WithContext(ctx).Create(t).Error
}

// GetByID 按 ID 查询（含用户校验）
func (r *ThemeStateRepo) GetByID(ctx context.Context, id, userID string) (*model.ThemeState, error) {
	var t model.ThemeState
	err := r.db.WithContext(ctx).Where("id = ? AND user_id = ?", id, userID).First(&t).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &t, err
}

// GetByName 按名称 + 用户查询（名称唯一性由应用层保证）
func (r *ThemeStateRepo) GetByName(ctx context.Context, userID, name string) (*model.ThemeState, error) {
	var t model.ThemeState
	err := r.db.WithContext(ctx).Where("user_id = ? AND name = ?", userID, name).First(&t).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &t, err
}

// ListActive 列出用户所有活跃（非关闭）主题
func (r *ThemeStateRepo) ListActive(ctx context.Context, userID string) ([]model.ThemeState, error) {
	var list []model.ThemeState
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND status != ?", userID, "closed").
		Order("created_at ASC").
		Find(&list).Error
	return list, err
}

// UpdateState 更新状态页文本 + 最后运行时间
func (r *ThemeStateRepo) UpdateState(ctx context.Context, id, userID, stateText string) error {
	now := time.Now()
	return r.db.WithContext(ctx).Model(&model.ThemeState{}).
		Where("id = ? AND user_id = ?", id, userID).
		Updates(map[string]interface{}{
			"state_text":  stateText,
			"last_run_at": now,
		}).Error
}

// ListByStatus 按状态列表查询
func (r *ThemeStateRepo) ListByStatus(ctx context.Context, userID string, statuses []string) ([]model.ThemeState, error) {
	var list []model.ThemeState
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND status IN ?", userID, statuses).
		Order("created_at ASC").
		Find(&list).Error
	return list, err
}

// UpdateStatus 更新主题状态
func (r *ThemeStateRepo) UpdateStatus(ctx context.Context, id, userID, status string) error {
	return r.db.WithContext(ctx).Model(&model.ThemeState{}).
		Where("id = ? AND user_id = ?", id, userID).
		Update("status", status).Error
}

// UpdateEmbeddingID 更新主题的 embedding_id
func (r *ThemeStateRepo) UpdateEmbeddingID(ctx context.Context, id, embeddingID string) error {
	return r.db.WithContext(ctx).Model(&model.ThemeState{}).
		Where("id = ?", id).
		Update("embedding_id", embeddingID).Error
}

// Archive 归档主题
func (r *ThemeStateRepo) Archive(ctx context.Context, id, userID string) error {
	return r.db.WithContext(ctx).Model(&model.ThemeState{}).
		Where("id = ? AND user_id = ?", id, userID).
		Update("status", "archived").Error
}

// Reactivate 重新激活归档的主题
func (r *ThemeStateRepo) Reactivate(ctx context.Context, id, userID string) error {
	return r.db.WithContext(ctx).Model(&model.ThemeState{}).
		Where("id = ? AND user_id = ?", id, userID).
		Update("status", "active").Error
}

// Close 关闭主题
func (r *ThemeStateRepo) Close(ctx context.Context, id, userID string) error {
	return r.db.WithContext(ctx).Model(&model.ThemeState{}).
		Where("id = ? AND user_id = ?", id, userID).
		Update("status", "closed").Error
}

// List 分页列出用户主题
func (r *ThemeStateRepo) List(ctx context.Context, userID, status string, limit, offset int) ([]model.ThemeState, int64, error) {
	if limit <= 0 {
		limit = 20
	}
	q := r.db.WithContext(ctx).Where("user_id = ?", userID)
	if status != "" {
		q = q.Where("status = ?", status)
	}

	var total int64
	if err := q.Model(&model.ThemeState{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var list []model.ThemeState
	err := q.Order("created_at DESC").Limit(limit).Offset(offset).Find(&list).Error
	return list, total, err
}
