package service

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"gorm.io/gorm"
	"xhan-helper/internal/model"
)

// ConversationRepo — 会话数据访问
type ConversationRepo struct {
	db *gorm.DB
}

// NewConversationRepo 创建 ConversationRepo
func NewConversationRepo(db *gorm.DB) *ConversationRepo {
	return &ConversationRepo{db: db}
}

// DB 暴露底层 gorm.DB
func (r *ConversationRepo) DB() *gorm.DB {
	return r.db
}

// ListQuiet 查找静默会话：有未提取的消息且超时无活动
func (r *ConversationRepo) ListQuiet(ctx context.Context, cutoff time.Time, limit int) ([]model.Conversation, error) {
	var list []model.Conversation
	err := r.db.WithContext(ctx).
		Where("updated_at < ? AND is_deleted = ?", cutoff, false).
		Where(`last_extracted_msg_id IS NULL
			OR last_extracted_msg_id != (
				SELECT id FROM messages
				WHERE conversation_id = conversations.id
				ORDER BY created_at DESC LIMIT 1
			)`).
		Order("updated_at ASC").
		Limit(limit).
		Find(&list).Error
	return list, err
}

// Create 创建新会话
func (r *ConversationRepo) Create(ctx context.Context, c *model.Conversation) error {
	return r.db.WithContext(ctx).Create(c).Error
}

// FindByID 根据 ID 查找会话
func (r *ConversationRepo) FindByID(ctx context.Context, id string) (*model.Conversation, error) {
	var c model.Conversation
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&c).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &c, err
}

// FindDefaultByUser 查找用户的默认会话，无则返回 nil
func (r *ConversationRepo) FindDefaultByUser(ctx context.Context, userID string) (*model.Conversation, error) {
	var c model.Conversation
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND is_default = ? AND is_deleted = ?", userID, true, false).
		First(&c).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &c, err
}

// ListByUser 获取用户未删除的会话列表（已 pin 置顶并按 pin 时间倒序，其余按更新时间倒序）。
// 排除子会话（subAgent，parent_id 非空）——子会话不进用户会话列表。
// 注：MySQL/SQLite 均视 NULL 为最小值，故 pinned_at DESC 天然把已 pin 排在前、未 pin（NULL）排在后。
func (r *ConversationRepo) ListByUser(ctx context.Context, userID string, limit, offset int) ([]model.Conversation, int64, error) {
	return r.listByUser(ctx, userID, limit, offset, false)
}

// ListArchivedByUser 获取用户已归档（is_deleted=true）的会话列表，排序同 ListByUser。
func (r *ConversationRepo) ListArchivedByUser(ctx context.Context, userID string, limit, offset int) ([]model.Conversation, int64, error) {
	return r.listByUser(ctx, userID, limit, offset, true)
}

