package service

import (
	"context"
	"strings"
	"time"

	"gorm.io/gorm"

	"xhan-helper/internal/model"
)

// NoteRepo — 笔记数据访问
type NoteRepo struct {
	db *gorm.DB
}

// NewNoteRepo 创建 NoteRepo
func NewNoteRepo(db *gorm.DB) *NoteRepo {
	return &NoteRepo{db: db}
}

// Create 创建笔记
func (r *NoteRepo) Create(ctx context.Context, n *model.Note) error {
	return r.db.WithContext(ctx).Create(n).Error
}

// GetByID 按 ID + 用户 ID 查询单条笔记（仅正常状态）
func (r *NoteRepo) GetByID(ctx context.Context, id, userID string) (*model.Note, error) {
	var note model.Note
	err := r.db.WithContext(ctx).Where("id = ? AND user_id = ? AND status = ?", id, userID, model.NoteStatusNormal).First(&note).Error
	if err != nil {
		return nil, err
	}
	return &note, nil
}

// GetByIDs 按 ID 列表查询（仅正常状态）
func (r *NoteRepo) GetByIDs(ctx context.Context, ids []string) ([]model.Note, error) {
	var list []model.Note
	err := r.db.WithContext(ctx).Where("id IN ? AND status = ?", ids, model.NoteStatusNormal).Find(&list).Error
	return list, err
}

// GetByIDsAndUser 按 ID 列表 + 用户 ID 查询（仅正常状态）
func (r *NoteRepo) GetByIDsAndUser(ctx context.Context, ids []string, userID string) ([]model.Note, error) {
	var list []model.Note
	err := r.db.WithContext(ctx).Where("id IN ? AND user_id = ? AND status = ?", ids, userID, model.NoteStatusNormal).Find(&list).Error
	return list, err
}

// ListAllPaginated 分页获取所有正常状态笔记（用于 warmup），limit<=0 时默认 100
func (r *NoteRepo) ListAllPaginated(ctx context.Context, limit, offset int) ([]model.Note, error) {
	if limit <= 0 {
		limit = 100
	}
	var list []model.Note
	err := r.db.WithContext(ctx).Where("status = ?", model.NoteStatusNormal).Limit(limit).Offset(offset).Find(&list).Error
	return list, err
}

// SoftDelete 软删除笔记（设置 status=2）
func (r *NoteRepo) SoftDelete(ctx context.Context, id, userID string) error {
	return r.db.WithContext(ctx).Model(&model.Note{}).Where("id = ? AND user_id = ?", id, userID).
		Update("status", model.NoteStatusCancelled).Error
}

// HardDelete 硬删除笔记
func (r *NoteRepo) HardDelete(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Delete(&model.Note{}, "id = ?", id).Error
}

// DeleteByIDs 批量硬删除笔记（批量清理场景）。
func (r *NoteRepo) DeleteByIDs(ctx context.Context, ids []string) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	res := r.db.WithContext(ctx).Where("id IN ?", ids).Delete(&model.Note{})
	return res.RowsAffected, res.Error
}

// Update 更新笔记，仅更新非空字段
func (r *NoteRepo) Update(ctx context.Context, id, userID, topic, content, tags string, variables *string) error {
	updates := map[string]interface{}{}
	if topic != "" {
		updates["topic"] = topic
	}
	if content != "" {
		updates["content"] = content
	}
	if tags != "" {
		updates["tags"] = tags
	}
	if variables != nil {
		updates["variables"] = variables
	}
	if len(updates) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Model(&model.Note{}).Where("id = ? AND user_id = ?", id, userID).Updates(updates).Error
}

// RenameTopic 把一组旧 topic 批量改名为新 topic，返回受影响行数（仅正常状态）。
func (r *NoteRepo) RenameTopic(ctx context.Context, userID string, from []string, to string) (int64, error) {
	if len(from) == 0 || to == "" {
		return 0, nil
	}
	res := r.db.WithContext(ctx).Model(&model.Note{}).
		Where("user_id = ? AND status = ? AND topic IN ?", userID, model.NoteStatusNormal, from).
		Update("topic", to)
	return res.RowsAffected, res.Error
}

// List 按条件分页查询，limit<=0 时默认 50（仅正常状态）
func (r *NoteRepo) List(ctx context.Context, userID, topic, keyword string, limit, offset int) ([]model.Note, int64, error) {
	if limit <= 0 {
		limit = 50
	}
	q := r.db.WithContext(ctx).Where("user_id = ? AND status = ?", userID, model.NoteStatusNormal)
	if topic != "" {
		q = q.Where("topic = ?", topic)
	}
	if keyword != "" {
		like := "%" + keyword + "%"
		q = q.Where("content LIKE ? OR tags LIKE ? OR topic LIKE ?", like, like, like)
	}

	var total int64
	if err := q.Model(&model.Note{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var list []model.Note
	err := q.Order("created_at DESC").Limit(limit).Offset(offset).Find(&list).Error
	return list, total, err
}

// CountRecent 统计用户最近的正常状态笔记数
func (r *NoteRepo) CountRecent(ctx context.Context, userID string, since time.Time) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.Note{}).
		Where("user_id = ? AND status = ? AND created_at >= ?", userID, model.NoteStatusNormal, since).
		Count(&count).Error
	return count, err
}

