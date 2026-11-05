package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"gorm.io/gorm"

	"xhan-helper/internal/model"
	"xhan-helper/internal/util"
)

// DefaultCursorRounds — 游标分页默认轮数（轮级/条级通用）
const DefaultCursorRounds = 7

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

// SetArtifactsJSON — 把本轮产出的 HTML 产物引用挂到 assistant 消息上。
// 只在本轮结束时调一次（正文不在这里，见 docs/design/html-artifact §五）。
func (r *MessageRepo) SetArtifactsJSON(ctx context.Context, msgID, artifactsJSON string) error {
	if msgID == "" {
		return nil
	}
	return r.db.WithContext(ctx).Model(&model.Message{}).
		Where("id = ?", msgID).
		Update("artifacts_json", artifactsJSON).Error
}

// DebugJSONUpdate — 单条调试 JSON 落库更新（异步刷盘用）
type DebugJSONUpdate struct {
	MessageID   string
	RequestJSON *string
}

// BatchUpdateDebugJSON 单事务批量更新 request_json（异步刷盘用）。
// 任一条失败整个事务回滚（debug 数据 best-effort，不逐条兜底）。
func (r *MessageRepo) BatchUpdateDebugJSON(ctx context.Context, updates []DebugJSONUpdate) error {
	if len(updates) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, u := range updates {
			if err := tx.Model(&model.Message{}).
				Where("id = ?", u.MessageID).
				Update("request_json", u.RequestJSON).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// ClearDebugJSONBefore 清空 before 之前消息的 request_json（保留清理用，limit 控制单批行数）。
func (r *MessageRepo) ClearDebugJSONBefore(ctx context.Context, before time.Time, limit int) (int64, error) {
	if limit <= 0 {
		limit = 500
	}
	res := r.db.WithContext(ctx).Model(&model.Message{}).
		Where("created_at < ? AND request_json IS NOT NULL", before).
		Limit(limit).
		Update("request_json", nil)
	return res.RowsAffected, res.Error
}

// extraDebugKeys — message.extra 中的检索调试字段（保留清理时整键删除）。
// 功能字段（usage / window_size / session_start_msg_id / no_merge）必须保留，不得列入。
// prompt_trace 是旧格式的 trace 容器，内容仅 providers，整键删除安全。
var extraDebugKeys = []string{"providers", "user_message_prefix", "history_builder_msg_ids", "prompt_trace"}

// ClearExtraDebugBefore 清除 before 之前消息 extra 中的调试字段，保留功能字段，
// 返回本批实际改写的行数。JSON 处理在 Go 侧完成（map 键删除），兼容 SQLite/MySQL。
// 解析失败的脏数据行跳过并记日志，不删除、不报错；无匹配行返回 0。
func (r *MessageRepo) ClearExtraDebugBefore(ctx context.Context, before time.Time, limit int) (int64, error) {
	if limit <= 0 {
		limit = 500
	}
	var rows []struct {
		ID    string
		Extra *string
	}
	if err := r.db.WithContext(ctx).Model(&model.Message{}).
		Select("id, extra").
		Where("created_at < ? AND extra IS NOT NULL AND extra != '' AND "+
			"(extra LIKE ? OR extra LIKE ? OR extra LIKE ?)",
			before, `%"providers"%`, `%user_message_prefix%`, `%history_builder_msg_ids%`).
		Limit(limit).
		Find(&rows).Error; err != nil {
		return 0, fmt.Errorf("list messages with extra trace: %w", err)
	}
	if len(rows) == 0 {
		return 0, nil
	}
	var cleared int64
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, row := range rows {
			if row.Extra == nil || *row.Extra == "" {
				continue
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal([]byte(*row.Extra), &fields); err != nil {
				log.Printf("[extra_retention] msg %s extra unmarshal error: %v", row.ID, err)
				continue
			}
			changed := false
			for _, k := range extraDebugKeys {
				if _, ok := fields[k]; ok {
					delete(fields, k)
					changed = true
				}
			}
			if !changed {
				continue
			}
			b, err := json.Marshal(fields)
			if err != nil {
				log.Printf("[extra_retention] msg %s extra marshal error: %v", row.ID, err)
				continue
			}
			s := string(b)
			if err := tx.Model(&model.Message{}).Where("id = ?", row.ID).Update("extra", s).Error; err != nil {
				return fmt.Errorf("update msg %s extra: %w", row.ID, err)
			}
			cleared++
		}
		return nil
	})
	return cleared, err
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
		Where("conversation_id = ? AND created_at < ? AND status = ?", convID, t, model.MsgStatusNormal).
		Count(&count)
	return count > 0
}

func (r *MessageRepo) existsAfter(ctx context.Context, convID string, t time.Time) bool {
	var count int64
	r.db.WithContext(ctx).Model(&model.Message{}).
		Where("conversation_id = ? AND created_at > ? AND status = ?", convID, t, model.MsgStatusNormal).
		Count(&count)
	return count > 0
}

// ListByConversationCursorByRounds 基于游标的轮级分页。
// 不再逐轮并发查询（buildRoundsFromUserMsgs），改为两次查询：
//  1. 找到目标 user 消息的时间边界
//  2. 一次范围查询拉取区间内全部消息
//
// 大幅降低 SQL 并发度和读放大。
func (r *MessageRepo) ListByConversationCursorByRounds(ctx context.Context, convID, cursor, direction string, rounds int) (*CursorResult, error) {
	if rounds <= 0 || rounds > 50 {
		rounds = DefaultCursorRounds
	}

	var anchorTime *time.Time
	if cursor != "" {
		var m model.Message
		err := r.db.WithContext(ctx).Where("id = ?", cursor).First(&m).Error
		if err == nil {
			anchorTime = &m.CreatedAt
		}
	}

	if anchorTime != nil {
		return r.listRoundsWithCursor(ctx, convID, anchorTime, rounds)
	}
	return r.listRoundsNoCursor(ctx, convID, rounds)
}

// listRoundsNoCursor 无 cursor：拉取最新 N 轮消息。
// 查最新 N 条 user 消息 → 取最旧 user 的时间 → 一次范围查询拉该时间至今的全部消息。
func (r *MessageRepo) listRoundsNoCursor(ctx context.Context, convID string, rounds int) (*CursorResult, error) {
	userMsgs, err := r.loadUserMessagesBefore(ctx, convID, nil, rounds)
	if err != nil {
		return nil, err
	}
	if len(userMsgs) == 0 {
		return &CursorResult{}, nil
	}

	oldestTime := userMsgs[0].CreatedAt
	messages, err := r.loadMessagesInRange(ctx, convID, &oldestTime, nil, rounds*30, true) // 前端分页排除 tool 消息
	if err != nil {
		return nil, err
	}

	result := &CursorResult{Messages: messages}
	if len(messages) > 0 {
		result.OldestCursor = messages[0].ID
		result.NewestCursor = messages[len(messages)-1].ID
		result.HasMoreBefore = r.existsUserBefore(ctx, convID, oldestTime)
		result.HasMoreAfter = r.existsUserAfter(ctx, convID, userMsgs[len(userMsgs)-1].CreatedAt)
	}
	return result, nil
}

// listRoundsWithCursor 有 cursor：拉取 cursor 之前 N 轮消息。
// 查 cursor 之前 N+1 条 user 消息（+1 判 HasMoreBefore）→ 一次范围查询拉 [最旧user时间, cursor时间) 的全部消息。
func (r *MessageRepo) listRoundsWithCursor(ctx context.Context, convID string, cursorTime *time.Time, rounds int) (*CursorResult, error) {
	userMsgs, err := r.loadUserMessagesBefore(ctx, convID, cursorTime, rounds+1)
	if err != nil {
		return nil, err
	}
	if len(userMsgs) == 0 {
		return &CursorResult{}, nil
	}

	hasMoreBefore := false
	if len(userMsgs) > rounds {
		hasMoreBefore = true
		userMsgs = userMsgs[:rounds]
	}

	oldestTime := userMsgs[0].CreatedAt
	messages, err := r.loadMessagesInRange(ctx, convID, &oldestTime, cursorTime, rounds*30, true) // 前端分页排除 tool 消息
	if err != nil {
		return nil, err
	}

	result := &CursorResult{Messages: messages}
	if len(messages) > 0 {
		result.OldestCursor = messages[0].ID
		result.NewestCursor = messages[len(messages)-1].ID
		result.HasMoreBefore = hasMoreBefore
		result.HasMoreAfter = r.existsUserAfter(ctx, convID, userMsgs[len(userMsgs)-1].CreatedAt)
	}
	return result, nil
}

// ListMessagesBetweenMsgIDs 根据两条消息 ID 拉取范围内的全部正常消息（含 user/assistant/tool）。
// limit <= 0 时不限制条数。
func (r *MessageRepo) ListMessagesBetweenMsgIDs(ctx context.Context, convID, fromMsgID, toMsgID string, limit int) ([]model.Message, error) {
	from, err := r.GetByIDCols(ctx, fromMsgID, "id", "created_at")
	if err != nil || from == nil {
		return nil, err
	}
	to, err := r.GetByIDCols(ctx, toMsgID, "id", "created_at")
	if err != nil || to == nil {
		return nil, err
	}

	start, end := from.CreatedAt, to.CreatedAt
	if start.After(end) {
		start, end = end, start
	}

	q := r.db.WithContext(ctx).
		Where("conversation_id = ? AND status = ?", convID, model.MsgStatusNormal).
		Where("created_at >= ? AND created_at <= ?", start, end)

	var msgs []model.Message
	if limit > 0 {
		err = q.Order("created_at ASC").Limit(limit).Find(&msgs).Error
	} else {
		err = q.Order("created_at ASC").Find(&msgs).Error
	}
	return msgs, err
}

// loadMessagesInRange 拉取指定时间范围内的全部正常消息。
// loadMessagesInRange 拉取指定时间范围内的正常消息。
// excludeTool=true 时排除 role='tool' 消息（前端分页用：tool 结果最终被 cleanMessages
// 丢弃并注入 assistant，查询时直接过滤可避免巨型工具轮撑爆 limit 截断最新消息）。
func (r *MessageRepo) loadMessagesInRange(ctx context.Context, convID string, from, to *time.Time, limit int, excludeTool bool) ([]model.Message, error) {
	q := r.db.WithContext(ctx).
		Where("conversation_id = ? AND status = ?", convID, model.MsgStatusNormal)
	if excludeTool {
		q = q.Where("role != ?", "tool")
	}
	if from != nil {
		q = q.Where("created_at >= ?", *from)
	}
	if to != nil {
		q = q.Where("created_at < ?", *to)
	}
	var msgs []model.Message
	err := q.Order("created_at ASC").Limit(limit).Find(&msgs).Error
	return msgs, err
}

// loadUserMessagesBefore 查询最近 N 条 role=user 消息（时间 < anchor），按时间正序返回。
func (r *MessageRepo) loadUserMessagesBefore(ctx context.Context, convID string, anchor *time.Time, limit int) ([]model.Message, error) {
	q := r.db.WithContext(ctx).
		Where("conversation_id = ? AND role = 'user' AND status = ?", convID, model.MsgStatusNormal)
	if anchor != nil {
		q = q.Where("created_at < ?", *anchor)
	}
	var temp []model.Message
	err := q.Order("created_at DESC").Limit(limit).Find(&temp).Error
	if err != nil {
		return nil, err
	}
	for i, j := 0, len(temp)-1; i < j; i, j = i+1, j-1 {
		temp[i], temp[j] = temp[j], temp[i]
	}
	return temp, nil
}

// existsUserBefore 检查是否存在更早的 user 消息。
func (r *MessageRepo) existsUserBefore(ctx context.Context, convID string, t time.Time) bool {
	var count int64
	r.db.WithContext(ctx).Model(&model.Message{}).
		Where("conversation_id = ? AND role = 'user' AND created_at < ? AND status = ?", convID, t, model.MsgStatusNormal).
		Count(&count)
	return count > 0
}

// existsUserAfter 检查是否存在更新的 user 消息。
func (r *MessageRepo) existsUserAfter(ctx context.Context, convID string, t time.Time) bool {
	var count int64
	r.db.WithContext(ctx).Model(&model.Message{}).
		Where("conversation_id = ? AND role = 'user' AND created_at > ? AND status = ?", convID, t, model.MsgStatusNormal).
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

	return r.buildRoundsFromUserMsgs(ctx, userMsgs, nil)
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
	return r.buildRoundsFromUserMsgs(ctx, userMsgs, nil)
}

// buildRoundsFromUserMsgs 给定已排序的 user 消息列表，并发取出每轮的完整消息。
// 每条 user 消息使用其自身的 ConversationID，支持跨会话的轮组装。
// endTime 可选：不为 nil 时作为最后一个 user 消息的上界（避免末轮无界溢出到相邻批次）。
//
// 为什么用逐轮查询 + 并发，而不是一次全量拉取？
// 上游 userMsgs 来自「最近 N 轮 + 向量检索 Top-M 轮」混合策略，命中点可能稀疏分布在消息历史中。
// 一次 WHERE created_at >= earliestTime 会拖出最早到最晚之间的所有消息，读放大严重。
// 逐轮查询每轮有 < nextUserMsg.CreatedAt 的紧上限 + Limit(20)，结果集小且可控；
// 再通过 LoadConcurrent 以最大并发度 5 并行，总延迟接近单次查询，但没有读放大。
func (r *MessageRepo) buildRoundsFromUserMsgs(ctx context.Context, userMsgs []model.Message, endTime *time.Time) ([][]model.Message, error) {
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
			// 上界优先级：下一个 user 消息 > endTime 参数
			if idx+1 < len(userMsgs) && userMsgs[idx+1].ConversationID == userMsg.ConversationID {
				q = q.Where("created_at < ?", userMsgs[idx+1].CreatedAt)
			} else if endTime != nil {
				q = q.Where("created_at < ?", *endTime)
			}
			var msgs []model.Message
			// 无 Limit：每轮查询已有 created_at 紧上界（< 下一条 user 消息），
			// 拉取的是该轮真实消息数（巨型 ReAct 轮可达 150+ 条）——这是 LLM 后续推理必需的数据，
			// 设上限会截断轮尾（孤儿 tool_call、后续对话丢失该轮内容）。
			err := q.Order("created_at ASC").Find(&msgs).Error
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

// ListRecentUserMessageContents 只取最近 N 条 user 消息的正文，按时间倒序。
// 给「校验 facts 是不是用户原话」这类只需要正文的调用方用：不拉 xml_context / request_json
// 这些大字段（record_memory 是每轮对话都走的热路径）。
func (r *MessageRepo) ListRecentUserMessageContents(ctx context.Context, convID string, limit int) ([]string, error) {
	var out []string
	err := r.db.WithContext(ctx).Model(&model.Message{}).
		Where("conversation_id = ? AND role = 'user' AND status = ?", convID, model.MsgStatusNormal).
		Order("created_at DESC").Limit(limit).
		Pluck("content", &out).Error
	return out, err
}

// CountRecent 统计用户最近 role=user 的消息数（排除子会话消息）
func (r *MessageRepo) CountRecent(ctx context.Context, userID string, since time.Time) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.Message{}).
		Where("user_id = ? AND created_at >= ? AND role = 'user'", userID, since).
		Where(subConversationExclusion).
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

// ListUserMessagesBetween 查询指定时间范围内会话的 user role 消息（按时间正序）。
// after 和 before 为闭区间（含边界）。
// limit <= 0 表示不限制数量。
func (r *MessageRepo) ListUserMessagesBetween(ctx context.Context, convID string, after, before time.Time, limit int) ([]model.Message, error) {
	var list []model.Message
	q := r.db.WithContext(ctx).
		Where("conversation_id = ? AND role = 'user' AND created_at >= ? AND created_at <= ? AND status = ?",
			convID, after, before, model.MsgStatusNormal).
		Order("created_at ASC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	err := q.Find(&list).Error
	if err != nil {
		return nil, err
	}
	return list, nil
}

// ListChatMessagesBetween 查询指定时间范围内会话的真实对话消息（user + assistant），按时间正序。
// 排除 tool 消息（可能是巨型 longtext）与内部消息（verify 草稿 / 工具结果）。
// after 和 before 为闭区间（含边界）。limit <= 0 表示不限制数量。
func (r *MessageRepo) ListChatMessagesBetween(ctx context.Context, convID string, after, before time.Time, limit int) ([]model.Message, error) {
	var list []model.Message
	q := r.db.WithContext(ctx).
		Where("conversation_id = ? AND role IN ('user','assistant') AND is_internal = 0 AND created_at >= ? AND created_at <= ? AND status = ?",
			convID, after, before, model.MsgStatusNormal).
		Order("created_at ASC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	err := q.Find(&list).Error
	if err != nil {
		return nil, err
	}
	return list, nil
}

// buildMessageFilterQuery — 构造共享的 WHERE 过滤条件（user_id, status, role, keywords, convID, time range）。
// subConversationExclusion — 排除子会话（subAgent）消息的 SQL 条件。
// 子会话消息（MsgSink 落库时置 IsSubAgent=true）只服务于其任务本身，
// 不参与任何"按用户扫描"的后台统计/检索聚合（后台反思的活动统计、
// message_search 等），避免污染用户活动数据。
const subConversationExclusion = "is_sub_agent = 0"

func (r *MessageRepo) buildMessageFilterQuery(ctx context.Context, userID string, keywords []string, role, convID string, after, before *time.Time) *gorm.DB {
	q := r.db.WithContext(ctx).Model(&model.Message{}).
		Where("user_id = ? AND status = ?", userID, model.MsgStatusNormal).
		Where(subConversationExclusion)

	if role != "" {
		q = q.Where("role IN ?", util.SplitAndTrim(role))
	}
	if len(keywords) > 0 {
		var ors []string
		var args []any
		for _, kw := range keywords {
			kw = strings.TrimSpace(kw)
			if kw == "" {
				continue
			}
			ors = append(ors, "content LIKE ?")
			args = append(args, "%"+kw+"%")
		}
		if len(ors) > 0 {
			q = q.Where("("+strings.Join(ors, " OR ")+")", args...)
		}
	}
	if convID != "" {
		q = q.Where("conversation_id = ?", convID)
	}
	if after != nil {
		q = q.Where("created_at > ?", *after)
	}
	if before != nil {
		q = q.Where("created_at < ?", *before)
	}
	return q
}

// searchMessages — 在 buildMessageFilterQuery 基础上叠加 cursor + order + limit+1 分页。
func (r *MessageRepo) searchMessages(ctx context.Context, userID string, keywords []string, role, convID string, after, before *time.Time, cursor *time.Time, direction string, limit int) ([]model.Message, bool, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}

	q := r.buildMessageFilterQuery(ctx, userID, keywords, role, convID, after, before)

	isAsc := direction == "asc"

	if cursor != nil {
		if isAsc {
			q = q.Where("created_at > ?", *cursor)
		} else {
			q = q.Where("created_at < ?", *cursor)
		}
	}

	if isAsc {
		q = q.Order("created_at ASC")
	} else {
		q = q.Order("created_at DESC")
	}

	// limit+1 detect hasMore
	var list []model.Message
	if err := q.Limit(limit + 1).Find(&list).Error; err != nil {
		return nil, false, err
	}

	hasMore := len(list) > limit
	if hasMore {
		list = list[:limit]
	}
	return list, hasMore, nil
}

// SearchMessagesByCursor 按关键词搜索消息，cursor 分页（DESC，最新在前）。
// keywords 之间为 OR 关系。role 为空时不过滤 role（查全部角色），支持逗号分隔多 role。
// convID 为空时跨所有会话搜索。cursor 为 nil 表示首页。
func (r *MessageRepo) SearchMessagesByCursor(ctx context.Context, userID string, keywords []string, role, convID string, cursor *time.Time, limit int) ([]model.Message, bool, error) {
	return r.searchMessages(ctx, userID, keywords, role, convID, nil, nil, cursor, "desc", limit)
}

// SearchMessagesByTimeCursor 按时间范围搜索消息，cursor 分页。
// after/before 为 nil 表示不限制该方向。direction 为 "asc" 或 "desc"。cursor 为 nil 表示首页。
func (r *MessageRepo) SearchMessagesByTimeCursor(ctx context.Context, userID, role, convID string, after, before *time.Time, cursor *time.Time, direction string, limit int) ([]model.Message, bool, error) {
	return r.searchMessages(ctx, userID, nil, role, convID, after, before, cursor, direction, limit)
}

// CountMessages 统计符合条件的消息总数。支持 keyword+time+role+convID 全维度组合。
func (r *MessageRepo) CountMessages(ctx context.Context, userID string, keywords []string, role, convID string, after, before *time.Time) (int64, error) {
	var count int64
	err := r.buildMessageFilterQuery(ctx, userID, keywords, role, convID, after, before).Count(&count).Error
	if err != nil {
		return 0, err
	}
	return count, nil
}

// GetByIDs 批量按 ID 获取消息（同一用户范围，静默跳过不存在的）。
func (r *MessageRepo) GetByIDs(ctx context.Context, ids []string, userID string) ([]model.Message, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var list []model.Message
	if err := r.db.WithContext(ctx).
		Where("id IN ? AND user_id = ? AND status = ?", ids, userID, model.MsgStatusNormal).
		Order("created_at ASC").
		Find(&list).Error; err != nil {
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
		Where("user_id = ? AND role = 'assistant' AND extra LIKE ?", userID, `%"providers"%`)
	q.Count(&total)

	var list []model.Message
	if err := q.Order("created_at DESC").Limit(limit).Offset(offset).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}
