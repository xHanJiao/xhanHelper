package service

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
	"xhan-helper/internal/model"
)

// BillFilter — 账单筛选条件
type BillFilter struct {
	StartDate     string
	EndDate       string
	Category1     string
	Category1List []string
	Category2     string
	Keyword       string
	Owner         string
	OwnerEmpty    bool
	AmountMin     float64
	AmountMax     float64
	ConsumeType   string
	PayType       string
	SortBy        string // "amount" | "date"，空则默认 date
	SortOrder     string // "asc" | "desc"，空则默认 desc
}

func (f BillFilter) orderClause() string {
	field := "date"
	if f.SortBy == "amount" {
		field = "amount"
	}
	dir := "DESC"
	if f.SortOrder == "asc" {
		dir = "ASC"
	}
	return fmt.Sprintf("%s %s, created_at DESC", field, dir)
}

// CategoryOptionsResult — 分类选项返回
type CategoryOptionsResult struct {
	Mapping       map[string][]string `json:"mapping"`
	Category1List []string            `json:"category1_list"`
	Category2List []string            `json:"category2_list"`
}

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

// CreateBatch 批量插入账单（CSV 导入用）。分块避免单条 SQL 过大；
// 整批放在一个事务里，避免前几批已落库、后一批失败时用户重试产生重复账单。
func (r *BillRepo) CreateBatch(ctx context.Context, bills []*model.Bill) error {
	if len(bills) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return tx.CreateInBatches(bills, 200).Error
	})
}

