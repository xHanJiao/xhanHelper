package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"xhan-helper/internal/idgen"
	"xhan-helper/internal/model"
)

// EventRepo — 事件数据访问。
//
// 事件不存正文，只存名字 + summary；正文是 user_memories.event_id 指向它的那些记忆。
// 因此合并/删除必须与记忆的 event_id 变更放在同一个事务里，否则记忆会变成
// 「两边都不注入」的隐形数据（MemorySource 只查 event_id IS NULL，EventSource 按 event_id 分组）。
//
// 另一个硬约束：**搬移记忆不能刷 updated_at**。updated_at 是事件层的核心时间信号
// （EventWorker 的输入范围、full 形态取最近 20 条、summary 重算判定都靠它），
// 而 GORM 的 Update/Updates 会自动写 AutoUpdateTime。所以改 event_id 一律用 UpdateColumn。
type EventRepo struct {
	db *gorm.DB
}

func NewEventRepo(db *gorm.DB) *EventRepo {
	return &EventRepo{db: db}
}

const (
	eventNameMaxRunes = 100
	// MaxEventSummaryRunes — 事件 summary 硬上限（设计 §4.4：≤200 字）。
	// 放在 service 里是为了让所有写入口（拼装、工具）共用同一口径。
	MaxEventSummaryRunes = 200
	eventListMaxLimit    = 200
)

// validateEventName 归一化并校验事件名（LLM 生成，MySQL 严格模式下超长会直接报错）。
func validateEventName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("event: name 不能为空")
	}
	if utf8.RuneCountInString(name) > eventNameMaxRunes {
		return "", fmt.Errorf("event: name 超过 %d 字", eventNameMaxRunes)
	}
	return name, nil
}

// Create 新建事件（不含记忆）；ID 为空时自动生成。
func (r *EventRepo) Create(ctx context.Context, e *model.Event) error {
	return r.create(r.db.WithContext(ctx), e)
}

// CreateWithMemories 新建事件并挂载记忆（同一事务），同时推进 last_memory_at。
// 拼装的主路径走这里：避免「事件建了但没挂上记忆」留下空壳。
// 注意：失败时事务回滚，但 e.ID 已经在事务内被赋值——调用方失败后不得使用该 ID。
func (r *EventRepo) CreateWithMemories(ctx context.Context, e *model.Event, memoryIDs []string) error {
	ids := uniqStrings(memoryIDs)
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := r.create(tx, e); err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		if err := assignMemories(tx, e.UserID, e.ID, ids); err != nil {
			return err
		}
		return advanceLastMemoryAt(tx, e.UserID, e.ID)
	})
}

func (r *EventRepo) create(tx *gorm.DB, e *model.Event) error {
	if e == nil {
		return errors.New("event: 空对象")
	}
	if strings.TrimSpace(e.UserID) == "" {
		return errors.New("event: user_id 不能为空")
	}
	name, err := validateEventName(e.Name)
	if err != nil {
		return err
	}
	e.Name = name
	if e.ID == "" {
		e.ID = idgen.Next()
	}
	return tx.Create(e).Error
}

// GetByID 按 ID + 用户查询。含已软删的（调用方自己看 IsDeleted，以便区分「已删除」和「不存在」）。
func (r *EventRepo) GetByID(ctx context.Context, userID, id string) (*model.Event, error) {
	var e model.Event
	err := r.db.WithContext(ctx).Where("id = ? AND user_id = ?", id, userID).First(&e).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &e, nil
}

// GetByIDs 批量按 ID 查询（含已软删的），返回 id → 事件。
func (r *EventRepo) GetByIDs(ctx context.Context, userID string, ids []string) (map[string]*model.Event, error) {
	if len(ids) == 0 {
		return map[string]*model.Event{}, nil
	}
	var list []model.Event
	err := r.db.WithContext(ctx).
		Where("id IN ? AND user_id = ?", ids, userID).
		Find(&list).Error
	if err != nil {
		return nil, err
	}
	m := make(map[string]*model.Event, len(list))
	for i := range list {
		m[list[i].ID] = &list[i]
	}
	return m, nil
}

// List 列出用户事件，最近记忆时间倒序。includeDeleted=true 时带上已软删的。
// limit<=0 时取默认 100；上限 200。
func (r *EventRepo) List(ctx context.Context, userID string, includeDeleted bool, limit, offset int) ([]model.Event, error) {
	limit = normalizeLimit(limit)
	var list []model.Event
	q := r.db.WithContext(ctx).Where("user_id = ?", userID)
	if !includeDeleted {
		q = q.Where("is_deleted = ?", false)
	}
	err := q.Order("last_memory_at DESC, id").Limit(limit).Offset(offset).Find(&list).Error
	return list, err
}

