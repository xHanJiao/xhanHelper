package service

import (
	"context"
	"errors"

	"gorm.io/gorm"
	"xhan-helper/internal/model"
)

// BillRepo — 账单数据访问
type BillRepo struct {
	db *gorm.DB
}

// NewBillRepo 创建 BillRepo
func NewBillRepo(db *gorm.DB) *BillRepo {
	return &BillRepo{db: db}
}

// Create 创建账单
func (r *BillRepo) Create(ctx context.Context, bill *model.Bill) error {
	return r.db.WithContext(ctx).Create(bill).Error
}

// Update 更新账单（仅写非零值字段，避免 Save 归零 Status 等）
func (r *BillRepo) Update(ctx context.Context, bill *model.Bill) error {
	return r.db.WithContext(ctx).Model(&model.Bill{}).Where("id = ?", bill.ID).Updates(bill).Error
}

// SoftDelete 软删除（status = 2）
func (r *BillRepo) SoftDelete(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Model(&model.Bill{}).Where("id = ?", id).
		Update("status", model.BillStatusCancel).Error
}

// FindByID 按 ID 查找
func (r *BillRepo) FindByID(ctx context.Context, id string) (*model.Bill, error) {
	var b model.Bill
	err := r.db.WithContext(ctx).Where("id = ? AND status = ?", id, model.BillStatusNormal).First(&b).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &b, err
}

// CountByUser 统计用户正常账单总数
func (r *BillRepo) CountByUser(ctx context.Context, userID string) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.Bill{}).
		Where("user_id = ? AND status = ?", userID, model.BillStatusNormal).
		Count(&count).Error
	return count, err
}

// ListByUser 分页获取用户正常状态账单，limit<=0 时默认 50
func (r *BillRepo) ListByUser(ctx context.Context, userID string, limit, offset int) ([]model.Bill, error) {
	if limit <= 0 {
		limit = 50
	}
	var list []model.Bill
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND status = ?", userID, model.BillStatusNormal).
		Order("date DESC, created_at DESC").
		Limit(limit).Offset(offset).
		Find(&list).Error
	return list, err
}

// ListFiltered 按条件筛选账单
func (r *BillRepo) ListFiltered(ctx context.Context, userID, startDate, endDate, category string, limit, offset int) ([]model.Bill, error) {
	if limit <= 0 {
		limit = 50
	}
	q := r.db.WithContext(ctx).
		Where("user_id = ? AND status = ?", userID, model.BillStatusNormal)

	if startDate != "" {
		q = q.Where("date >= ?", startDate)
	}
	if endDate != "" {
		q = q.Where("date <= ?", endDate)
	}
	if category != "" {
		q = q.Where("category1 = ?", category)
	}

	var list []model.Bill
	err := q.Order("date DESC, created_at DESC").Limit(limit).Offset(offset).Find(&list).Error
	return list, err
}
