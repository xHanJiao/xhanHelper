package service

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"xhan-helper/internal/model"
	"xhan-helper/internal/util"
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

// GetByIDCols 按 ID 查单条消息，可指定查询列（跳过 RequestJSON 等大字段）。
// 不传 cols 时默认只查常用字段：id, conversation_id, user_id, role, content, created_at
func (r *MessageRepo) GetByIDCols(ctx context.Context, id string, cols ...string) (*model.Message, error) {
	if len(cols) == 0 {
		cols = []string{"id", "conversation_id", "user_id", "role", "content", "created_at"}
	}
	var m model.Message
	err := r.db.WithContext(ctx).
		Select(cols).
		Where("id = ? AND status = ?", id, 1).
		First(&m).Error
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
		Where("conversation_id = ? AND status = ?", convID, model.MsgStatusNormal).
		Order("created_at ASC").Limit(limit).
		Find(&list).Error
	return list, err
}

// ListByConversationRecent 获取会话最近 N 条消息，按时间正序返回
func (r *MessageRepo) ListByConversationRecent(ctx context.Context, convID string, limit int) ([]model.Message, error) {
	var list []model.Message
	err := r.db.WithContext(ctx).
		Where("conversation_id = ? AND status = ?", convID, model.MsgStatusNormal).
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
		Where("conversation_id = ? AND status = ?", convID, model.MsgStatusNormal)
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

// ListRecentRounds 取最近 N 轮，每轮是一个 user 消息及其后所有消息（直到下一个 user）
func (r *MessageRepo) ListRecentRounds(ctx context.Context, convID string, n int) ([][]model.Message, error) {
	// 1. 找到最近 N 条 user 消息
	var userMsgs []model.Message
	err := r.db.WithContext(ctx).
		Where("conversation_id = ? AND role = 'user' AND status = ?", convID, model.MsgStatusNormal).
		Order("created_at DESC").Limit(n).
		Find(&userMsgs).Error
	if err != nil || len(userMsgs) == 0 {
		return nil, err
	}

	// 还原时间正序
	for i, j := 0, len(userMsgs)-1; i < j; i, j = i+1, j-1 {
		userMsgs[i], userMsgs[j] = userMsgs[j], userMsgs[i]
	}

	return r.buildRoundsFromUserMsgs(ctx, userMsgs)
}

// GetRoundsByUserMsgIDs 根据一批 user 消息 ID 取出完整轮（不限制会话，支持跨会话检索）
func (r *MessageRepo) GetRoundsByUserMsgIDs(ctx context.Context, userMsgIDs []string) ([][]model.Message, error) {
	if len(userMsgIDs) == 0 {
		return nil, nil
	}
	var userMsgs []model.Message
	err := r.db.WithContext(ctx).
		Where("id IN ? AND status = ?", userMsgIDs, model.MsgStatusNormal).
		Order("created_at ASC").
		Find(&userMsgs).Error
	if err != nil {
		return nil, err
	}
	return r.buildRoundsFromUserMsgs(ctx, userMsgs)
}

// buildRoundsFromUserMsgs 给定已排序的 user 消息列表，并发取出每轮的完整消息。
// 每条 user 消息使用其自身的 ConversationID，支持跨会话的轮组装。
//
// 为什么用逐轮查询 + 并发，而不是一次全量拉取？
// 上游 userMsgs 来自「最近 N 轮 + 向量检索 Top-M 轮」混合策略，命中点可能稀疏分布在消息历史中。
// 一次 WHERE created_at >= earliestTime 会拖出最早到最晚之间的所有消息，读放大严重。
// 逐轮查询每轮有 < nextUserMsg.CreatedAt 的紧上限 + Limit(20)，结果集小且可控；
// 再通过 LoadConcurrent 以最大并发度 5 并行，总延迟接近单次查询，但没有读放大。
func (r *MessageRepo) buildRoundsFromUserMsgs(ctx context.Context, userMsgs []model.Message) ([][]model.Message, error) {
	if len(userMsgs) == 0 {
		return nil, nil
	}

	tasks := make([]func(ctx context.Context) ([]model.Message, error), len(userMsgs))
	for i, um := range userMsgs {
		idx := i
		userMsg := um
		tasks[idx] = func(taskCtx context.Context) ([]model.Message, error) {
			q := r.db.WithContext(taskCtx).
				Where("conversation_id = ? AND created_at >= ? AND status = ?", userMsg.ConversationID, userMsg.CreatedAt, model.MsgStatusNormal)
			// 仅当相邻的下一条 user 消息同属一个会话时才用它的时间作为上界
			if idx+1 < len(userMsgs) && userMsgs[idx+1].ConversationID == userMsg.ConversationID {
				q = q.Where("created_at < ?", userMsgs[idx+1].CreatedAt)
			}
			var msgs []model.Message
			err := q.Order("created_at ASC").Limit(30).Find(&msgs).Error
			return msgs, err
		}
	}

	return util.LoadConcurrent(ctx, 5, tasks)
}

// SearchUserMessages 按关键词搜索用户的 role=user 消息（跨会话），按时间倒序
// convID 为空时跨会话搜索，keyword 使用 SQL LIKE 模糊匹配
func (r *MessageRepo) SearchUserMessages(ctx context.Context, userID, keyword, convID string, limit, offset int) ([]model.Message, int64, error) {
	if limit <= 0 {
		limit = 20
	}
	q := r.db.WithContext(ctx).Model(&model.Message{}).
		Where("user_id = ? AND role = 'user' AND content LIKE ? AND status = ?", userID, "%"+keyword+"%", 1)
	if convID != "" {
		q = q.Where("conversation_id = ?", convID)
	}

	var total int64
	q.Count(&total)

	var list []model.Message
	if err := q.Order("created_at DESC").Limit(limit).Offset(offset).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// GetMessagesSince 获取从指定时间开始的消息（同会话，按时间正序），用于启发式获取一轮对话
func (r *MessageRepo) GetMessagesSince(ctx context.Context, convID string, since time.Time, limit int) ([]model.Message, error) {
	if limit <= 0 {
		limit = 50
	}
	var list []model.Message
	err := r.db.WithContext(ctx).
		Where("conversation_id = ? AND created_at >= ? AND status = ?", convID, since, 1).
		Order("created_at ASC").Limit(limit).
		Find(&list).Error
	return list, err
}

// ListRecentUserMessages 获取会话最近 N 条 role=user 消息，按时间倒序
func (r *MessageRepo) ListRecentUserMessages(ctx context.Context, convID string, limit int) ([]model.Message, error) {
	var list []model.Message
	err := r.db.WithContext(ctx).
		Where("conversation_id = ? AND role = 'user' AND status = ?", convID, model.MsgStatusNormal).
		Order("created_at DESC").Limit(limit).
		Find(&list).Error
	return list, err
}

// CountRecent 统计用户最近 role=user 的消息数
func (r *MessageRepo) CountRecent(ctx context.Context, userID string, since time.Time) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.Message{}).
		Where("user_id = ? AND created_at >= ? AND role = 'user'", userID, since).
		Count(&count).Error
	return count, err
}

