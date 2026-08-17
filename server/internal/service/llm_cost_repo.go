package service

import (
	"context"
	"fmt"
	"sort"
	"time"

	"gorm.io/gorm"

	"xhan-helper/internal/model"
)

// LlmCostRepo token 消耗数据访问层
type LlmCostRepo struct {
	db *gorm.DB
}

// NewLlmCostRepo 创建 LlmCostRepo
func NewLlmCostRepo(db *gorm.DB) *LlmCostRepo {
	return &LlmCostRepo{db: db}
}

// Insert 插入一条调用明细。
// detail.Date 应为 "2006-01-02" 格式字符串；如果为空则用当前日期。
func (r *LlmCostRepo) Insert(ctx context.Context, detail *model.LlmCostDetail) error {
	if detail.Date == "" {
		detail.Date = time.Now().Format("2006-01-02")
	}
	return r.db.WithContext(ctx).Create(detail).Error
}

// ============================================================
// 聚合查询
// ============================================================

// QueryParams 聚合查询参数
type QueryParams struct {
	Start    string   // 开始日期 YYYY-MM-DD（含）
	End      string   // 结束日期 YYYY-MM-DD（含）
	UserIDs  []string // 可选，多选用户；为空 = 所有用户
	Category string   // 可选，按大类筛选
	GroupBy  string   // 分组字段：date / category / model / user_id / sub_category
}

// TokenTotal 一段数据的 token 汇总（含缓存拆分）
type TokenTotal struct {
	PromptCacheHit  int64   `json:"prompt_cache_hit"`
	PromptCacheMiss int64   `json:"prompt_cache_miss"`
	Completion      int64   `json:"completion"`
	TotalTokens     int64   `json:"total_tokens"`
	CallCount       int64   `json:"call_count"`
	CacheHitRate    float64 `json:"cache_hit_rate"`
}

// GroupRow 单个分组行
type GroupRow struct {
	Key string `json:"key"`
	TokenTotal
}

// GroupRows
type GroupRows []GroupRow

// Len 返回切片长度
func (g GroupRows) Len() int {
	return len(g)
}

// Less 默认从小到大排序，按 Key 比较
func (g GroupRows) Less(i, j int) bool {
	return g[i].Key < g[j].Key
}

// Swap 交换两个元素
func (g GroupRows) Swap(i, j int) {
	g[i], g[j] = g[j], g[i]
}

// QueryResult 聚合查询结果
type QueryResult struct {
	Total  TokenTotal `json:"total"`
	Groups []GroupRow `json:"groups"`
}

var validDims = map[string]bool{
	"date": true, "category": true, "model": true, "user_id": true, "sub_category": true,
}

// dimToColumn 将查询参数维度名转为数据库列名（防注入）。
func dimToColumn(dim string) string {
	switch dim {
	case "date":
		return "date"
	case "category":
		return "category"
	case "model":
		return "model"
	case "user_id":
		return "user_id"
	case "sub_category":
		return "sub_category"
	default:
		return "date"
	}
}

const tokenSelectCols = "COALESCE(SUM(prompt_cache_hit_tokens),0) as prompt_cache_hit," +
	"COALESCE(SUM(prompt_cache_miss_tokens),0) as prompt_cache_miss," +
	"COALESCE(SUM(completion_tokens),0) as completion," +
	"COALESCE(SUM(total_tokens),0) as total_tokens," +
	"COUNT(*) as call_count"

// Query 执行聚合查询。
func (r *LlmCostRepo) Query(ctx context.Context, params QueryParams) (*QueryResult, error) {
	if !validDims[params.GroupBy] {
		return nil, fmt.Errorf("invalid group_by: %s", params.GroupBy)
	}

	q := r.db.WithContext(ctx).Model(&model.LlmCostDetail{})
	if params.Start != "" {
		q = q.Where("date >= ?", params.Start)
	}
	if params.End != "" {
		q = q.Where("date <= ?", params.End)
	}
	if len(params.UserIDs) > 0 {
		q = q.Where("user_id IN ?", params.UserIDs)
	}
	if params.Category != "" {
		q = q.Where("category = ?", params.Category)
	}

	// --- 总额 ---
	var total TokenTotal
	if err := q.Select(tokenSelectCols).Scan(&total).Error; err != nil {
		return nil, fmt.Errorf("query totals: %w", err)
	}
	total.CacheHitRate = cacheHitRate(total.PromptCacheHit, total.PromptCacheMiss)

	// --- 分组 ---
	groupCol := dimToColumn(params.GroupBy)
	var groups []GroupRow
	if err := q.Select(groupCol + " as `key`," + tokenSelectCols).
		Group(groupCol).
		Order("total_tokens DESC").
		Scan(&groups).Error; err != nil {
		return nil, fmt.Errorf("query groups: %w", err)
	}
	for i := range groups {
		groups[i].Key = normalizeDateKey(groups[i].Key)
		groups[i].CacheHitRate = cacheHitRate(groups[i].PromptCacheHit, groups[i].PromptCacheMiss)
	}
	if groups == nil {
		groups = []GroupRow{}
	}
	var groupRows GroupRows = groups
	sort.Sort(groupRows)

	return &QueryResult{Total: total, Groups: groupRows}, nil
}

