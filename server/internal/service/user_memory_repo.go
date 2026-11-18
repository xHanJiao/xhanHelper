package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"

	"xhan-helper/internal/activity"
	"xhan-helper/internal/model"
	"xhan-helper/internal/util"
)

// UserMemoryRepo — 用户记忆数据访问
type UserMemoryRepo struct {
	db *gorm.DB
	// activity 仅供 BatchDelete 显式埋点（批量删除是唯一拿不到逐行 PK 的例外）；
	// 其余变更事件由 activity.RegisterChangeCallbacks 的回调统一发。
	activity *activity.Store
}

// NewUserMemoryRepo 创建 UserMemoryRepo
func NewUserMemoryRepo(db *gorm.DB) *UserMemoryRepo {
	return &UserMemoryRepo{db: db}
}

// SetActivity 注入活动埋点 sink（nil = 不采集）。
func (r *UserMemoryRepo) SetActivity(s *activity.Store) { r.activity = s }

// Create 创建新记忆
func (r *UserMemoryRepo) Create(ctx context.Context, m *model.UserMemory) error {
	return r.db.WithContext(ctx).Create(m).Error
}

// ListByUser 分页获取用户记忆，支持关键词/来源/topic 过滤，limit<=0 时默认 50
func (r *UserMemoryRepo) ListByUser(ctx context.Context, userID, keyword, source, topic string, limit, offset int) ([]model.UserMemory, int64, error) {
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
	if topic != "" {
		q = q.Where("topic = ?", topic)
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
	return r.db.WithContext(ctx).Delete(&model.UserMemory{ID: id}).Error
}

// BatchDelete 批量删除记忆（按 ID + 用户过滤）。
// 一条 SQL 删多行，变更回调拿不到逐行 PK，故显式抑制回调并自行逐条发事件。
func (r *UserMemoryRepo) BatchDelete(ctx context.Context, ids []string, userID string) error {
	if len(ids) == 0 {
		return nil
	}
	// 先取实际命中的 id：用户可控的 req.IDs 里可能混入不存在/他人的 id，
	// 对没删掉的 id 发事件就是幻影（回调路径靠 RowsAffected==0 防，这条自埋点路径得自己防）。
	var hit []string
	if err := r.db.WithContext(ctx).Model(&model.UserMemory{}).
		Where("id IN ? AND user_id = ?", ids, userID).
		Pluck("id", &hit).Error; err != nil {
		return err
	}
	if err := r.db.WithContext(activity.WithNoEvent(ctx)).
		Where("id IN ? AND user_id = ?", ids, userID).Delete(&model.UserMemory{}).Error; err != nil {
		return err
	}
	for _, id := range hit {
		r.activity.EmitChange(ctx, activity.TypeMemoryEntityChanged, "", map[string]any{
			"memory_id": id,
			"op":        "delete",
		})
	}
	return nil
}

// PurgeBySourceConvs 删除这些会话写下的记忆，返回被删的 id（供清理派生数据）。
// 无痕销毁专用：系统内部清理，不发实体变更事件（与 BeginPurge 的处理一致）。
func (r *UserMemoryRepo) PurgeBySourceConvs(ctx context.Context, convIDs []string) ([]string, error) {
	if len(convIDs) == 0 {
		return nil, nil
	}
	var ids []string
	if err := r.db.WithContext(ctx).Model(&model.UserMemory{}).
		Where("source_conv_id IN ?", convIDs).Pluck("id", &ids).Error; err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}
	if err := r.db.WithContext(activity.WithNoEvent(ctx)).
		Where("id IN ?", ids).Delete(&model.UserMemory{}).Error; err != nil {
		return nil, err
	}
	return ids, nil
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

// ListAllUsersMemoriesPaginated 分页获取所有用户的记忆（仅用于 warmup 加载向量库），limit<=0 时默认 100
func (r *UserMemoryRepo) ListAllUsersMemoriesPaginated(ctx context.Context, limit, offset int) ([]model.UserMemory, error) {
	if limit <= 0 {
		limit = 100
	}
	var list []model.UserMemory
	err := r.db.WithContext(ctx).Limit(limit).Offset(offset).Find(&list).Error
	return list, err
}

// ListUpdatedSince 返回 since 之后创建或改写的记忆（事件拼装输入）。
// updated_at 是「记忆内容变了」的唯一时间信号：创建、Update、BatchUpdateConfirm 都会刷新它；
// 事件归组本身用 UpdateColumn，不会刷新（否则同一 session 重试会重复进入拼装）。
func (r *UserMemoryRepo) ListUpdatedSince(ctx context.Context, userID string, since time.Time) ([]model.UserMemory, error) {
	var list []model.UserMemory
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND updated_at >= ?", userID, since).
		Order("updated_at ASC").
		Find(&list).Error
	return list, err
}

// ListBySourcesAfter 按 created_at 正序分页取指定来源的记忆（一次性冷启动用）。
// after 非零时只取严格晚于 (after, afterID) 的行，用 id 做同秒 tiebreaker，保证断点续跑不重不漏。
func (r *UserMemoryRepo) ListBySourcesAfter(ctx context.Context, userID string, sources []string, after *time.Time, afterID string, limit int) ([]model.UserMemory, error) {
	if len(sources) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = 40
	}
	q := r.db.WithContext(ctx).Where("user_id = ? AND source IN ?", userID, sources)
	if after != nil {
		q = q.Where("created_at > ? OR (created_at = ? AND id > ?)", *after, *after, afterID)
	}
	var list []model.UserMemory
	err := q.Order("created_at ASC, id ASC").Limit(limit).Find(&list).Error
	return list, err
}