// CountUserMessagesBetween 统计指定时间范围内会话的 user role 消息数
func (r *MessageRepo) CountUserMessagesBetween(ctx context.Context, convID string, after, before time.Time) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.Message{}).
		Where("conversation_id = ? AND role = 'user' AND created_at > ? AND created_at < ? AND status = ?",
			convID, after, before, model.MsgStatusNormal).
		Count(&count).Error
	return count, err
}

// ListUserMessagesBefore 查询指定时间之前会话的 user role 消息，取最近的 limit 条（按时间正序返回）。
// 内部用 DESC+反转，确保冷启动时取到窗口外最近的消息而非最老的。
func (r *MessageRepo) ListUserMessagesBefore(ctx context.Context, convID string, before time.Time, limit int) ([]model.Message, error) {
	if limit <= 0 {
		limit = 50
	}
	var list []model.Message
	err := r.db.WithContext(ctx).
		Where("conversation_id = ? AND role = 'user' AND created_at < ? AND status = ?",
			convID, before, model.MsgStatusNormal).
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

// ListUserMessagesBetween 查询指定时间范围内会话的 user role 消息（按时间正序），最多 limit 条
func (r *MessageRepo) ListUserMessagesBetween(ctx context.Context, convID string, after, before time.Time, limit int) ([]model.Message, error) {
	if limit <= 0 {
		limit = 50
	}
	var list []model.Message
	err := r.db.WithContext(ctx).
		Where("conversation_id = ? AND role = 'user' AND created_at > ? AND created_at < ? AND status = ?",
			convID, after, before, model.MsgStatusNormal).
		Order("created_at ASC").Limit(limit).
		Find(&list).Error
	if err != nil {
		return nil, err
	}
	return list, nil
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
