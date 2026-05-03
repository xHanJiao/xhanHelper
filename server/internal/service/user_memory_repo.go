package service

import (
	"context"
	"errors"

	"gorm.io/gorm"
	"xhan-helper/internal/model"
)

// UserMemoryRepo — 用户记忆数据访问
type UserMemoryRepo struct {
	db *gorm.DB
}

// NewUserMemoryRepo 创建 UserMemoryRepo
func NewUserMemoryRepo(db *gorm.DB) *UserMemoryRepo {
	return &UserMemoryRepo{db: db}
}

// Create 创建新记忆
func (r *UserMemoryRepo) Create(ctx context.Context, m *model.UserMemory) error {
	return r.db.WithContext(ctx).Create(m).Error
}

// FindByKey 按 key 查找已有记忆
func (r *UserMemoryRepo) FindByKey(ctx context.Context, userID, key string) (*model.UserMemory, error) {
	var m model.UserMemory
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND memory_key = ?", userID, key).
		First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &m, err
}

// ListByUser 获取设备所有记忆
func (r *UserMemoryRepo) ListByUser(ctx context.Context, userID string) ([]model.UserMemory, error) {
	var list []model.UserMemory
	err := r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("created_at DESC").
		Find(&list).Error
	return list, err
}

// UpdateConfirm 更新确认计数
func (r *UserMemoryRepo) UpdateConfirm(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Model(&model.UserMemory{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"confirm_count":    gorm.Expr("confirm_count + 1"),
			"last_confirmed_at": gorm.Expr("CURRENT_TIMESTAMP"),
		}).Error
}
