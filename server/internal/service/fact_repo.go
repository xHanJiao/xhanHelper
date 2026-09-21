package service

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"

	"xhan-helper/internal/model"
)

// FactRepo — 长期事实数据访问。
// 约定与 UserMemoryRepo 一致：not found 返回 (nil, nil)，list 方法分页。
type FactRepo struct {
	db *gorm.DB
}

// NewFactRepo 创建 FactRepo
func NewFactRepo(db *gorm.DB) *FactRepo {
	return &FactRepo{db: db}
}

// Create 插入一条事实
func (r *FactRepo) Create(ctx context.Context, f *model.Fact) error {
	if err := r.db.WithContext(ctx).Create(f).Error; err != nil {
		return fmt.Errorf("fact create: %w", err)
	}
	return nil
}

// FindByID 按 ID + 用户查单条（不过滤 status，供审计/回滚读回软删行）。
// not found 返回 (nil, nil)。
func (r *FactRepo) FindByID(ctx context.Context, userID, id string) (*model.Fact, error) {
	var f model.Fact
	err := r.db.WithContext(ctx).Where("id = ? AND user_id = ?", id, userID).First(&f).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("fact find by id: %w", err)
	}
	return &f, nil
}

// FindByContentHash 精确判重：按 user + hash 查 active 事实。
// not found 返回 (nil, nil)。
func (r *FactRepo) FindByContentHash(ctx context.Context, userID, contentHash string) (*model.Fact, error) {
	var f model.Fact
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND content_hash = ? AND status = ?", userID, contentHash, model.FactStatusActive).
		First(&f).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("fact find by content hash: %w", err)
	}
	return &f, nil
}

// UpdateContent 覆盖 content（及 subject/kind），用于 UPDATE 决策。空字符串表示不改该字段。
func (r *FactRepo) UpdateContent(ctx context.Context, id, content, contentHash, subject, kind string) error {
	updates := map[string]interface{}{
		"content":      content,
		"content_hash": contentHash,
	}
	if subject != "" {
		updates["subject"] = subject
	}
	if kind != "" {
		updates["kind"] = kind
	}
	if err := r.db.WithContext(ctx).Model(&model.Fact{}).Where("id = ?", id).Updates(updates).Error; err != nil {
		return fmt.Errorf("fact update content: %w", err)
	}
	return nil
}

// SoftDelete 软删：status = deleted。
func (r *FactRepo) SoftDelete(ctx context.Context, id string) error {
	if err := r.db.WithContext(ctx).Model(&model.Fact{}).
		Where("id = ?", id).
		Update("status", model.FactStatusDeleted).Error; err != nil {
		return fmt.Errorf("fact soft delete: %w", err)
	}
	return nil
}

// ListByUser 分页列出用户 active 事实（含总数）。limit<=0 默认 50。
func (r *FactRepo) ListByUser(ctx context.Context, userID string, limit, offset int) ([]model.Fact, int64, error) {
	if limit <= 0 {
		limit = 50
	}
	q := r.db.WithContext(ctx).Model(&model.Fact{}).
		Where("user_id = ? AND status = ?", userID, model.FactStatusActive)

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("fact count by user: %w", err)
	}

	var list []model.Fact
	if err := q.Order("created_at DESC").Limit(limit).Offset(offset).Find(&list).Error; err != nil {
		return nil, 0, fmt.Errorf("fact list by user: %w", err)
	}
	return list, total, nil
}

// ListAllActivePaginated 全用户 active 分页，仅 startup 预热用。limit<=0 默认 100。
func (r *FactRepo) ListAllActivePaginated(ctx context.Context, limit, offset int) ([]model.Fact, error) {
	if limit <= 0 {
		limit = 100
	}
	var list []model.Fact
	if err := r.db.WithContext(ctx).
		Where("status = ?", model.FactStatusActive).
		Limit(limit).Offset(offset).
		Find(&list).Error; err != nil {
		return nil, fmt.Errorf("fact list active paginated: %w", err)
	}
	return list, nil
}
