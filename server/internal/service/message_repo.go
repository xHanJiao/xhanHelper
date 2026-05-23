package service

import (
	"context"
	"errors"
	"time"

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

// CountByRole 统计会话下指定角色的消息数
func (r *MessageRepo) CountByRole(ctx context.Context, convID, role string) (int, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.Message{}).
		Where("conversation_id = ? AND role = ? AND status = ?", convID, role, 1).
		Count(&count).Error
	return int(count), err
}

// SoftDelete 软删除消息（标记 status=2）
func (r *MessageRepo) SoftDelete(ctx context.Context, id, userID string) error {
	return r.db.WithContext(ctx).
		Model(&model.Message{}).
		Where("id = ? AND user_id = ? AND status = ?", id, userID, 1).
		Update("status", 2).Error
}

// GetByID 按 ID 查单条消息，未找到返回 (nil, nil)
func (r *MessageRepo) GetByID(ctx context.Context, id string) (*model.Message, error) {
	var m model.Message
	err := r.db.WithContext(ctx).Where("id = ? AND status = ?", id, 1).First(&m).Error
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
		Where("conversation_id = ? AND status = ?", convID, 1).
		Order("created_at ASC").Limit(limit).
		Find(&list).Error
	return list, err
}

// ListByConversationRecent 获取会话最近 N 条消息，按时间正序返回
func (r *MessageRepo) ListByConversationRecent(ctx context.Context, convID string, limit int) ([]model.Message, error) {
	var list []model.Message
	err := r.db.WithContext(ctx).
		Where("conversation_id = ? AND status = ?", convID, 1).
		Order("created_at DESC").Limit(limit).
		Find(&list).Error
	if err != nil {
		return nil, err
	}
	// 反转回时间正序
	for i, j := 0, len(list)-1; i < j; i, j = i+1, j-1 {
		list[i], list[j] = list[j], list[i]
	}
	return list, nil
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

// CursorResult — 游标分页结果
type CursorResult struct {
	Messages      []model.Message `json:"messages"`
	HasMoreBefore bool            `json:"has_more_before"`
	HasMoreAfter  bool            `json:"has_more_after"`
	OldestCursor  string          `json:"oldest_cursor"`
	NewestCursor  string          `json:"newest_cursor"`
}

// ListByConversationCursor 基于游标的分页查询
// cursor: 锚点消息 ID（空则从最新开始）
// direction: "before" 加载更旧消息，"after" 加载更新消息
func (r *MessageRepo) ListByConversationCursor(ctx context.Context, convID, cursor, direction string, limit int) (*CursorResult, error) {
	if limit <= 0 || limit > 50 {
		limit = 10
	}

	var anchorTime *time.Time
	if cursor != "" {
		var m model.Message
		err := r.db.WithContext(ctx).Where("id = ?", cursor).First(&m).Error
		if err == nil {
			anchorTime = &m.CreatedAt
		}
	}

	var messages []model.Message
	var err error

	if direction == "after" && anchorTime != nil {
		messages, err = r.loadAfter(ctx, convID, *anchorTime, limit)
	} else {
		messages, err = r.loadBefore(ctx, convID, anchorTime, limit)
	}
	if err != nil {
		return nil, err
	}

	result := &CursorResult{Messages: messages}
	if len(messages) > 0 {
		result.OldestCursor = messages[0].ID
		result.NewestCursor = messages[len(messages)-1].ID
		result.HasMoreBefore = r.existsBefore(ctx, convID, messages[0].CreatedAt)
		result.HasMoreAfter = r.existsAfter(ctx, convID, messages[len(messages)-1].CreatedAt)
	}
	return result, nil
}

func (r *MessageRepo) loadBefore(ctx context.Context, convID string, anchor *time.Time, limit int) ([]model.Message, error) {
	q := r.db.WithContext(ctx).
		Where("conversation_id = ? AND status = ?", convID, 1)
	if anchor != nil {
		q = q.Where("created_at < ?", *anchor)
	}
	// 取最近的 N 条较旧消息，先倒序再正过来
	var temp []model.Message
	err := q.Order("created_at DESC").Limit(limit).Find(&temp).Error
	if err != nil {
		return nil, err
	}
	// 反转为正序
	for i, j := 0, len(temp)-1; i < j; i, j = i+1, j-1 {
		temp[i], temp[j] = temp[j], temp[i]
	}
	return temp, nil
}

func (r *MessageRepo) loadAfter(ctx context.Context, convID string, anchor time.Time, limit int) ([]model.Message, error) {
	var list []model.Message
	err := r.db.WithContext(ctx).
		Where("conversation_id = ? AND created_at > ?", convID, anchor).
		Order("created_at ASC").Limit(limit).
		Find(&list).Error
	return list, err
}

func (r *MessageRepo) existsBefore(ctx context.Context, convID string, t time.Time) bool {
	var count int64
	r.db.WithContext(ctx).Model(&model.Message{}).
		Where("conversation_id = ? AND created_at < ?", convID, t).
		Count(&count)
	return count > 0
}

func (r *MessageRepo) existsAfter(ctx context.Context, convID string, t time.Time) bool {
	var count int64
	r.db.WithContext(ctx).Model(&model.Message{}).
		Where("conversation_id = ? AND created_at > ?", convID, t).
		Count(&count)
	return count > 0
}

// CountRecent 统计用户最近 role=user 的消息数
func (r *MessageRepo) CountRecent(ctx context.Context, userID string, since time.Time) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.Message{}).
		Where("user_id = ? AND created_at >= ? AND role = 'user'", userID, since).
		Count(&count).Error
	return count, err
}

// ListWithTrace 分页列出有 prompt trace 的 assistant 消息，按时间倒序
func (r *MessageRepo) ListWithTrace(ctx context.Context, userID string, limit, offset int) ([]model.Message, int64, error) {
	if limit <= 0 {
		limit = 20
	}
	var total int64
	q := r.db.WithContext(ctx).Model(&model.Message{}).
		Where("user_id = ? AND role = 'assistant' AND extra IS NOT NULL AND extra != ''", userID)
	q.Count(&total)

	var list []model.Message
	if err := q.Order("created_at DESC").Limit(limit).Offset(offset).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}
