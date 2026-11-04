package service

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"xhan-helper/internal/idgen"
	"xhan-helper/internal/model"
)

// EventRepo — 事件数据访问。
//
// 事件不存正文，只存名字 + summary；正文是 user_memories.event_id 指向它的那些记忆。
// 因此合并/删除必须与记忆的 event_id 变更放在同一个事务里，否则记忆会变成
// 「两边都不注入」的隐形数据（MemorySource 只查 event_id IS NULL，EventSource 按 event_id 分组）。
type EventRepo struct {
	db *gorm.DB
}

func NewEventRepo(db *gorm.DB) *EventRepo {
	return &EventRepo{db: db}
}

// Create 新建事件；ID 为空时自动生成。
func (r *EventRepo) Create(ctx context.Context, e *model.Event) error {
	if e.ID == "" {
		e.ID = idgen.Next()
	}
	return r.db.WithContext(ctx).Create(e).Error
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
func (r *EventRepo) List(ctx context.Context, userID string, includeDeleted bool) ([]model.Event, error) {
	var list []model.Event
	q := r.db.WithContext(ctx).Where("user_id = ?", userID)
	if !includeDeleted {
		q = q.Where("is_deleted = ?", false)
	}
	err := q.Order("last_memory_at DESC").Find(&list).Error
	return list, err
}

// ListUpdatedSince 最近 since 之后有记忆写入的未删除事件（拼装候选、注入候选用）。
func (r *EventRepo) ListUpdatedSince(ctx context.Context, userID string, since time.Time) ([]model.Event, error) {
	var list []model.Event
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND is_deleted = ? AND last_memory_at >= ?", userID, false, since).
		Order("last_memory_at DESC").
		Find(&list).Error
	return list, err
}

// UpdateSummary 更新事件名与摘要。name 为空则保持原名；summary 为空则保持原摘要。
func (r *EventRepo) UpdateSummary(ctx context.Context, userID, id, name, summary string) error {
	updates := map[string]any{}
	if name != "" {
		updates["name"] = name
	}
	if summary != "" {
		updates["summary"] = summary
	}
	if len(updates) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Model(&model.Event{ID: id}).
		Where("user_id = ? AND is_deleted = ?", userID, false).
		Updates(updates).Error
}

// SetLastMemoryAt 把事件的「最后记忆时间」推进到 t（只增不减，避免乱序写入把它拉回去）。
func (r *EventRepo) SetLastMemoryAt(ctx context.Context, userID, id string, t time.Time) error {
	return r.db.WithContext(ctx).Model(&model.Event{ID: id}).
		Where("user_id = ? AND (last_memory_at IS NULL OR last_memory_at < ?)", userID, t).
		Update("last_memory_at", t).Error
}

// AssignMemories 把一批记忆挂到事件上（覆盖式：允许从别的条目改挂过来）。
func (r *EventRepo) AssignMemories(ctx context.Context, userID, eventID string, memoryIDs []string) error {
	if len(memoryIDs) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Model(&model.UserMemory{}).
		Where("id IN ? AND user_id = ?", memoryIDs, userID).
		Update("event_id", eventID).Error
}

// Merge 合并事件：from 的记忆全部搬到 to，然后把 from 软删。
// 重复合并同一条是幂等的（from 已删则直接返回）。
func (r *EventRepo) Merge(ctx context.Context, userID, fromID, toID string) error {
	if fromID == "" || toID == "" {
		return errors.New("event merge: from/to 不能为空")
	}
	if fromID == toID {
		return errors.New("event merge: from 与 to 相同")
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var events []model.Event
		if err := tx.Where("id IN ? AND user_id = ?", []string{fromID, toID}, userID).
			Find(&events).Error; err != nil {
			return err
		}
		if len(events) != 2 {
			return errors.New("event merge: 事件不存在或不属于该用户")
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
		if to.IsDeleted {
			return errors.New("event merge: 目标事件已删除")
		}
		if from.IsDeleted {
			return nil // 幂等：已经合并过
		}
		if err := tx.Model(&model.UserMemory{}).
			Where("event_id = ? AND user_id = ?", fromID, userID).
			Update("event_id", toID).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.Event{ID: fromID}).
			Where("user_id = ?", userID).
			Update("is_deleted", true).Error; err != nil {
			return err
		}
		// 目标事件的最后记忆时间取两者较大值。
		if from.LastMemoryAt != nil && (to.LastMemoryAt == nil || from.LastMemoryAt.After(*to.LastMemoryAt)) {
			if err := tx.Model(&model.Event{ID: toID}).
				Where("user_id = ?", userID).
				Update("last_memory_at", from.LastMemoryAt).Error; err != nil {
				return err
			}
		}
		return nil
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
		err := tx.Where("id = ? AND user_id = ?", id, userID).First(&e).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errors.New("event delete: 事件不存在")
		}
		if err != nil {
			return err
		}
		if e.IsDeleted {
			return nil // 幂等
		}
		if err := tx.Model(&model.UserMemory{}).
			Where("event_id = ? AND user_id = ?", id, userID).
			Update("event_id", nil).Error; err != nil {
			return err
		}
		return tx.Model(&model.Event{ID: id}).
			Where("user_id = ?", userID).
			Update("is_deleted", true).Error
	})
}
