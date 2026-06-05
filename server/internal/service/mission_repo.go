package service

import (
	"context"
	"time"

	"gorm.io/gorm"

	"xhan-helper/internal/model"
)

// MissionRepo — 后台追踪任务数据访问
type MissionRepo struct {
	db *gorm.DB
}

func NewMissionRepo(db *gorm.DB) *MissionRepo {
	return &MissionRepo{db: db}
}

func (r *MissionRepo) Create(ctx context.Context, m *model.Mission) error {
	return r.db.WithContext(ctx).Create(m).Error
}

func (r *MissionRepo) Update(ctx context.Context, m *model.Mission) error {
	return r.db.WithContext(ctx).Save(m).Error
}

func (r *MissionRepo) GetByID(ctx context.Context, id, userID string) (*model.Mission, error) {
	var m model.Mission
	err := r.db.WithContext(ctx).Where("id = ? AND user_id = ?", id, userID).First(&m).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &m, err
}

func (r *MissionRepo) ListDueForCheck(ctx context.Context, limit int) ([]model.Mission, error) {
	var list []model.Mission
	err := r.db.WithContext(ctx).
		Where("status = ? AND next_check_at IS NOT NULL AND next_check_at <= ?", "active", time.Now()).
		Order("next_check_at ASC").Limit(limit).
		Find(&list).Error
	return list, err
}

func (r *MissionRepo) ListByTheme(ctx context.Context, themeID string) ([]model.Mission, error) {
	var list []model.Mission
	err := r.db.WithContext(ctx).Where("theme_id = ?", themeID).Find(&list).Error
	return list, err
}

func (r *MissionRepo) ListByStatuses(ctx context.Context, userID string, statuses []string) ([]model.Mission, error) {
	var list []model.Mission
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND status IN ?", userID, statuses).
		Order("created_at DESC").
		Find(&list).Error
	return list, err
}

func (r *MissionRepo) ListActiveByUser(ctx context.Context, userID string) ([]model.Mission, error) {
	return r.ListByStatuses(ctx, userID, []string{"active"})
}

func (r *MissionRepo) UpdateAfterCheck(ctx context.Context, id string, summary string, nextCheck time.Time) error {
	return r.db.WithContext(ctx).Model(&model.Mission{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"last_summary":  summary,
			"last_check_at": time.Now(),
			"next_check_at": nextCheck,
			"check_count":   gorm.Expr("check_count + 1"),
		}).Error
}

func (r *MissionRepo) ListByIDs(ctx context.Context, ids []string, userID string) (map[string]*model.Mission, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var list []model.Mission
	err := r.db.WithContext(ctx).
		Where("id IN ? AND user_id = ? AND status = ?", ids, userID, "active").
		Find(&list).Error
	if err != nil {
		return nil, err
	}
	m := make(map[string]*model.Mission, len(list))
	for i := range list {
		m[list[i].ID] = &list[i]
	}
	return m, nil
}

func (r *MissionRepo) UpdateStatus(ctx context.Context, id, userID, status string) error {
	return r.db.WithContext(ctx).Model(&model.Mission{}).
		Where("id = ? AND user_id = ?", id, userID).
		Update("status", status).Error
}