// Count 统计事件总数（列表分页用），口径与 List 一致。
func (r *EventRepo) Count(ctx context.Context, userID string, includeDeleted bool) (int64, error) {
	q := r.db.WithContext(ctx).Model(&model.Event{}).Where("user_id = ?", userID)
	if !includeDeleted {
		q = q.Where("is_deleted = ?", false)
	}
	var n int64
	err := q.Count(&n).Error
	return n, err
}

// ListUpdatedSince 最近 since 之后有记忆写入的未删除事件（拼装候选、注入候选用）。
func (r *EventRepo) ListUpdatedSince(ctx context.Context, userID string, since time.Time, limit int) ([]model.Event, error) {
	var list []model.Event
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND is_deleted = ? AND last_memory_at >= ?", userID, false, since).
		Order("last_memory_at DESC, id").
		Limit(normalizeLimit(limit)).
		Find(&list).Error
	return list, err
}

// UpdateSummary 更新事件名与摘要。name 为空则保持原名；summary 为空则保持原摘要
// （即无法用空串清空 summary）。事件不存在或已删时返回包装后的 gorm.ErrRecordNotFound，
// 避免拼装拿到过期 event_id 时静默丢更新。
func (r *EventRepo) UpdateSummary(ctx context.Context, userID, id, name, summary string) error {
	updates := map[string]any{}
	if strings.TrimSpace(name) != "" {
		n, err := validateEventName(name)
		if err != nil {
			return err
		}
		updates["name"] = n
	}
	if summary = strings.TrimSpace(summary); summary != "" {
		updates["summary"] = truncateEventSummary(summary)
	}
	if len(updates) == 0 {
		return nil
	}
	res := r.db.WithContext(ctx).Model(&model.Event{ID: id}).
		Where("user_id = ? AND is_deleted = ?", userID, false).
		Updates(updates)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		// 可能是并发软删，也可能是值未变化（MySQL 默认只报 changed rows）。
		// 再查一次存活，区分「已删/不存在」与「无变化」。
		ok, err := r.existsActive(ctx, userID, id)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("event update summary %s: %w", id, gorm.ErrRecordNotFound)
		}
	}
	return nil
}

// SetLastMemoryAt 把事件的「最后记忆时间」推进到 t（只增不减，避免乱序写入把它拉回去）。
func (r *EventRepo) SetLastMemoryAt(ctx context.Context, userID, id string, t time.Time) error {
	return r.db.WithContext(ctx).Model(&model.Event{ID: id}).
		Where("user_id = ? AND (last_memory_at IS NULL OR last_memory_at < ?)", userID, t).
		Update("last_memory_at", t).Error
}

// AssignMemories 把一批记忆挂到事件上（覆盖式：允许从别的条目改挂过来）。
//
// 目标事件必须是「本人 + 未删除」，否则记忆会挂到一个永远注入不到的事件上（隐形数据）。
// 记忆侧只按 id + user_id 过滤，跨用户的 ID 自然匹配不到（不会误挂），但会返回错误。
func (r *EventRepo) AssignMemories(ctx context.Context, userID, eventID string, memoryIDs []string) error {
	ids := uniqStrings(memoryIDs)
	if len(ids) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := assignMemories(tx, userID, eventID, ids); err != nil {
			return err
		}
		return advanceLastMemoryAt(tx, userID, eventID)
	})
}

// assignMemories 事务内版本：校验事件存活 + 校验记忆归属 + UpdateColumn（不刷 updated_at）。
func assignMemories(tx *gorm.DB, userID, eventID string, ids []string) error {
	if strings.TrimSpace(eventID) == "" {
		return errors.New("event assign: event_id 不能为空")
	}
	var e model.Event
	err := tx.Where("id = ? AND user_id = ? AND is_deleted = ?", eventID, userID, false).First(&e).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("event assign %s: 目标事件不存在或已删除: %w", eventID, gorm.ErrRecordNotFound)
	}
	if err != nil {
		return err
	}
	var matched int64
	if err := tx.Model(&model.UserMemory{}).
		Where("id IN ? AND user_id = ?", ids, userID).
		Count(&matched).Error; err != nil {
		return err
	}
	if matched != int64(len(ids)) {
		return fmt.Errorf("event assign %s: %d 条记忆不存在或不属于该用户", eventID, int64(len(ids))-matched)
	}
	return tx.Model(&model.UserMemory{}).
		Where("id IN ? AND user_id = ?", ids, userID).
		UpdateColumn("event_id", eventID).Error
}

// advanceLastMemoryAt 把事件的 last_memory_at 推进到其记忆的最大 updated_at。
// 事件没有记忆时不动（空事件不该排到最前）。
func advanceLastMemoryAt(tx *gorm.DB, userID, eventID string) error {
	var row model.UserMemory
	if err := tx.Select("updated_at").
		Where("event_id = ? AND user_id = ?", eventID, userID).
		Order("updated_at DESC").Limit(1).
		Find(&row).Error; err != nil {
		return err
	}
	if row.UpdatedAt.IsZero() {
		return nil
	}
	return tx.Model(&model.Event{ID: eventID}).
		Where("user_id = ? AND (last_memory_at IS NULL OR last_memory_at < ?)", userID, row.UpdatedAt).
		Update("last_memory_at", row.UpdatedAt).Error
}