// DistinctUserIDs 返回有笔记的所有 user_id（供存量清洗工具枚举用户）。
func (r *NoteRepo) DistinctUserIDs(ctx context.Context) ([]string, error) {
	var ids []string
	err := r.db.WithContext(ctx).Model(&model.Note{}).
		Distinct().Pluck("user_id", &ids).Error
	return ids, err
}

// DistinctTopics 获取用户所有不重复的笔记主题（仅正常状态）
func (r *NoteRepo) DistinctTopics(ctx context.Context, userID string) ([]string, error) {
	var topics []string
	err := r.db.WithContext(ctx).Model(&model.Note{}).
		Where("user_id = ? AND status = ?", userID, model.NoteStatusNormal).
		Distinct("topic").Pluck("topic", &topics).Error
	return topics, err
}

// NoteTopicCount — topic + 笔记数量
type NoteTopicCount struct {
	Topic string `json:"topic"`
	Count int64  `json:"count"`
}

// DistinctTopicsWithCount 获取用户所有不重复 topic 及对应笔记数量，按 count 倒序。
func (r *NoteRepo) DistinctTopicsWithCount(ctx context.Context, userID string) ([]NoteTopicCount, error) {
	var results []NoteTopicCount
	err := r.db.WithContext(ctx).Model(&model.Note{}).
		Select("topic, COUNT(*) AS count").
		Where("user_id = ? AND status = ?", userID, model.NoteStatusNormal).
		Group("topic").Order("count DESC").
		Find(&results).Error
	if err != nil {
		return nil, err
	}
	return results, nil
}

// SearchNotesByCursor 按关键词搜索笔记，cursor 分页（DESC，最新在前）。
// keywords OR LIKE，topic 精确过滤，after/before/cursor 为 nil 时跳过。
func (r *NoteRepo) SearchNotesByCursor(ctx context.Context, userID string, keywords []string, topic string, after, before *time.Time, cursor *time.Time, limit int) ([]model.Note, bool, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}

	q := r.db.WithContext(ctx).Model(&model.Note{}).Where("user_id = ? AND status = ?", userID, model.NoteStatusNormal)

	if topic != "" {
		q = q.Where("topic = ?", topic)
	}
	if len(keywords) > 0 {
		var ors []string
		var args []any
		for _, kw := range keywords {
			kw = strings.TrimSpace(kw)
			if kw == "" {
				continue
			}
			ors = append(ors, "content LIKE ? OR tags LIKE ?")
			args = append(args, "%"+kw+"%", "%"+kw+"%")
		}
		if len(ors) > 0 {
			q = q.Where("("+strings.Join(ors, " OR ")+")", args...)
		}
	}
	if after != nil {
		q = q.Where("created_at > ?", *after)
	}
	if before != nil {
		q = q.Where("created_at < ?", *before)
	}
	if cursor != nil {
		q = q.Where("created_at < ?", *cursor)
	}

	var list []model.Note
	if err := q.Order("created_at DESC").Limit(limit + 1).Find(&list).Error; err != nil {
		return nil, false, err
	}

	hasMore := len(list) > limit
	if hasMore {
		list = list[:limit]
	}
	return list, hasMore, nil
}

// CountNotes 统计符合条件的笔记总数。支持 keyword+topic+time。
func (r *NoteRepo) CountNotes(ctx context.Context, userID string, keywords []string, topic string, after, before *time.Time) (int64, error) {
	q := r.db.WithContext(ctx).Model(&model.Note{}).Where("user_id = ? AND status = ?", userID, model.NoteStatusNormal)

	if topic != "" {
		q = q.Where("topic = ?", topic)
	}
	if len(keywords) > 0 {
		var ors []string
		var args []any
		for _, kw := range keywords {
			kw = strings.TrimSpace(kw)
			if kw == "" {
				continue
			}
			ors = append(ors, "content LIKE ? OR tags LIKE ?")
			args = append(args, "%"+kw+"%", "%"+kw+"%")
		}
		if len(ors) > 0 {
			q = q.Where("("+strings.Join(ors, " OR ")+")", args...)
		}
	}
	if after != nil {
		q = q.Where("created_at > ?", *after)
	}
	if before != nil {
		q = q.Where("created_at < ?", *before)
	}

	var count int64
	if err := q.Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}
