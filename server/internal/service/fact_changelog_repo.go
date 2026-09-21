package service

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	"xhan-helper/internal/model"
)

// FactChangelogRepo — 事实变更审计数据访问。
type FactChangelogRepo struct {
	db *gorm.DB
}

// NewFactChangelogRepo 创建 FactChangelogRepo
func NewFactChangelogRepo(db *gorm.DB) *FactChangelogRepo {
	return &FactChangelogRepo{db: db}
}

// Create 写入一条变更记录
func (r *FactChangelogRepo) Create(ctx context.Context, c *model.FactChangelog) error {
	if err := r.db.WithContext(ctx).Create(c).Error; err != nil {
		return fmt.Errorf("fact changelog create: %w", err)
	}
	return nil
}

// ListByFact 按事实分页列出变更（含总数）。limit<=0 默认 20。
func (r *FactChangelogRepo) ListByFact(ctx context.Context, userID, factID string, limit, offset int) ([]model.FactChangelog, int64, error) {
	if limit <= 0 {
		limit = 20
	}
	q := r.db.WithContext(ctx).Model(&model.FactChangelog{}).
		Where("user_id = ? AND fact_id = ?", userID, factID)

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("fact changelog count: %w", err)
	}

	var list []model.FactChangelog
	if err := q.Order("created_at DESC").Limit(limit).Offset(offset).Find(&list).Error; err != nil {
		return nil, 0, fmt.Errorf("fact changelog list by fact: %w", err)
	}
	return list, total, nil
}
