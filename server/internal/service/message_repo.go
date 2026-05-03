package service

import (
	"context"

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

// ListByConversation 获取会话的消息列表（按时间正序）
func (r *MessageRepo) ListByConversation(ctx context.Context, convID string, limit int) ([]model.Message, error) {
	var list []model.Message
	err := r.db.WithContext(ctx).
		Where("conversation_id = ?", convID).
		Order("created_at ASC").Limit(limit).
		Find(&list).Error
	return list, err
}