// UpdateFields 按列更新账单，只写 updates 中显式给出的字段。
// 调用方必须只放需要修改的列：整条写回会把未改字段的旧快照覆盖回去，并发更新下丢改动。
func (r *BillRepo) UpdateFields(ctx context.Context, id, userID string, updates map[string]interface{}) error {
	if len(updates) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Model(&model.Bill{}).Where("id = ? AND user_id = ?", id, userID).Updates(updates).Error
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

// applyFilter 将 BillFilter 条件应用到 query
func applyFilter(q *gorm.DB, f BillFilter) *gorm.DB {
	if f.StartDate != "" {
		q = q.Where("date >= ?", f.StartDate)
	}
	if f.EndDate != "" {
		q = q.Where("date <= ?", f.EndDate)
	}
	if len(f.Category1List) > 0 {
		q = q.Where("category1 IN ?", f.Category1List)
	} else if f.Category1 != "" {
		q = q.Where("category1 = ?", f.Category1)
	}
	if f.Category2 != "" {
		q = q.Where("category2 = ?", f.Category2)
	}
	if f.Keyword != "" {
		q = q.Where("(item LIKE ? OR remark LIKE ?)", "%"+f.Keyword+"%", "%"+f.Keyword+"%")
	}
	if f.OwnerEmpty {
		q = q.Where("owner = ?", "")
	} else if f.Owner != "" {
		q = q.Where("owner = ?", f.Owner)
	}
	if f.AmountMin > 0 {
		q = q.Where("amount >= ?", f.AmountMin)
	}
	if f.AmountMax > 0 {
		q = q.Where("amount <= ?", f.AmountMax)
	}
	if f.ConsumeType != "" {
		q = q.Where("consume_type = ?", f.ConsumeType)
	}
	if f.PayType != "" {
		q = q.Where("pay_type = ?", f.PayType)
	}
	return q
}

// ListFiltered 按条件筛选账单
func (r *BillRepo) ListFiltered(ctx context.Context, userID string, f BillFilter, limit, offset int) ([]model.Bill, int64, error) {
	if limit <= 0 {
		limit = 50
	}
	base := func() *gorm.DB {
		return r.db.WithContext(ctx).
			Where("user_id = ? AND status = ?", userID, model.BillStatusNormal)
	}

	var total int64
	if err := applyFilter(base(), f).Model(&model.Bill{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var list []model.Bill
	err := applyFilter(base(), f).Order(f.orderClause()).
		Limit(limit).Offset(offset).Find(&list).Error
	return list, total, err
}

// Stats 按当前筛选条件聚合金额（无分页，覆盖全部匹配记录）
func (r *BillRepo) Stats(ctx context.Context, userID string, f BillFilter) (count int64, sum float64, err error) {
	q := applyFilter(r.db.WithContext(ctx).Model(&model.Bill{}).
		Where("user_id = ? AND status = ?", userID, model.BillStatusNormal), f)
	var row struct {
		Count int64
		Sum   float64
	}
	if err := q.Select("COUNT(*) AS count, COALESCE(SUM(amount), 0) AS sum").Scan(&row).Error; err != nil {
		return 0, 0, err
	}
	return row.Count, row.Sum, nil
}

// CategoryStats 按一级分类汇总金额和数量（MySQL/SQLite GROUP BY）
func (r *BillRepo) CategoryStats(ctx context.Context, userID, startDate, endDate, owner string) ([]CatStatRow, error) {
	q := r.db.WithContext(ctx).Model(&model.Bill{}).
		Select("category1, SUM(amount) as amount, COUNT(*) as count").
		Where("user_id = ? AND status = ?", userID, model.BillStatusNormal)
	if startDate != "" {
		q = q.Where("date >= ?", startDate)
	}
	if endDate != "" {
		q = q.Where("date <= ?", endDate)
	}
	if owner != "" {
		q = q.Where("owner = ?", owner)
	}

	var rows []CatStatRow
	err := q.Group("category1").Order("amount DESC").Find(&rows).Error
	return rows, err
}

// MonthlyStats 按月汇总金额和数量（MySQL/SQLite GROUP BY）
func (r *BillRepo) MonthlyStats(ctx context.Context, userID, startDate, endDate, category, category2, owner string) ([]MonthlyStatRow, error) {
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
	if owner != "" {
		q = q.Where("owner = ?", owner)
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

// CategoryOptions 从实际账单数据中提取一级分类 → 二级分类列表的映射，以及各自去重列表
func (r *BillRepo) CategoryOptions(ctx context.Context, userID string) (*CategoryOptionsResult, error) {
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

	result := &CategoryOptionsResult{
		Mapping: make(map[string][]string),
	}
	c1Set := make(map[string]struct{})
	c2Set := make(map[string]struct{})
	for _, r := range rows {
		result.Mapping[r.Category1] = append(result.Mapping[r.Category1], r.Category2)
		c1Set[r.Category1] = struct{}{}
		c2Set[r.Category2] = struct{}{}
	}
	for k := range c1Set {
		result.Category1List = append(result.Category1List, k)
	}
	for k := range c2Set {
		result.Category2List = append(result.Category2List, k)
	}
	return result, nil
}

// DistinctOwners 获取当前用户所有账单的归属人列表（去重）
func (r *BillRepo) DistinctOwners(ctx context.Context, userID string) ([]string, error) {
	var owners []string
	err := r.db.WithContext(ctx).Model(&model.Bill{}).
		Select("DISTINCT owner").
		Where("user_id = ? AND status = ?", userID, model.BillStatusNormal).
		Order("owner").
		Pluck("owner", &owners).Error
	return owners, err
}

// BatchUpdateFields — 批量更新时允许修改的字段
type BatchUpdateFields struct {
	Owner       string `json:"owner,omitempty"`
	Category1   string `json:"category1,omitempty"`
	Category2   string `json:"category2,omitempty"`
	ConsumeType string `json:"consume_type,omitempty"`
	PayType     string `json:"pay_type,omitempty"`
	Remark      string `json:"remark,omitempty"`
}

// BatchUpdate 按筛选条件批量更新账单字段，最多更新 500 条，返回实际影响行数
func (r *BillRepo) BatchUpdate(ctx context.Context, userID string, f BillFilter, fields BatchUpdateFields) (int64, error) {
	base := r.db.WithContext(ctx).Model(&model.Bill{}).
		Where("user_id = ? AND status = ?", userID, model.BillStatusNormal)

	updates := map[string]interface{}{}
	if fields.Owner != "" {
		updates["owner"] = fields.Owner
	}
	if fields.Category1 != "" {
		updates["category1"] = fields.Category1
	}
	if fields.Category2 != "" {
		updates["category2"] = fields.Category2
	}
	if fields.ConsumeType != "" {
		updates["consume_type"] = fields.ConsumeType
	}
	if fields.PayType != "" {
		updates["pay_type"] = fields.PayType
	}
	if fields.Remark != "" {
		updates["remark"] = fields.Remark
	}
	if len(updates) == 0 {
		return 0, fmt.Errorf("no fields to update")
	}

	result := applyFilter(base, f).Limit(500).Updates(updates)
	return result.RowsAffected, result.Error
}
