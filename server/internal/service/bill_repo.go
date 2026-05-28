package service

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
	"xhan-helper/internal/model"
)

// CatStatRow — 分类统计查询结果行
type CatStatRow struct {
	Category1 string
	Amount    float64
	Count     int64
}

// MonthlyStatRow — 月度统计查询结果行
type MonthlyStatRow struct {
	Month  string
	Amount float64
	Count  int64
}

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
func (r *BillRepo) ListFiltered(ctx context.Context, userID, startDate, endDate, category, category2, keyword string, limit, offset int) ([]model.Bill, int64, error) {
	if limit <= 0 {
		limit = 50
	}
	base := func() *gorm.DB {
		return r.db.WithContext(ctx).
			Where("user_id = ? AND status = ?", userID, model.BillStatusNormal)
	}
	q := base()
	if startDate != "" {
		q = q.Where("date >= ?", startDate)
	}
	if endDate != "" {
		q = q.Where("date <= ?", endDate)
	}
	if category != "" {
		q = q.Where("category1 = ?", category)
	}
	if category2 != "" {
		q = q.Where("category2 = ?", category2)
	}
	if keyword != "" {
		q = q.Where("(item LIKE ? OR remark LIKE ?)", "%"+keyword+"%", "%"+keyword+"%")
	}

	var total int64
	countQ := base()
	if startDate != "" {
		countQ = countQ.Where("date >= ?", startDate)
	}
	if endDate != "" {
		countQ = countQ.Where("date <= ?", endDate)
	}
	if category != "" {
		countQ = countQ.Where("category1 = ?", category)
	}
	if category2 != "" {
		countQ = countQ.Where("category2 = ?", category2)
	}
	if keyword != "" {
		countQ = countQ.Where("(item LIKE ? OR remark LIKE ?)", "%"+keyword+"%", "%"+keyword+"%")
	}
	if err := countQ.Model(&model.Bill{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var list []model.Bill
	err := q.Order("date DESC, created_at DESC").Limit(limit).Offset(offset).Find(&list).Error
	return list, total, err
}

// CategoryStats 按一级分类汇总金额和数量（MySQL/SQLite GROUP BY）
func (r *BillRepo) CategoryStats(ctx context.Context, userID, startDate, endDate string) ([]CatStatRow, error) {
	q := r.db.WithContext(ctx).Model(&model.Bill{}).
		Select("category1, SUM(amount) as amount, COUNT(*) as count").
		Where("user_id = ? AND status = ?", userID, model.BillStatusNormal)
	if startDate != "" {
		q = q.Where("date >= ?", startDate)
	}
	if endDate != "" {
		q = q.Where("date <= ?", endDate)
	}

	var rows []CatStatRow
	err := q.Group("category1").Order("amount DESC").Find(&rows).Error
	return rows, err
}

// MonthlyStats 按月汇总金额和数量（MySQL/SQLite GROUP BY）
func (r *BillRepo) MonthlyStats(ctx context.Context, userID, startDate, endDate, category, category2 string) ([]MonthlyStatRow, error) {
	q := r.db.WithContext(ctx).Model(&model.Bill{}).
		Select("SUBSTR(date, 1, 7) as month, SUM(amount) as amount, COUNT(*) as count").
		Where("user_id = ? AND status = ? AND date >= ? AND date <= ?",
			userID, model.BillStatusNormal, startDate, endDate)
	if category != "" {
		q = q.Where("category1 = ?", category)
	}
	if category2 != "" {
		q = q.Where("category2 = ?", category2)
	}

	var rows []MonthlyStatRow
	err := q.Group("SUBSTR(date, 1, 7)").Order("month").Find(&rows).Error
	return rows, err
}

// GetBill 按 ID 查询单条账单
func (r *BillRepo) GetBill(ctx context.Context, userID, id string) (*model.Bill, error) {
	var b model.Bill
	err := r.db.WithContext(ctx).
		Where("id = ? AND user_id = ? AND status = ?", id, userID, model.BillStatusNormal).
		First(&b).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("bill not found: %s", id)
	}
	return &b, err
}

// CategoryOptions 从实际账单数据中提取一级分类 → 二级分类列表的映射
func (r *BillRepo) CategoryOptions(ctx context.Context, userID string) (map[string][]string, error) {
	type row struct {
		Category1 string
		Category2 string
	}
	var rows []row
	err := r.db.WithContext(ctx).Model(&model.Bill{}).
		Select("DISTINCT category1, category2").
		Where("user_id = ? AND status = ?", userID, model.BillStatusNormal).
		Order("category1, category2").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}

	result := make(map[string][]string)
	for _, r := range rows {
		result[r.Category1] = append(result[r.Category1], r.Category2)
	}
	return result, nil
}