// Merge 合并事件：from 的记忆全部搬到 to，然后把 from 软删。
// 重复合并同一条是幂等的（from 已删则直接返回）。
// MySQL 上对两个事件行加排他锁，避免 A→B 与 B→A 并发时把两边都软删。
func (r *EventRepo) Merge(ctx context.Context, userID, fromID, toID string) error {
	if fromID == "" || toID == "" {
		return errors.New("event merge: from/to 不能为空")
	}
	if fromID == toID {
		return errors.New("event merge: from 与 to 相同")
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var events []model.Event
		q := lockRows(tx)
		if err := q.Where("id IN ? AND user_id = ?", []string{fromID, toID}, userID).
			Find(&events).Error; err != nil {
			return err
		}
		if len(events) != 2 {
			return fmt.Errorf("event merge: 事件不存在或不属于该用户: %w", gorm.ErrRecordNotFound)
		}
		var from, to *model.Event
		for i := range events {
			switch events[i].ID {
			case fromID:
				from = &events[i]
			case toID:
				to = &events[i]
			}
		}
		// 幂等判断要在目标检查之前：from 已合并、to 之后又被删时，重试应直接成功。
		if from.IsDeleted {
			return nil
		}
		if to.IsDeleted {
			return errors.New("event merge: 目标事件已删除")
		}
		if err := tx.Model(&model.UserMemory{}).
			Where("event_id = ? AND user_id = ?", fromID, userID).
			UpdateColumn("event_id", toID).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.Event{ID: fromID}).
			Where("user_id = ?", userID).
			Update("is_deleted", true).Error; err != nil {
			return err
		}
		return advanceLastMemoryAt(tx, userID, toID)
	})
}

// Delete 软删事件：记忆退回普通池（event_id = NULL），事件置 is_deleted。
// 记忆必须回池，否则它们既不在普通记忆块、也不在事件块，等于静默消失。
func (r *EventRepo) Delete(ctx context.Context, userID, id string) error {
	if id == "" {
		return errors.New("event delete: id 为空")
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e model.Event
		err := lockRows(tx).Where("id = ? AND user_id = ?", id, userID).First(&e).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("event delete %s: %w", id, gorm.ErrRecordNotFound)
		}
		if err != nil {
			return err
		}
		if e.IsDeleted {
			return nil // 幂等
		}
		if err := tx.Model(&model.UserMemory{}).
			Where("event_id = ? AND user_id = ?", id, userID).
			UpdateColumn("event_id", nil).Error; err != nil {
			return err
		}
		return tx.Model(&model.Event{ID: id}).
			Where("user_id = ?", userID).
			Update("is_deleted", true).Error
	})
}

// ResetUser 清空一个用户的全部事件数据：记忆退回普通池（UpdateColumn，不刷 updated_at），
// 事件硬删。仅供一次性冷启动 --reset 使用；生产删除走 Delete（软删）。
//
// 不清理 Conversation.Extra.EventFocus：残留的旧 ID 在注入时被跳过，下一次 event.enable
// 会重新写整个列表，属于可自愈的残留。
func (r *EventRepo) ResetUser(ctx context.Context, userID string) (events int64, memories int64, err error) {
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&model.UserMemory{}).
			Where("user_id = ? AND event_id IS NOT NULL", userID).
			UpdateColumn("event_id", nil)
		if res.Error != nil {
			return res.Error
		}
		memories = res.RowsAffected
		del := tx.Where("user_id = ?", userID).Delete(&model.Event{})
		if del.Error != nil {
			return del.Error
		}
		events = del.RowsAffected
		return nil
	})
	return events, memories, err
}

// truncateEventSummary 截断到 MaxEventSummaryRunes，避免超长 summary 长期占注入预算。
func truncateEventSummary(s string) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= MaxEventSummaryRunes {
		return s
	}
	return string([]rune(s)[:MaxEventSummaryRunes])
}

func (r *EventRepo) existsActive(ctx context.Context, userID, id string) (bool, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&model.Event{}).
		Where("id = ? AND user_id = ? AND is_deleted = ?", id, userID, false).
		Count(&n).Error
	return n > 0, err
}

// lockRows 在支持行锁的方言上加 FOR UPDATE；SQLite（单测）不支持，直接跳过。
func lockRows(tx *gorm.DB) *gorm.DB {
	if tx.Dialector.Name() == "sqlite" {
		return tx
	}
	return tx.Clauses(clause.Locking{Strength: "UPDATE"})
}

func normalizeLimit(limit int) int {
	if limit <= 0 {
		return 100
	}
	if limit > eventListMaxLimit {
		return eventListMaxLimit
	}
	return limit
}

func uniqStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
