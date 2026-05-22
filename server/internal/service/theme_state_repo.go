package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"gorm.io/gorm"

	"xhan-helper/internal/config"
	"xhan-helper/internal/model"
)

// ThemeStateRepo — 主题状态页数据访问
type ThemeStateRepo struct {
	db *gorm.DB
}

func NewThemeStateRepo(db *gorm.DB) *ThemeStateRepo {
	return &ThemeStateRepo{db: db}
}

// Create 创建主题
func (r *ThemeStateRepo) Create(ctx context.Context, t *model.ThemeState) error {
	return r.db.WithContext(ctx).Create(t).Error
}

// GetByID 按 ID 查询（含用户校验）
func (r *ThemeStateRepo) GetByID(ctx context.Context, id, userID string) (*model.ThemeState, error) {
	var t model.ThemeState
	err := r.db.WithContext(ctx).Where("id = ? AND user_id = ?", id, userID).First(&t).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &t, err
}

// GetByName 按名称 + 用户查询（名称唯一性由应用层保证）
func (r *ThemeStateRepo) GetByName(ctx context.Context, userID, name string) (*model.ThemeState, error) {
	var t model.ThemeState
	err := r.db.WithContext(ctx).Where("user_id = ? AND name = ?", userID, name).First(&t).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &t, err
}

// ListActive 列出用户所有活跃（非关闭）主题
func (r *ThemeStateRepo) ListActive(ctx context.Context, userID string) ([]model.ThemeState, error) {
	var list []model.ThemeState
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND status != ?", userID, "closed").
		Order("created_at ASC").
		Find(&list).Error
	return list, err
}

// UpdateState 更新状态页文本 + 最后运行时间，旧内容 append 到 history
func (r *ThemeStateRepo) UpdateState(ctx context.Context, id, userID, stateText string) error {
	now := time.Now()
	// 先读取旧值
	var old model.ThemeState
	if err := r.db.WithContext(ctx).Where("id = ? AND user_id = ?", id, userID).First(&old).Error; err != nil {
		return err
	}

	history := old.HistoryStateText
	entry := fmt.Sprintf("--- %s ---\n%s\n", now.In(config.BJ).Format("2006-01-02 15:04"), old.StateText)
	if history != nil && *history != "" {
		combined := *history + "\n" + entry
		history = &combined
	} else {
		history = &entry
	}

	return r.db.WithContext(ctx).Model(&model.ThemeState{}).
		Where("id = ? AND user_id = ?", id, userID).
		Updates(map[string]interface{}{
			"state_text":         stateText,
			"history_state_text": history,
			"last_run_at":        now,
		}).Error
}

// ListByStatus 按状态列表查询
func (r *ThemeStateRepo) ListByStatus(ctx context.Context, userID string, statuses []string) ([]model.ThemeState, error) {
	var list []model.ThemeState
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND status IN ?", userID, statuses).
		Order("created_at ASC").
		Find(&list).Error
	return list, err
}

// UpdateStatus 更新主题状态
func (r *ThemeStateRepo) UpdateStatus(ctx context.Context, id, userID, status string) error {
	return r.db.WithContext(ctx).Model(&model.ThemeState{}).
		Where("id = ? AND user_id = ?", id, userID).
		Updates(map[string]interface{}{
			"status":     status,
			"updated_at": time.Now(),
		}).Error
}

// UpdateEmbeddingID 更新主题的 embedding_id
func (r *ThemeStateRepo) UpdateEmbeddingID(ctx context.Context, id, embeddingID string) error {
	return r.db.WithContext(ctx).Model(&model.ThemeState{}).
		Where("id = ?", id).
		Update("embedding_id", embeddingID).Error
}

// Archive 关闭主题（等同于 close）
func (r *ThemeStateRepo) Archive(ctx context.Context, id, userID string) error {
	return r.Close(ctx, id, userID)
}

// ReactivateDraft 重新激活 closed → draft
func (r *ThemeStateRepo) ReactivateDraft(ctx context.Context, id, userID string) error {
	return r.db.WithContext(ctx).Model(&model.ThemeState{}).
		Where("id = ? AND user_id = ?", id, userID).
		Updates(map[string]interface{}{
			"status":     "draft",
			"updated_at": time.Now(),
		}).Error
}

// UpdateMetadata 更新主题的 Metadata JSON 字段
func (r *ThemeStateRepo) UpdateMetadata(ctx context.Context, id, userID, metadataJSON string) error {
	return r.db.WithContext(ctx).Model(&model.ThemeState{}).
		Where("id = ? AND user_id = ?", id, userID).
		Update("metadata", metadataJSON).Error
}

// UpdateConversationID 更新关联的对话 ID
func (r *ThemeStateRepo) UpdateConversationID(ctx context.Context, id, userID, convID string) error {
	return r.db.WithContext(ctx).Model(&model.ThemeState{}).
		Where("id = ? AND user_id = ?", id, userID).
		Update("conversation_id", convID).Error
}

// ExpireDrafts 将超过 days 天的 draft 主题关闭
func (r *ThemeStateRepo) ExpireDrafts(ctx context.Context, userID string, days int) error {
	result := r.db.WithContext(ctx).Model(&model.ThemeState{}).
		Where("user_id = ? AND status = ? AND created_at < DATE_SUB(NOW(), INTERVAL ? DAY)", userID, "draft", days).
		Update("status", "closed")
	if result.RowsAffected > 0 {
		log.Printf("[theme] expired %d drafts for user %s", result.RowsAffected, userID[:8])
	}
	return result.Error
}