func (r *ConversationRepo) listByUser(ctx context.Context, userID string, limit, offset int, archived bool) ([]model.Conversation, int64, error) {
	if limit <= 0 {
		limit = 50
	}
	q := r.db.WithContext(ctx).Where("user_id = ? AND is_deleted = ? AND parent_id IS NULL", userID, archived)
	var total int64
	if err := q.Model(&model.Conversation{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.Conversation
	err := q.Order("pinned_at DESC, updated_at DESC").Limit(limit).Offset(offset).Find(&list).Error
	return list, total, err
}

// Rename 重命名会话
func (r *ConversationRepo) Rename(ctx context.Context, id, title string) error {
	return r.db.WithContext(ctx).Model(&model.Conversation{}).
		Where("id = ?", id).Update("title", title).Error
}

// SetPinned 设置/取消会话置顶。pinned=true 记当前时间为 pin 时间，false 清空。
// 用 UpdateColumn 避免 GORM 自动刷新 updated_at（取消置顶后应回到原更新时间的位置）。
func (r *ConversationRepo) SetPinned(ctx context.Context, id string, pinned bool) error {
	var pinnedAt *time.Time
	if pinned {
		now := time.Now()
		pinnedAt = &now
	}
	return r.db.WithContext(ctx).Model(&model.Conversation{}).
		Where("id = ?", id).UpdateColumn("pinned_at", pinnedAt).Error
}

// Update 更新会话字段（仅写非零值字段，避免 Save 归零 IsDeleted 等）
func (r *ConversationRepo) Update(ctx context.Context, c *model.Conversation) error {
	return r.db.WithContext(ctx).Model(&model.Conversation{}).Where("id = ?", c.ID).Updates(c).Error
}

// SetAgentID 绑定智能体到会话（竞态保护：仅当目标会话尚未绑定 agent 时才写入）
func (r *ConversationRepo) SetAgentID(ctx context.Context, convID, agentID string) error {
	return r.db.WithContext(ctx).Model(&model.Conversation{}).
		Where("id = ? AND (agent_id IS NULL OR agent_id = '')", convID).
		Update("agent_id", agentID).Error
}

// UpdateAgentID 更新或清空会话的 agent_id（支持切换和解绑）
func (r *ConversationRepo) UpdateAgentID(ctx context.Context, convID string, agentID *string) error {
	return r.db.WithContext(ctx).Model(&model.Conversation{}).
		Where("id = ?", convID).Update("agent_id", agentID).Error
}

// SetMode 切换会话模式（work/plan）。模式只能由用户/客户端切换，LLM 无控制权。
func (r *ConversationRepo) SetMode(ctx context.Context, convID, mode string) error {
	return r.db.WithContext(ctx).Model(&model.Conversation{}).
		Where("id = ?", convID).Update("mode", mode).Error
}

// CountActive 统计指定时间段内有活动的会话数
func (r *ConversationRepo) CountActive(ctx context.Context, since time.Duration) int {
	var count int64
	cutoff := time.Now().Add(-since)
	r.db.WithContext(ctx).Model(&model.Conversation{}).
		Where("updated_at >= ? AND is_deleted = ?", cutoff, false).
		Count(&count)
	return int(count)
}

// UpdateExtra 仅更新会话的 extra 字段，不动 updated_at
func (r *ConversationRepo) UpdateExtra(ctx context.Context, convID, extra string) error {
	return r.db.WithContext(ctx).Model(&model.Conversation{}).
		Where("id = ?", convID).Update("extra", extra).Error
}

// Touch 更新会话的 updated_at 到当前时间
func (r *ConversationRepo) Touch(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Model(&model.Conversation{}).Where("id = ?", id).Update("updated_at", time.Now()).Error
}

// SoftDelete 软删除会话。
func (r *ConversationRepo) SoftDelete(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Model(&model.Conversation{}).
		Where("id = ?", id).Update("is_deleted", true).Error
}

// Restore 恢复已归档（软删）的会话。
func (r *ConversationRepo) Restore(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Model(&model.Conversation{}).
		Where("id = ?", id).Update("is_deleted", false).Error
}

// ArchiveStale 将无活动超过 cutoff 的会话批量归档（is_deleted=true）。
// 排除默认会话（客户端需保留可发消息的默认会话）与已置顶会话（用户显式保留）。
// 单条 UPDATE，无逐行循环。返回受影响行数。
func (r *ConversationRepo) ArchiveStale(ctx context.Context, cutoff time.Time) (int64, error) {
	res := r.db.WithContext(ctx).Model(&model.Conversation{}).
		Where("is_deleted = ? AND is_default = ? AND pinned_at IS NULL AND updated_at < ?", false, false, cutoff).
		Update("is_deleted", true)
	return res.RowsAffected, res.Error
}

// IsEphemeral 读取会话的无痕标记。会话不存在时返回 (false, nil)。
func (r *ConversationRepo) IsEphemeral(ctx context.Context, convID string) (bool, error) {
	extra, err := r.GetExtra(ctx, convID)
	if err != nil || extra == nil {
		return false, err
	}
	return extra.Ephemeral, nil
}

// ListScopeIDs 返回会话及其 parent_id 子会话的 ID（不过滤 is_deleted：
// 无痕销毁 Stage A 会先软删，Stage B 仍需重新推导范围）。
func (r *ConversationRepo) ListScopeIDs(ctx context.Context, convID string) ([]string, error) {
	var subs []string
	if err := r.db.WithContext(ctx).Model(&model.Conversation{}).
		Where("parent_id = ?", convID).Pluck("id", &subs).Error; err != nil {
		return nil, err
	}
	return append([]string{convID}, subs...), nil
}

// BeginPurge 无痕销毁 Stage A：立即隐藏会话及其消息，并清理会话域副表。
// 单事务。found=false 表示会话不存在（幂等 no-op）。
// 返回被纳入销毁的会话 ID（自身+子会话），供调用方清理内存向量。
func (r *ConversationRepo) BeginPurge(ctx context.Context, convID string) (scope []string, found bool, err error) {
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var c model.Conversation
		if e := tx.Where("id = ?", convID).First(&c).Error; e != nil {
			if errors.Is(e, gorm.ErrRecordNotFound) {
				return nil
			}
			return e
		}
		found = true
		var subs []string
		if e := tx.Model(&model.Conversation{}).Where("parent_id = ?", convID).Pluck("id", &subs).Error; e != nil {
			return e
		}
		scope = append([]string{convID}, subs...)
		if e := tx.Model(&model.Conversation{}).Where("id IN ?", scope).
			Update("is_deleted", true).Error; e != nil {
			return e
		}
		if e := tx.Model(&model.Message{}).Where("conversation_id IN ?", scope).
			Update("status", model.MsgStatusDeleted).Error; e != nil {
			return e
		}
		if e := tx.Where("conversation_id IN ?", scope).Delete(&model.ConversationSummary{}).Error; e != nil {
			return e
		}
		if e := tx.Where("conversation_id IN ?", scope).Delete(&model.ConversationPlan{}).Error; e != nil {
			return e
		}
		if e := tx.Where("conversation_id IN ?", scope).Delete(&model.MessageShare{}).Error; e != nil {
			return e
		}
		return nil
	})
	return
}

