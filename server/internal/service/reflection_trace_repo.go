package service

import (
	"context"
	"time"

	"gorm.io/gorm"

	"xhan-helper/internal/model"
)

// ReflectionTraceRepo — 反思可观测性数据访问
type ReflectionTraceRepo struct {
	db           *gorm.DB
	persistDebug bool // 是否持久化完整 LLM 请求/响应 JSON（仅 dev 调试用）
}

func NewReflectionTraceRepo(db *gorm.DB, persistDebug bool) *ReflectionTraceRepo {
	return &ReflectionTraceRepo{db: db, persistDebug: persistDebug}
}

// CreateTrace 创建一条反思 trace 记录
func (r *ReflectionTraceRepo) CreateTrace(ctx context.Context, trace *model.ReflectionTrace) error {
	return r.db.WithContext(ctx).Create(trace).Error
}

// EndTrace 结束 trace（更新状态、结束时间、统计数）
func (r *ReflectionTraceRepo) EndTrace(ctx context.Context, id string, status string, totalFindings, deliveredCount int, errMsg *string) error {
	now := time.Now()
	updates := map[string]interface{}{
		"ended_at":        now,
		"status":          status,
		"total_findings":  totalFindings,
		"delivered_count": deliveredCount,
	}
	if errMsg != nil {
		updates["error_message"] = *errMsg
	}
	return r.db.WithContext(ctx).Model(&model.ReflectionTrace{}).
		Where("id = ?", id).
		Updates(updates).Error
}

// CreateLLMCall 记录一次子 Agent 的 LLM 调用
func (r *ReflectionTraceRepo) CreateLLMCall(ctx context.Context, call *model.ReflectionLLMCall) error {
	if !r.persistDebug {
		call.RequestJSON = nil
		call.ResponseJSON = nil
	}
	return r.db.WithContext(ctx).Create(call).Error
}

// SumTokensByTrace 汇总一次 trace 的 token 消耗
func (r *ReflectionTraceRepo) SumTokensByTrace(ctx context.Context, traceID string) (promptTokens, completionTokens int64) {
	type row struct {
		Prompt     int64
		Completion int64
	}
	var rows []row
	r.db.WithContext(ctx).Model(&model.ReflectionLLMCall{}).
		Select("COALESCE(SUM(prompt_tokens),0) as prompt, COALESCE(SUM(completion_tokens),0) as completion").
		Where("trace_id = ?", traceID).
		Find(&rows)
	if len(rows) > 0 {
		return rows[0].Prompt, rows[0].Completion
	}
	return 0, 0
}

// CreateDecision 记录一个关键决策
func (r *ReflectionTraceRepo) CreateDecision(ctx context.Context, decision *model.ReflectionDecision) error {
	return r.db.WithContext(ctx).Create(decision).Error
}

// ListTraces 分页列出用户的反思 trace
func (r *ReflectionTraceRepo) ListTraces(ctx context.Context, userID string, limit, offset int) ([]model.ReflectionTrace, int64, error) {
	if limit <= 0 {
		limit = 20
	}
	q := r.db.WithContext(ctx).Where("user_id = ?", userID)

	var total int64
	if err := q.Model(&model.ReflectionTrace{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var list []model.ReflectionTrace
	err := q.Order("started_at DESC").Limit(limit).Offset(offset).Find(&list).Error
	return list, total, err
}

// GetLLMCallsByTrace 按 trace_id 获取所有 LLM 调用记录
func (r *ReflectionTraceRepo) GetLLMCallsByTrace(ctx context.Context, traceID string) ([]model.ReflectionLLMCall, error) {
	var list []model.ReflectionLLMCall
	err := r.db.WithContext(ctx).Where("trace_id = ?", traceID).Order("created_at ASC").Find(&list).Error
	return list, err
}

// GetDecisionsByTrace 按 trace_id 获取所有决策记录
func (r *ReflectionTraceRepo) GetDecisionsByTrace(ctx context.Context, traceID string) ([]model.ReflectionDecision, error) {
	var list []model.ReflectionDecision
	err := r.db.WithContext(ctx).Where("trace_id = ?", traceID).Order("created_at ASC").Find(&list).Error
	return list, err
}

// ListDecisions 分页列出决策，支持筛选
func (r *ReflectionTraceRepo) ListDecisions(ctx context.Context, userID, stage, verdict string, limit, offset int) ([]model.ReflectionDecision, int64, error) {
	if limit <= 0 {
		limit = 50
	}
	// 通过 trace 关联到 user
	q := r.db.WithContext(ctx).Model(&model.ReflectionDecision{}).
		Joins("JOIN reflection_traces ON reflection_traces.id = reflection_decisions.trace_id").
		Where("reflection_traces.user_id = ?", userID)
	if stage != "" {
		q = q.Where("reflection_decisions.stage = ?", stage)
	}
	if verdict != "" {
		q = q.Where("reflection_decisions.verdict = ?", verdict)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var list []model.ReflectionDecision
	err := q.Select("reflection_decisions.*").Order("reflection_decisions.created_at DESC").
		Limit(limit).Offset(offset).Find(&list).Error
	return list, total, err
}

// GetTraceByID 获取单条 trace 详情（含校验用户）
func (r *ReflectionTraceRepo) GetTraceByID(ctx context.Context, id string) (*model.ReflectionTrace, error) {
	var t model.ReflectionTrace
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&t).Error
	if err != nil {
		return nil, err
	}
	return &t, nil
}
