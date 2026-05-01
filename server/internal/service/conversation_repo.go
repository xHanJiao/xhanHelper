package service

import (
	"context"
	"errors"

	"gorm.io/gorm"
	"xhan-helper/internal/model"
)

// ConversationRepo — 会话数据访问
type ConversationRepo struct {
	db *gorm.DB
}

// NewConversationRepo 创建 ConversationRepo
func NewConversationRepo(db *gorm.DB) *ConversationRepo {
	return &ConversationRepo{db: db}
}

// Create 创建新会话
func (r *ConversationRepo) Create(ctx context.Context, c *model.Conversation) error {
	return r.db.WithContext(ctx).Create(c).Error
}

// FindByID 根据 ID 查找会话
func (r *ConversationRepo) FindByID(ctx context.Context, id string) (*model.Conversation, error) {
	var c model.Conversation
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&c).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &c, err
}

// ListByDevice 获取设备未删除的会话列表（按更新时间倒序）
func (r *ConversationRepo) ListByDevice(ctx context.Context, deviceID string, limit int) ([]model.Conversation, error) {
	var list []model.Conversation
	err := r.db.WithContext(ctx).
		Where("device_id = ? AND is_deleted = ?", deviceID, false).
		Order("updated_at DESC").Limit(limit).
		Find(&list).Error
	return list, err
}

// Rename 重命名会话
func (r *ConversationRepo) Rename(ctx context.Context, id, title string) error {
	return r.db.WithContext(ctx).Model(&model.Conversation{}).
		Where("id = ?", id).Update("title", title).Error
}

// SoftDelete 软删除会话
func (r *ConversationRepo) SoftDelete(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Model(&model.Conversation{}).
		Where("id = ?", id).Update("is_deleted", true).Error
}