// PurgeMessageBatch 无痕销毁 Stage B：删除一批消息及其向量，返回本批行数。
// 每批独立事务（非全局原子），靠 WHERE 谓词幂等续跑。
func (r *ConversationRepo) PurgeMessageBatch(ctx context.Context, scope []string, limit int) (int, error) {
	if len(scope) == 0 {
		return 0, nil
	}
	if limit <= 0 {
		limit = 300
	}
	var count int
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var ids []string
		if e := tx.Model(&model.Message{}).Where("conversation_id IN ?", scope).
			Order("created_at ASC").Limit(limit).Pluck("id", &ids).Error; e != nil {
			return e
		}
		if len(ids) == 0 {
			return nil
		}
		if e := tx.Where("entity_type = ? AND entity_id IN ?", model.EntityTypeMessage, ids).
			Delete(&model.Embedding{}).Error; e != nil {
			return e
		}
		if e := tx.Where("id IN ?", ids).Delete(&model.Message{}).Error; e != nil {
			return e
		}
		count = len(ids)
		return nil
	})
	return count, err
}

// FinishPurge 无痕销毁 Stage B 收尾：硬删会话行（最后一步，保证可恢复）。
func (r *ConversationRepo) FinishPurge(ctx context.Context, scope []string) error {
	if len(scope) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Where("id IN ?", scope).Delete(&model.Conversation{}).Error
}

// GetExtra — 读取会话的 extra 数据
func (r *ConversationRepo) GetExtra(ctx context.Context, convID string) (*model.ConversationExtra, error) {
	var c model.Conversation
	if err := r.db.WithContext(ctx).Where("id = ?", convID).Select("extra").First(&c).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}

	var extra model.ConversationExtra
	if c.Extra == "" || c.Extra == "{}" {
		return &extra, nil
	}

	if err := json.Unmarshal([]byte(c.Extra), &extra); err != nil {
		return nil, err
	}

	return &extra, nil
}

