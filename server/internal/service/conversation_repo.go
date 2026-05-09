package service

import (
	"context"
	"errors"
	"time"

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

// DB 暴露底层 gorm.DB
func (r *ConversationRepo) DB() *gorm.DB {
	return r.db
}

// ListQuiet 查找静默会话：有未提取的消息且超时无活动
func (r *ConversationRepo) ListQuiet(ctx context.Context, cutoff time.Time, limit int) ([]model.Conversation, error) {
	var list []model.Conversation
	err := r.db.WithContext(ctx).
		Where("updated_at < ? AND is_deleted = ?", cutoff, false).
		Where(`last_extracted_msg_id IS NULL
			OR last_extracted_msg_id != (
				SELECT id FROM messages
				WHERE conversation_id = conversations.id
				ORDER BY created_at DESC LIMIT 1
			)`).
		Order("updated_at ASC").
		Limit(limit).
		Find(&list).Error
	return list, err
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

// ListByUser 获取设备未删除的会话列表（按更新时间倒序）
func (r *ConversationRepo) ListByUser(ctx context.Context, userID string, limit int) ([]model.Conversation, error) {
	var list []model.Conversation
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND is_deleted = ?", userID, false).
		Order("updated_at DESC").Limit(limit).
		Find(&list).Error
	return list, err
}

// Rename 重命名会话
func (r *ConversationRepo) Rename(ctx context.Context, id, title string) error {
	return r.db.WithContext(ctx).Model(&model.Conversation{}).
		Where("id = ?", id).Update("title", title).Error
}

// Update 更新会话字段（仅写非零值字段，避免 Save 归零 IsDeleted 等）
func (r *ConversationRepo) Update(ctx context.Context, c *model.Conversation) error {
	return r.db.WithContext(ctx).Model(&model.Conversation{}).Where("id = ?", c.ID).Updates(c).Error
}

// Touch 更新会话的 updated_at 到当前时间
func (r *ConversationRepo) Touch(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Model(&model.Conversation{}).Where("id = ?", id).Update("updated_at", time.Now()).Error
}

// SoftDelete 软删除会话
func (r *ConversationRepo) SoftDelete(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Model(&model.Conversation{}).
		Where("id = ?", id).Update("is_deleted", true).Error
}