// CountBySources 统计指定来源的记忆总数（冷启动报告的基数）。
func (r *UserMemoryRepo) CountBySources(ctx context.Context, userID string, sources []string) (int64, error) {
	if len(sources) == 0 {
		return 0, nil
	}
	var n int64
	err := r.db.WithContext(ctx).Model(&model.UserMemory{}).
		Where("user_id = ? AND source IN ?", userID, sources).
		Count(&n).Error
	return n, err
}

// ListByEventIDs 返回挂在指定事件下的记忆（拼装候选打分用，按事件分组由调用方做）。
func (r *UserMemoryRepo) ListByEventIDs(ctx context.Context, userID string, eventIDs []string) ([]model.UserMemory, error) {
	if len(eventIDs) == 0 {
		return nil, nil
	}
	var list []model.UserMemory
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND event_id IN ?", userID, eventIDs).
		Find(&list).Error
	return list, err
}

// CountByEventIDs 按事件聚合记忆数（只查 event_id + COUNT，不拉正文大字段）。
func (r *UserMemoryRepo) CountByEventIDs(ctx context.Context, userID string, eventIDs []string) (map[string]int, error) {
	if len(eventIDs) == 0 {
		return map[string]int{}, nil
	}
	var rows []struct {
		EventID string
		Count   int
	}
	err := r.db.WithContext(ctx).Model(&model.UserMemory{}).
		Select("event_id, COUNT(*) AS count").
		Where("user_id = ? AND event_id IN ?", userID, eventIDs).
		Group("event_id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	counts := make(map[string]int, len(rows))
	for _, row := range rows {
		counts[row.EventID] = row.Count
	}
	return counts, nil
}

// Update 覆盖一条记忆的内容（topic / memory_value / facts）。改内容不碰 confirm 计数。
// 派生数据（向量/chunk/概念）由调用方在写库后重建。
func (r *UserMemoryRepo) Update(ctx context.Context, id, topic, memoryValue string, facts *string) error {
	// 空 id 时 GORM 会丢弃 PK 条件 → 退化成全表更新（且本方法无 user_id 余件）。挡住 typo。
	if id == "" {
		return errors.New("memory id 为空")
	}
	return r.db.WithContext(ctx).Model(&model.UserMemory{ID: id}).
		Updates(map[string]interface{}{
			"topic":        topic,
			"memory_key":   topic,
			"memory_value": memoryValue,
			"facts":        facts,
			"content_hash": util.MD5Hex(memoryValue),
		}).Error
}

// BatchUpdateConfirm 批量更新确认计数（口径 B：被当轮新召回且注入最终上下文）
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

// SetRating 设置用户对某条记忆的反馈（0 未评 / 1 有用 / -1 没用）。按 user 归属更新，主键进 Model。
func (r *UserMemoryRepo) SetRating(ctx context.Context, userID, id string, rating int) error {
	if id == "" {
		return errors.New("memory id 为空")
	}
	return r.db.WithContext(ctx).Model(&model.UserMemory{ID: id}).
		Where("user_id = ?", userID).
		Update("rating", rating).Error
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

// RenameTopic 把一组旧 topic 批量改名为新 topic，返回受影响行数。
func (r *UserMemoryRepo) RenameTopic(ctx context.Context, userID string, from []string, to string) (int64, error) {
	if len(from) == 0 || to == "" {
		return 0, nil
	}
	res := r.db.WithContext(ctx).Model(&model.UserMemory{}).
		Where("user_id = ? AND topic IN ?", userID, from).
		Update("topic", to)
	return res.RowsAffected, res.Error
}

// DistinctUserIDs 返回有记忆的所有 user_id（供存量清洗工具枚举用户）。
func (r *UserMemoryRepo) DistinctUserIDs(ctx context.Context) ([]string, error) {
	var ids []string
	err := r.db.WithContext(ctx).Model(&model.UserMemory{}).
		Distinct().Pluck("user_id", &ids).Error
	return ids, err
}

// DistinctTopics 返回用户所有不重复 topic 及对应记忆数量，按 count 倒序。
// 有意排除空 topic（reuseTopic 候选集不该含空值）。存量清洗要连空 topic 一起收拾，
// 用 EmptyTopics 单独取。
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

// SetTopicByID 把单条记忆的 topic 设为 to（存量清洗填空 topic 用；按 id 定位）。
func (r *UserMemoryRepo) SetTopicByID(ctx context.Context, userID, id, to string) (int64, error) {
	if to == "" {
		return 0, nil
	}
	// 空 id 时 Model(&T{ID:""}) 丢 PK → 退化成该用户批量改 topic。挡 typo。
	if id == "" {
		return 0, nil
	}
	res := r.db.WithContext(ctx).Model(&model.UserMemory{ID: id}).
		Where("user_id = ?", userID).
		Update("topic", to)
	return res.RowsAffected, res.Error
}

// EmptyTopicMemory — 一条空 topic 记忆（存量清洗逐条起名用）。
type EmptyTopicMemory struct {
	ID      string
	Content string
}

// EmptyTopics 返回该用户所有空 topic 记忆的 id + 内容（供清洗逐条按内容起名）。
func (r *UserMemoryRepo) EmptyTopics(ctx context.Context, userID string, limit int) ([]EmptyTopicMemory, error) {
	if limit <= 0 {
		limit = 100
	}
	var out []EmptyTopicMemory
	err := r.db.WithContext(ctx).Model(&model.UserMemory{}).
		Select("id, memory_value AS content").
		Where("user_id = ? AND (topic IS NULL OR TRIM(topic) = '')", userID).
		Order("created_at DESC").Limit(limit).
		Scan(&out).Error
	return out, err
}
