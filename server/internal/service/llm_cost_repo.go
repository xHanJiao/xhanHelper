package service

import (
	"context"
	"fmt"
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

// QueryParams 聚合查询参数
type QueryParams struct {
	Start    string // 开始日期 YYYY-MM-DD（含）
	End      string // 结束日期 YYYY-MM-DD（含）
	UserID   string // 可选，按用户筛选
	Category string // 可选，按大类筛选
	GroupBy  string // 一级分组字段：date / category / model / user_id / sub_category
	SubGroup string // 二级分组字段，与 GroupBy 不同即可
}

// QueryResult 聚合查询结果
type QueryResult struct {
	TotalTokens      int64      `json:"total_tokens"`
	PromptTokens     int64      `json:"prompt_tokens"`
	CompletionTokens int64      `json:"completion_tokens"`
	TotalCalls       int64      `json:"total_calls"`
	Groups           []GroupRow `json:"groups"`
}

// GroupRow 单个分组行
type GroupRow struct {
	Key              string     `json:"key"`
	TotalTokens      int64      `json:"total_tokens"`
	PromptTokens     int64      `json:"prompt_tokens"`
	CompletionTokens int64      `json:"completion_tokens"`
	CallCount        int64      `json:"call_count"`
	SubGroups        []GroupRow `json:"sub_groups,omitempty"`
}

// resultRow GORM Scan 的中间结构体
type resultRow struct {
	TotalTokens      int64
	PromptTokens     int64
	CompletionTokens int64
	CallCount        int64
	Key              string
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

// Query 执行聚合查询。
// 不支持跨维度 SubGroup（如 GroupBy=date 时 SubGroup=date），调用方自行保证。
func (r *LlmCostRepo) Query(ctx context.Context, params QueryParams) (*QueryResult, error) {
	if !validDims[params.GroupBy] {
		return nil, fmt.Errorf("invalid group_by: %s", params.GroupBy)
	}
	if params.SubGroup != "" && !validDims[params.SubGroup] {
		return nil, fmt.Errorf("invalid sub_group: %s", params.SubGroup)
	}
	if params.SubGroup == params.GroupBy {
		return nil, fmt.Errorf("sub_group must differ from group_by")
	}

	q := r.db.WithContext(ctx).Model(&model.LlmCostDetail{})
	if params.Start != "" {
		q = q.Where("date >= ?", params.Start)
	}
	if params.End != "" {
		q = q.Where("date <= ?", params.End)
	}
	if params.UserID != "" {
		q = q.Where("user_id = ?", params.UserID)
	}
	if params.Category != "" {
		q = q.Where("category = ?", params.Category)
	}

	// --- 总额 ---
	var total resultRow
	if err := q.Select(
		"COALESCE(SUM(total_tokens),0) as total_tokens," +
			"COALESCE(SUM(prompt_tokens),0) as prompt_tokens," +
			"COALESCE(SUM(completion_tokens),0) as completion_tokens," +
			"COUNT(*) as call_count",
	).Scan(&total).Error; err != nil {
		return nil, fmt.Errorf("query totals: %w", err)
	}

	// --- 一级分组 ---
	groupCol := dimToColumn(params.GroupBy)
	selectCols := groupCol + " as `key`," +
		"SUM(total_tokens) as total_tokens," +
		"SUM(prompt_tokens) as prompt_tokens," +
		"SUM(completion_tokens) as completion_tokens," +
		"COUNT(*) as call_count"

	var groups []GroupRow
	if err := q.Select(selectCols).
		Group(groupCol).
		Order("total_tokens DESC").
		Scan(&groups).Error; err != nil {
		return nil, fmt.Errorf("query groups: %w", err)
	}

	// --- 二级分组 ---
	if params.SubGroup != "" && len(groups) > 0 {
		subCol := dimToColumn(params.SubGroup)
		for i := range groups {
			var subs []GroupRow
			if err := q.Where(groupCol+" = ?", groups[i].Key).
				Select(
					subCol + " as `key`," +
						"SUM(total_tokens) as total_tokens," +
						"SUM(prompt_tokens) as prompt_tokens," +
						"SUM(completion_tokens) as completion_tokens," +
						"COUNT(*) as call_count",
				).
				Group(subCol).
				Order("total_tokens DESC").
				Scan(&subs).Error; err != nil {
				return nil, fmt.Errorf("query sub groups: %w", err)
			}
			groups[i].SubGroups = subs
		}
	}

	return &QueryResult{
		TotalTokens:      total.TotalTokens,
		PromptTokens:     total.PromptTokens,
		CompletionTokens: total.CompletionTokens,
		TotalCalls:       total.CallCount,
		Groups:           groups,
	}, nil
}