// normalizeDateKey SQLite 驱动可能把 date 列读回 RFC3339（"2026-08-11T00:00:00Z"），
// MySQL 返回纯日期串（"2026-08-11"）。统一截取日期部分。
func normalizeDateKey(s string) string {
	if len(s) > 10 && s[4] == '-' {
		return s[:10]
	}
	return s
}

// cacheHitRate 缓存命中率：hit / (hit + miss)，分母为 0 返回 0。
func cacheHitRate(hit, miss int64) float64 {
	if hit+miss == 0 {
		return 0
	}
	return float64(hit) / float64(hit+miss)
}

// ============================================================
// 明细 / 用户列表 / 汇总
// ============================================================

// ListParams 明细分页查询参数
type ListParams struct {
	Start    string
	End      string
	UserIDs  []string
	Category string
	Date     string // 精确日期筛选
}

// ListDetails 分页查询调用明细（created_at DESC）
func (r *LlmCostRepo) ListDetails(ctx context.Context, params ListParams, page, pageSize int) ([]model.LlmCostDetail, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}

	q := r.db.WithContext(ctx).Model(&model.LlmCostDetail{})
	if params.Start != "" {
		q = q.Where("date >= ?", params.Start)
	}
	if params.End != "" {
		q = q.Where("date <= ?", params.End)
	}
	if len(params.UserIDs) > 0 {
		q = q.Where("user_id IN ?", params.UserIDs)
	}
	if params.Category != "" {
		q = q.Where("category = ?", params.Category)
	}
	if params.Date != "" {
		q = q.Where("date = ?", params.Date)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count details: %w", err)
	}

	var rows []model.LlmCostDetail
	err := q.Order("created_at DESC").
		Limit(pageSize).
		Offset((page - 1) * pageSize).
		Find(&rows).Error
	if err != nil {
		return nil, 0, fmt.Errorf("list details: %w", err)
	}
	for i := range rows {
		rows[i].Date = normalizeDateKey(rows[i].Date)
	}
	return rows, total, nil
}

// UserCostSummary 用户在统计时段内的消耗汇总（下拉列表排序用）
type UserCostSummary struct {
	UserID      string `json:"user_id"`
	TotalTokens int64  `json:"total_tokens"`
	CallCount   int64  `json:"call_count"`
}

// ListUsers 列出有成本记录的用户（total_tokens DESC），userID 非空过滤。
func (r *LlmCostRepo) ListUsers(ctx context.Context, start, end string) ([]UserCostSummary, error) {
	q := r.db.WithContext(ctx).Model(&model.LlmCostDetail{}).
		Select("user_id, SUM(total_tokens) as total_tokens, COUNT(*) as call_count").
		Where("user_id != ''")
	if start != "" {
		q = q.Where("date >= ?", start)
	}
	if end != "" {
		q = q.Where("date <= ?", end)
	}
	var rows []UserCostSummary
	err := q.Group("user_id").Order("total_tokens DESC").Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	return rows, nil
}

// Summary 返回时间段内的汇总（不含分组）
func (r *LlmCostRepo) Summary(ctx context.Context, start, end string, userIDs []string, category string) (*TokenTotal, error) {
	q := r.db.WithContext(ctx).Model(&model.LlmCostDetail{})
	if start != "" {
		q = q.Where("date >= ?", start)
	}
	if end != "" {
		q = q.Where("date <= ?", end)
	}
	if len(userIDs) > 0 {
		q = q.Where("user_id IN ?", userIDs)
	}
	if category != "" {
		q = q.Where("category = ?", category)
	}
	var total TokenTotal
	if err := q.Select(tokenSelectCols).Scan(&total).Error; err != nil {
		return nil, fmt.Errorf("query summary: %w", err)
	}
	total.CacheHitRate = cacheHitRate(total.PromptCacheHit, total.PromptCacheMiss)
	return &total, nil
}
