package service

import (
	"context"
	"errors"

	"gorm.io/gorm"
	"xhan-helper/internal/model"
)

// MessageRepo — 消息数据访问
type MessageRepo struct {
	db *gorm.DB
}

// NewMessageRepo 创建 MessageRepo
func NewMessageRepo(db *gorm.DB) *MessageRepo {
	return &MessageRepo{db: db}
}

// Create 创建新消息
func (r *MessageRepo) Create(ctx context.Context, m *model.Message) error {
	return r.db.WithContext(ctx).Create(m).Error
}

// UpdateLastUserEmbedding 更新会话中最后一条 user 消息的 embedding
func (r *MessageRepo) UpdateLastUserEmbedding(ctx context.Context, convID string, embedding *string) error {
	return r.db.WithContext(ctx).Model(&model.Message{}).
		Where("conversation_id = ? AND role = ?", convID, "user").
		Order("created_at DESC").Limit(1).
		Update("embedding", embedding).Error
}

// GetByID 按 ID 查单条消息，未找到返回 (nil, nil)
func (r *MessageRepo) GetByID(ctx context.Context, id string) (*model.Message, error) {
	var m model.Message
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// ListByConversation 获取会话的消息列表（按时间正序）
func (r *MessageRepo) ListByConversation(ctx context.Context, convID string, limit int) ([]model.Message, error) {
	var list []model.Message
	err := r.db.WithContext(ctx).
		Where("conversation_id = ?", convID).
		Order("created_at ASC").Limit(limit).
		Find(&list).Error
	return list, err
}

// ListByConversationSince 获取指定消息之后的消息（按时间正序，不含 sinceID 自身）
func (r *MessageRepo) ListByConversationSince(ctx context.Context, convID, sinceID string, limit int) ([]model.Message, error) {
	var since model.Message
	if err := r.db.WithContext(ctx).Where("id = ?", sinceID).First(&since).Error; err != nil {
		return r.ListByConversation(ctx, convID, limit)
	}
	var list []model.Message
	err := r.db.WithContext(ctx).
		Where("conversation_id = ? AND created_at > ?", convID, since.CreatedAt).
		Order("created_at ASC").Limit(limit).
		Find(&list).Error
	return list, err
}
