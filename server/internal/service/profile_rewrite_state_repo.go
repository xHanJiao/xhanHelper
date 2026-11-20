package service

import (
	"context"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"xhan-helper/internal/model"
)

// ProfileRewriteStateRepo — 画像每周重写的运行状态（每用户一行）。
type ProfileRewriteStateRepo struct {
	db *gorm.DB
}

// NewProfileRewriteStateRepo 创建 ProfileRewriteStateRepo。
func NewProfileRewriteStateRepo(db *gorm.DB) *ProfileRewriteStateRepo {
	return &ProfileRewriteStateRepo{db: db}
}

// Get 取该用户的重写状态；从未重写过返回 (nil, nil)。
func (r *ProfileRewriteStateRepo) Get(ctx context.Context, userID string) (*model.ProfileRewriteState, error) {
	// 用 Find 而非 First：没记录属于常态（首次重写），不该被当成 gorm 错误刷日志。
	var rows []model.ProfileRewriteState
	if err := r.db.WithContext(ctx).Where("user_id = ?", userID).Limit(1).Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &rows[0], nil
}

// Save upsert 一行状态（保留 created_at，只覆盖运行字段）。
func (r *ProfileRewriteStateRepo) Save(ctx context.Context, s *model.ProfileRewriteState) error {
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		DoUpdates: clause.AssignmentColumns([]string{
			"last_run_at", "last_event_count", "last_session_count", "last_fact_count",
			"last_status", "last_error", "updated_at",
		}),
	}).Create(s).Error
}
