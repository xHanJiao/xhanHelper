package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"

	"xhan-helper/internal/model"
)

// UserMemoryRepo — 用户记忆数据访问
type UserMemoryRepo struct {
	db *gorm.DB
}

// NewUserMemoryRepo 创建 UserMemoryRepo
func NewUserMemoryRepo(db *gorm.DB) *UserMemoryRepo {
	return &UserMemoryRepo{db: db}
}

// Create 创建新记忆
func (r *UserMemoryRepo) Create(ctx context.Context, m *model.UserMemory) error {
	return r.db.WithContext(ctx).Create(m).Error
}

// FindByKey 按 key 查找已有记忆
func (r *UserMemoryRepo) FindByKey(ctx context.Context, userID, key string) (*model.UserMemory, error) {
	var m model.UserMemory
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND memory_key = ?", userID, key).
		First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &m, err
}

// ListByUser 分页获取用户记忆，支持关键词搜索，limit<=0 时默认 50
func (r *UserMemoryRepo) ListByUser(ctx context.Context, userID, keyword, source string, limit, offset int) ([]model.UserMemory, int64, error) {
	if limit <= 0 {
		limit = 50
	}
	q := r.db.WithContext(ctx).Where("user_id = ?", userID)
	if keyword != "" {
		like := "%" + keyword + "%"
		q = q.Where("memory_value LIKE ? OR memory_key LIKE ?", like, like)
	}
	if source != "" {
		q = q.Where("source = ?", source)
	}

	var total int64
	if err := q.Model(&model.UserMemory{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var list []model.UserMemory
	err := q.Order("created_at DESC").Limit(limit).Offset(offset).Find(&list).Error
	return list, total, err
}

// FindByID 按 ID 查单条记忆
func (r *UserMemoryRepo) FindByID(ctx context.Context, userID, id string) (*model.UserMemory, error) {
	var m model.UserMemory
	err := r.db.WithContext(ctx).Where("id = ? AND user_id = ?", id, userID).First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &m, err
}

// Delete 删除记忆
func (r *UserMemoryRepo) Delete(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Delete(&model.UserMemory{}, "id = ?", id).Error
}

// BatchDelete 批量删除记忆（按 ID + 用户过滤）
func (r *UserMemoryRepo) BatchDelete(ctx context.Context, ids []string, userID string) error {
	return r.db.WithContext(ctx).Where("id IN ? AND user_id = ?", ids, userID).Delete(&model.UserMemory{}).Error
}

// GetByIDs 按 ID 列表查询
func (r *UserMemoryRepo) GetByIDs(ctx context.Context, ids []string) ([]model.UserMemory, error) {
	var list []model.UserMemory
	err := r.db.WithContext(ctx).Where("id IN ?", ids).Find(&list).Error
	return list, err
}

// GetByIDsAndUser 按 ID 列表 + 用户 ID 查询
func (r *UserMemoryRepo) GetByIDsAndUser(ctx context.Context, ids []string, userID string) ([]model.UserMemory, error) {
	var list []model.UserMemory
	err := r.db.WithContext(ctx).Where("id IN ? AND user_id = ?", ids, userID).Find(&list).Error
	return list, err
}

// FindByPromptHash 按 prompt 哈希查找已有记忆（输入去重）
func (r *UserMemoryRepo) FindByPromptHash(ctx context.Context, userID, hash string) (*model.UserMemory, error) {
	var m model.UserMemory
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND prompt_hash = ?", userID, hash).
		First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &m, err
}

// ListAllUsersMemoriesPaginated 分页获取所有用户的记忆（仅用于 warmup 加载向量库），limit<=0 时默认 100
func (r *UserMemoryRepo) ListAllUsersMemoriesPaginated(ctx context.Context, limit, offset int) ([]model.UserMemory, error) {
	if limit <= 0 {
		limit = 100
	}
	var list []model.UserMemory
	err := r.db.WithContext(ctx).Limit(limit).Offset(offset).Find(&list).Error
	return list, err
}

// FindByContentHash 按 content_hash 查找记忆
func (r *UserMemoryRepo) FindByContentHash(ctx context.Context, userID, contentHash string) (*model.UserMemory, error) {
	var m model.UserMemory
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND content_hash = ?", userID, contentHash).
		First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &m, err
}

// UpdateConfirm 更新确认计数
func (r *UserMemoryRepo) UpdateConfirm(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Model(&model.UserMemory{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"confirm_count":     gorm.Expr("confirm_count + 1"),
			"last_confirmed_at": gorm.Expr("CURRENT_TIMESTAMP"),
		}).Error
}

// BatchUpdateConfirm 批量更新确认计数
func (r *UserMemoryRepo) BatchUpdateConfirm(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Model(&model.UserMemory{}).
		Where("id IN ?", ids).
		Updates(map[string]interface{}{
			"confirm_count":     gorm.Expr("confirm_count + 1"),
			"last_confirmed_at": gorm.Expr("CURRENT_TIMESTAMP"),
		}).Error
}

// CountRecent 统计用户最近的记忆数
func (r *UserMemoryRepo) CountRecent(ctx context.Context, userID string, since time.Time) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.UserMemory{}).
		Where("user_id = ? AND created_at >= ?", userID, since).
		Count(&count).Error
	return count, err
}

// TopicCount — topic + 计数结果
type TopicCount struct {
	Topic string `json:"topic"`
	Count int64  `json:"count"`
}

// SearchMemoriesByCursor 按关键词搜索记忆，cursor 分页（DESC，最新在前）。
// keywords 之间为 OR 关系。topic 为空时不过滤。after/before 为 nil 时不过滤时间。cursor 为 nil 表示首页。
func (r *UserMemoryRepo) SearchMemoriesByCursor(ctx context.Context, userID string, keywords []string, topic string, after, before *time.Time, cursor *time.Time, limit int) ([]model.UserMemory, bool, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}

	q := r.db.WithContext(ctx).Model(&model.UserMemory{}).Where("user_id = ?", userID)

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
			ors = append(ors, "memory_value LIKE ?")
			args = append(args, "%"+kw+"%")
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

	var list []model.UserMemory
	if err := q.Order("created_at DESC").Limit(limit + 1).Find(&list).Error; err != nil {
		return nil, false, err
	}

	hasMore := len(list) > limit
	if hasMore {
		list = list[:limit]
	}
	return list, hasMore, nil
}

// CountMemories 统计符合条件的记忆总数。支持 keyword+topic+time 全维度。
func (r *UserMemoryRepo) CountMemories(ctx context.Context, userID string, keywords []string, topic string, after, before *time.Time) (int64, error) {
	q := r.db.WithContext(ctx).Model(&model.UserMemory{}).Where("user_id = ?", userID)

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
			ors = append(ors, "memory_value LIKE ?")
			args = append(args, "%"+kw+"%")
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

// DistinctTopics 返回用户所有不重复 topic 及对应记忆数量，按 count 倒序。
func (r *UserMemoryRepo) DistinctTopics(ctx context.Context, userID string) ([]TopicCount, error) {
	var results []TopicCount
	err := r.db.WithContext(ctx).Model(&model.UserMemory{}).
		Select("topic, COUNT(*) AS count").
		Where("user_id = ? AND topic != ''", userID).
		Group("topic").Order("count DESC").
		Find(&results).Error
	if err != nil {
		return nil, err
	}
	return results, nil
}
