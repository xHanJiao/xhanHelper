package service

import (
	"context"
	"strings"

	"gorm.io/gorm"

	"xhan-helper/internal/idgen"
	"xhan-helper/internal/model"
)

// UserProfileFactRepo — 用户画像事实数据访问（field → fact 三级结构）。
type UserProfileFactRepo struct {
	db *gorm.DB
}

// NewUserProfileFactRepo 创建 UserProfileFactRepo。
func NewUserProfileFactRepo(db *gorm.DB) *UserProfileFactRepo {
	return &UserProfileFactRepo{db: db}
}

// ReplaceFieldFacts 全量替换某字段的事实集（事务）：
// 旧 active 全部标 outdated（关联 batchID），新 values 逐条插入 active。
// 不做文本 diff——"LLM 省略某条" 即判定过时，由调用方决定替换与否。
// 防呆：values 先去重（trim 后相同内容只保留首次出现）+ 过滤空值，避免 LLM 重复输出同一事实。
func (r *UserProfileFactRepo) ReplaceFieldFacts(ctx context.Context, userID, field string, values []string, batchID string) error {
	values = dedupeValues(values)
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.UserProfileFact{}).
			Where("user_id = ? AND field = ? AND status = ?", userID, field, model.FactStatusActive).
			Updates(map[string]any{"status": model.FactStatusOutdated, "batch_id": batchID}).Error; err != nil {
			return err
		}
		facts := make([]model.UserProfileFact, 0, len(values))
		for _, v := range values {
			facts = append(facts, model.UserProfileFact{
				ID:      idgen.Next(),
				UserID:  userID,
				Field:   field,
				Value:   v,
				Status:  model.FactStatusActive,
				BatchID: batchID,
			})
		}
		if len(facts) > 0 {
			if err := tx.Create(&facts).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// dedupeValues 防呆去重：trim 后过滤空值，相同内容只保留首次出现的顺序。
func dedupeValues(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

// GetActiveByUser 返回用户全部 active 事实，按 created_at 升序（旧→新）。
func (r *UserProfileFactRepo) GetActiveByUser(ctx context.Context, userID string) ([]model.UserProfileFact, error) {
	var list []model.UserProfileFact
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND status = ?", userID, model.FactStatusActive).
		Order("created_at ASC").
		Find(&list).Error
	return list, err
}

// GetActiveByField 返回用户某字段的全部 active 事实，按 created_at 升序。
func (r *UserProfileFactRepo) GetActiveByField(ctx context.Context, userID, field string) ([]model.UserProfileFact, error) {
	var list []model.UserProfileFact
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND field = ? AND status = ?", userID, field, model.FactStatusActive).
		Order("created_at ASC").
		Find(&list).Error
	return list, err
}

// HasActive 检查用户是否有任何 active 事实（冷启动判断用）。
func (r *UserProfileFactRepo) HasActive(ctx context.Context, userID string) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Model(&model.UserProfileFact{}).
		Where("user_id = ? AND status = ?", userID, model.FactStatusActive).
		Count(&count).Error
	return count > 0, err
}

// CleanupOutdated 清理 outdated 事实：按批次时间倒序保留最近 keepBatches 批，删除更旧的。
// 返回删除条数。
func (r *UserProfileFactRepo) CleanupOutdated(ctx context.Context, userID string, keepBatches int) (int64, error) {
	var batches []string
	err := r.db.WithContext(ctx).
		Model(&model.UserProfileFact{}).
		Select("batch_id").
		Where("user_id = ? AND status = ?", userID, model.FactStatusOutdated).
		Group("batch_id").
		Order("MAX(created_at) DESC").
		Pluck("batch_id", &batches).Error
	if err != nil {
		return 0, err
	}
	if len(batches) <= keepBatches {
		return 0, nil
	}
	stale := batches[keepBatches:]
	res := r.db.WithContext(ctx).
		Where("user_id = ? AND status = ? AND batch_id IN ?", userID, model.FactStatusOutdated, stale).
		Delete(&model.UserProfileFact{})
	return res.RowsAffected, res.Error
}