// SaveExtra — 保存会话的 extra 数据
func (r *ConversationRepo) SaveExtra(ctx context.Context, convID string, extra *model.ConversationExtra) error {
	var extraJSON string
	if extra != nil {
		b, err := json.Marshal(extra)
		if err != nil {
			return err
		}
		extraJSON = string(b)
	} else {
		extraJSON = "{}"
	}

	return r.db.WithContext(ctx).Model(&model.Conversation{}).
		Where("id = ?", convID).Update("extra", extraJSON).Error
}

// AddUsedTool — 记录使用过的工具
func (r *ConversationRepo) AddUsedTool(ctx context.Context, convID, toolID string) error {
	extra, err := r.GetExtra(ctx, convID)
	if err != nil {
		return err
	}
	if extra == nil {
		extra = &model.ConversationExtra{}
	}

	// 去重
	exists := false
	for _, id := range extra.UsedToolIDs {
		if id == toolID {
			exists = true
			break
		}
	}
	if !exists {
		extra.UsedToolIDs = append(extra.UsedToolIDs, toolID)
		return r.SaveExtra(ctx, convID, extra)
	}
	return nil
}

// UpdateExtraField — 事务内读取最新 extra，调用 fn 修改后写回。
// 读-改-写在单事务内完成，避免并发写入覆盖其它字段（对齐 mergeMsgExtra 的单字段合并语义）。
// fn 内只改自己关心的字段，其它字段原样保留。
func (r *ConversationRepo) UpdateExtraField(ctx context.Context, convID string, fn func(extra *model.ConversationExtra)) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var c model.Conversation
		if err := tx.Where("id = ?", convID).Select("extra").First(&c).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return gorm.ErrRecordNotFound
			}
			return err
		}

		var extra model.ConversationExtra
		if c.Extra != "" && c.Extra != "{}" {
			if err := json.Unmarshal([]byte(c.Extra), &extra); err != nil {
				return err
			}
		}

		fn(&extra)
		b, err := json.Marshal(&extra)
		if err != nil {
			return err
		}
		return tx.Model(&model.Conversation{}).Where("id = ?", convID).Update("extra", string(b)).Error
	})
}

// GetUsedToolIDs — 获取使用过的工具 id 列表
func (r *ConversationRepo) GetUsedToolIDs(ctx context.Context, convID string) ([]string, error) {
	extra, err := r.GetExtra(ctx, convID)
	if err != nil {
		return nil, err
	}
	if extra == nil {
		return []string{}, nil
	}
	return extra.UsedToolIDs, nil
}

// SubConversationRow — 子会话列表行（含消息数）。
// msg_count 来自 SQL 子查询，非模型字段。
type SubConversationRow struct {
	model.Conversation
	MsgCount int64 `gorm:"column:msg_count"`
}

// ListSubConversationIDs 返回某父会话下全部子会话 ID（未删除）。
func (r *ConversationRepo) ListSubConversationIDs(ctx context.Context, parentID string) ([]string, error) {
	if parentID == "" {
		return nil, nil
	}
	var ids []string
	err := r.db.WithContext(ctx).
		Model(&model.Conversation{}).
		Where("parent_id = ? AND is_deleted = ?", parentID, false).
		Pluck("id", &ids).Error
	return ids, err
}

// ListSubConversations 分页列出子会话（parent_id IS NOT NULL），按创建时间倒序。
// 返回 (rows, hasMore)。消息数用一条子查询聚合，避免 N+1。
func (r *ConversationRepo) ListSubConversations(ctx context.Context, limit, offset int) ([]SubConversationRow, bool, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	var rows []SubConversationRow
	err := r.db.WithContext(ctx).
		Table("conversations").
		Select("conversations.*, (SELECT COUNT(*) FROM messages m WHERE m.conversation_id = conversations.id AND m.status = ?) AS msg_count", model.MsgStatusNormal).
		Where("parent_id IS NOT NULL AND is_deleted = ?", false).
		Order("created_at DESC").
		Limit(limit + 1).Offset(offset).
		Scan(&rows).Error
	if err != nil {
		return nil, false, err
	}
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	return rows, hasMore, nil
}