// Close 关闭主题
func (r *ThemeStateRepo) Close(ctx context.Context, id, userID string) error {
	return r.db.WithContext(ctx).Model(&model.ThemeState{}).
		Where("id = ? AND user_id = ?", id, userID).
		Update("status", "closed").Error
}

// BatchGetByIDs 批量按 ID + userID 查询主题，用 IN 查询
func (r *ThemeStateRepo) BatchGetByIDs(ctx context.Context, ids []string, userID string) (map[string]*model.ThemeState, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var list []model.ThemeState
	if err := r.db.WithContext(ctx).Where("id IN ? AND user_id = ?", ids, userID).Find(&list).Error; err != nil {
		return nil, err
	}
	result := make(map[string]*model.ThemeState, len(list))
	for i := range list {
		result[list[i].ID] = &list[i]
	}
	return result, nil
}

// ThemeStats 主题统计信息
type ThemeStats struct {
	DraftCount        int   `json:"draft_count"`
	ActiveCount       int   `json:"active_count"`
	ClosedCount       int   `json:"closed_count"`
	TodayDraftCreated int64 `json:"today_draft_created"`
	TodayUpgraded     int64 `json:"today_upgraded"`
}

// Stats 返回用户主题统计
func (r *ThemeStateRepo) Stats(ctx context.Context, userID string) (ThemeStats, error) {
	var s ThemeStats
	today := time.Now().Truncate(24 * time.Hour)

	// 按状态计数
	type row struct {
		Status string
		Count  int64
	}
	var rows []row
	if err := r.db.WithContext(ctx).Model(&model.ThemeState{}).
		Select("status, COUNT(*) as count").
		Where("user_id = ?", userID).
		Group("status").Find(&rows).Error; err != nil {
		return s, err
	}
	for _, r := range rows {
		switch r.Status {
		case "draft":
			s.DraftCount = int(r.Count)
		case "active":
			s.ActiveCount = int(r.Count)
		case "closed":
			s.ClosedCount = int(r.Count)
		}
	}

	// 今日新建 draft
	if err := r.db.WithContext(ctx).Model(&model.ThemeState{}).
		Where("user_id = ? AND status = ? AND created_at >= ?", userID, "draft", today).
		Count(&s.TodayDraftCreated).Error; err != nil {
		return s, err
	}
	// 今日升级到 active
	if err := r.db.WithContext(ctx).Model(&model.ThemeState{}).
		Where("user_id = ? AND status = ? AND updated_at >= ?", userID, "active", today).
		Count(&s.TodayUpgraded).Error; err != nil {
		return s, err
	}
	return s, nil
}

// GlobalStats 返回全局主题统计（不区分用户）
func (r *ThemeStateRepo) GlobalStats(ctx context.Context) (ThemeStats, error) {
	var s ThemeStats
	today := time.Now().Truncate(24 * time.Hour)

	type row struct {
		Status string
		Count  int64
	}
	var rows []row
	if err := r.db.WithContext(ctx).Model(&model.ThemeState{}).
		Select("status, COUNT(*) as count").
		Group("status").Find(&rows).Error; err != nil {
		return s, err
	}
	for _, r := range rows {
		switch r.Status {
		case "draft":
			s.DraftCount = int(r.Count)
		case "active":
			s.ActiveCount = int(r.Count)
		case "closed":
			s.ClosedCount = int(r.Count)
		}
	}

	if err := r.db.WithContext(ctx).Model(&model.ThemeState{}).
		Where("status = ? AND created_at >= ?", "draft", today).
		Count(&s.TodayDraftCreated).Error; err != nil {
		return s, err
	}
	if err := r.db.WithContext(ctx).Model(&model.ThemeState{}).
		Where("status = ? AND updated_at >= ?", "active", today).
		Count(&s.TodayUpgraded).Error; err != nil {
		return s, err
	}
	return s, nil
}

// ListNotDraft 分页列出用户主题（排除 draft 内部状态）
func (r *ThemeStateRepo) ListNotDraft(ctx context.Context, userID string, limit, offset int) ([]model.ThemeState, int64, error) {
	if limit <= 0 {
		limit = 20
	}
	q := r.db.WithContext(ctx).Where("user_id = ? AND status != ?", userID, "draft")
	var total int64
	if err := q.Model(&model.ThemeState{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.ThemeState
	err := q.Order("created_at DESC").Limit(limit).Offset(offset).Find(&list).Error
	return list, total, err
}

// List 分页列出用户主题
func (r *ThemeStateRepo) List(ctx context.Context, userID, status string, limit, offset int) ([]model.ThemeState, int64, error) {
	if limit <= 0 {
		limit = 20
	}
	q := r.db.WithContext(ctx).Where("user_id = ?", userID)
	if status != "" {
		q = q.Where("status = ?", status)
	}

	var total int64
	if err := q.Model(&model.ThemeState{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var list []model.ThemeState
	err := q.Order("created_at DESC").Limit(limit).Offset(offset).Find(&list).Error
	return list, total, err
}
