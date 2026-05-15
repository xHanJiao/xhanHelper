package service

import (
	"context"

	"gorm.io/gorm"

	"xhan-helper/internal/model"
)

// MailboxRepo — 信箱消息数据访问
type MailboxRepo struct {
	db *gorm.DB
}

func NewMailboxRepo(db *gorm.DB) *MailboxRepo {
	return &MailboxRepo{db: db}
}

// Create 投递一条信箱消息
func (r *MailboxRepo) Create(ctx context.Context, item *model.MailboxItem) error {
	return r.db.WithContext(ctx).Create(item).Error
}

// ListByUser 分页查询用户信箱消息，支持按 type / is_read 筛选
func (r *MailboxRepo) ListByUser(ctx context.Context, userID, itemType string, isRead *bool, limit, offset int) ([]model.MailboxItem, int64, error) {
	if limit <= 0 {
		limit = 20
	}
	q := r.db.WithContext(ctx).Where("user_id = ? AND is_archived = ?", userID, false)
	if itemType != "" {
		q = q.Where("type = ?", itemType)
	}
	if isRead != nil {
		q = q.Where("is_read = ?", *isRead)
	}

	var total int64
	if err := q.Model(&model.MailboxItem{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var list []model.MailboxItem
	err := q.Order("created_at DESC").Limit(limit).Offset(offset).Find(&list).Error
	return list, total, err
}

// UnreadCount 获取用户未读信箱消息数
func (r *MailboxRepo) UnreadCount(ctx context.Context, userID string) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.MailboxItem{}).
		Where("user_id = ? AND is_read = ? AND is_archived = ?", userID, false, false).
		Count(&count).Error
	return count, err
}

// MarkRead 标记单条消息为已读
func (r *MailboxRepo) MarkRead(ctx context.Context, id, userID string) error {
	return r.db.WithContext(ctx).Model(&model.MailboxItem{}).
		Where("id = ? AND user_id = ?", id, userID).
		Update("is_read", true).Error
}

// Archive 归档单条消息
func (r *MailboxRepo) Archive(ctx context.Context, id, userID string) error {
	return r.db.WithContext(ctx).Model(&model.MailboxItem{}).
		Where("id = ? AND user_id = ?", id, userID).
		Update("is_archived", true).Error
}

// BatchMarkRead 批量标记已读
func (r *MailboxRepo) BatchMarkRead(ctx context.Context, ids []string, userID string) error {
	return r.db.WithContext(ctx).Model(&model.MailboxItem{}).
		Where("id IN ? AND user_id = ?", ids, userID).
		Update("is_read", true).Error
}
