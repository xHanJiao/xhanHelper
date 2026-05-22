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

// ListByUser 获取设备未删除的会话列表（按更新时间倒序）
func (r *ConversationRepo) ListByUser(ctx context.Context, userID string, limit, offset int) ([]model.Conversation, int64, error) {
	if limit <= 0 {
		limit = 50
	}
	q := r.db.WithContext(ctx).Where("user_id = ? AND is_deleted = ?", userID, false)
	var total int64
	if err := q.Model(&model.Conversation{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.Conversation
	err := q.Order("updated_at DESC").Limit(limit).Offset(offset).Find(&list).Error
	return list, total, err
}

// Rename 重命名会话
func (r *ConversationRepo) Rename(ctx context.Context, id, title string) error {
	return r.db.WithContext(ctx).Model(&model.Conversation{}).
		Where("id = ?", id).Update("title", title).Error
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

// SoftDelete 软删除会话
func (r *ConversationRepo) SoftDelete(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Model(&model.Conversation{}).
		Where("id = ?", id).Update("is_deleted", true).Error
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
