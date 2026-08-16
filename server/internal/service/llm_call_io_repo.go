package service

import (
	"context"

	"gorm.io/gorm"

	"xhan-helper/internal/model"
)

// LLMCallIORepo — LLM 调用审计数据访问。
type LLMCallIORepo struct {
	db *gorm.DB
}

func NewLLMCallIORepo(db *gorm.DB) *LLMCallIORepo {
	return &LLMCallIORepo{db: db}
}

// Create 插入一条审计记录。
func (r *LLMCallIORepo) Create(ctx context.Context, row *model.LLMCallIO) error {
	return r.db.WithContext(ctx).Create(row).Error
}

// ListParams — 列表筛选参数。
type LLMIOListParams struct {
	Category    string // 可选
	SubCategory string // 可选
	ConvID      string // 可选
	Limit       int    // 默认 50，上限 200
	Offset      int
}

// List 分页列表，不含大字段（system_prompt/messages_json/response/error_msg），详情单独取。
func (r *LLMCallIORepo) List(ctx context.Context, p LLMIOListParams) ([]model.LLMCallIO, int64, error) {
	if p.Limit <= 0 {
		p.Limit = 50
	}
	if p.Limit > 200 {
		p.Limit = 200
	}
	q := r.db.WithContext(ctx).Model(&model.LLMCallIO{})
	if p.Category != "" {
		q = q.Where("category = ?", p.Category)
	}
	if p.SubCategory != "" {
		q = q.Where("sub_category = ?", p.SubCategory)
	}
	if p.ConvID != "" {
		q = q.Where("conv_id = ?", p.ConvID)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []model.LLMCallIO
	err := q.Select("id", "category", "sub_category", "user_id", "conv_id", "model",
		"prompt_tokens", "completion_tokens", "latency_ms", "error_msg", "created_at").
		Order("created_at DESC").
		Limit(p.Limit).Offset(p.Offset).
		Find(&rows).Error
	return rows, total, err
}

// GetByID 详情（含大字段），不存在返回 (nil, nil)。
func (r *LLMCallIORepo) GetByID(ctx context.Context, id string) (*model.LLMCallIO, error) {
	var row model.LLMCallIO
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&row).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &row, err
}

// CleanupBefore 删除 created_at 早于 cutoff 的记录。limit 控制单批行数（防长事务）。
func (r *LLMCallIORepo) CleanupBefore(ctx context.Context, cutoff interface{}, limit int) (int64, error) {
	res := r.db.WithContext(ctx).
		Where("created_at < ?", cutoff).
		Limit(limit).
		Delete(&model.LLMCallIO{})
	return res.RowsAffected, res.Error
}
