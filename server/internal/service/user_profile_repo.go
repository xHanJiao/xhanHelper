package service

import (
	"context"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"xhan-helper/internal/model"
)

// UserProfileRepo — 用户画像数据访问
type UserProfileRepo struct {
	db *gorm.DB
}

// NewUserProfileRepo 创建 UserProfileRepo
func NewUserProfileRepo(db *gorm.DB) *UserProfileRepo {
	return &UserProfileRepo{db: db}
}

// Upsert 插入或更新画像字段（user_id + field 复合主键冲突时覆盖）。
// 用户明确陈述的新事实与旧值冲突 → 直接覆盖（用户亲口说 = 高置信度）。
func (r *UserProfileRepo) Upsert(ctx context.Context, p *model.UserProfile) error {
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "user_id"}, {Name: "field"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"field_value", "source", "updated_at",
		}),
	}).Create(p).Error
}

// GetByUser 返回用户全部画像字段，按更新时间升序（旧→新）。
func (r *UserProfileRepo) GetByUser(ctx context.Context, userID string) ([]model.UserProfile, error) {
	var list []model.UserProfile
	err := r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("updated_at ASC").
		Find(&list).Error
	return list, err
}

// HasAny 检查用户是否有任何画像行（冷启动判断用）。
func (r *UserProfileRepo) HasAny(ctx context.Context, userID string) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Model(&model.UserProfile{}).
		Where("user_id = ?", userID).
		Count(&count).Error
	return count > 0, err
}
