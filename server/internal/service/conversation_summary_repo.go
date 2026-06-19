package service

import (
	"context"

	"gorm.io/gorm"

	"xhan-helper/internal/model"
)

// ConversationSummaryRepo — 对话摘要数据访问
type ConversationSummaryRepo struct {
	db *gorm.DB
}

func NewConversationSummaryRepo(db *gorm.DB) *ConversationSummaryRepo {
	return &ConversationSummaryRepo{db: db}
}

// Create 创建一条摘要记录
func (r *ConversationSummaryRepo) Create(ctx context.Context, s *model.ConversationSummary) error {
	return r.db.WithContext(ctx).Create(s).Error
}

// GetLatest 获取会话最新的一条 active 摘要
func (r *ConversationSummaryRepo) GetLatest(ctx context.Context, convID string) (*model.ConversationSummary, error) {
	var s model.ConversationSummary
	err := r.db.WithContext(ctx).
		Where("conversation_id = ? AND status = ?", convID, "active").
		Order("created_at DESC").Limit(1).
		First(&s).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &s, nil
}

// GetByID 按 ID + user_id 查询摘要，防止串用户数据
func (r *ConversationSummaryRepo) GetByID(ctx context.Context, id, userID string) (*model.ConversationSummary, error) {
	var s model.ConversationSummary
	err := r.db.WithContext(ctx).
		Where("id = ? AND user_id = ?", id, userID).
		First(&s).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &s, nil
}

// MarkSuperseded 将指定摘要标记为已替代
func (r *ConversationSummaryRepo) MarkSuperseded(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).
		Model(&model.ConversationSummary{}).
		Where("id = ?", id).
		Update("status", "superseded").Error
}
