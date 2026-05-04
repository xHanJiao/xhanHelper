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

// UpdateEmbedding 更新记忆的向量字段
func (r *UserMemoryRepo) UpdateEmbedding(ctx context.Context, id string, embedding *string) error {
	return r.db.WithContext(ctx).Model(&model.UserMemory{}).Where("id = ?", id).
		Update("embedding", embedding).Error
}

// GetByIDs 按 ID 列表查询
func (r *UserMemoryRepo) GetByIDs(ctx context.Context, ids []string) ([]model.UserMemory, error) {
	var list []model.UserMemory
	err := r.db.WithContext(ctx).Where("id IN ?", ids).Find(&list).Error
	return list, err
}

// FindByPromptHash 按 prompt 哈希查找已有记忆（输入去重）
func (r *UserMemoryRepo) FindByPromptHash(ctx context.Context, userID, hash string) (*model.UserMemory, error) {
	var m model.UserMemory
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND prompt_hash = ?", userID, hash).
		First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &m, err
}

// ListAll 获取所有记忆（用于 warmup 加载向量库）
func (r *UserMemoryRepo) ListAll(ctx context.Context) ([]model.UserMemory, error) {
	var list []model.UserMemory
	err := r.db.WithContext(ctx).Find(&list).Error
	return list, err
}

// UpdateConfirm 更新确认计数
func (r *UserMemoryRepo) UpdateConfirm(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Model(&model.UserMemory{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"confirm_count":     gorm.Expr("confirm_count + 1"),
			"last_confirmed_at": gorm.Expr("CURRENT_TIMESTAMP"),
		}).Error
}
