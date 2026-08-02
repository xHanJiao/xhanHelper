package service

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"xhan-helper/internal/model"
)

// ConversationPlanRepo — 会话执行计划存储（每会话一行，全量替换）
type ConversationPlanRepo struct {
	db *gorm.DB
}

func NewConversationPlanRepo(db *gorm.DB) *ConversationPlanRepo {
	return &ConversationPlanRepo{db: db}
}

// Upsert 全量替换当前会话的计划（每会话一行）。
func (r *ConversationPlanRepo) Upsert(ctx context.Context, userID, convID, goal string, steps []model.PlanStep) error {
	stepsJSON, err := json.Marshal(steps)
	if err != nil {
		return err
	}
	var count int64
	if err := r.db.WithContext(ctx).Model(&model.ConversationPlan{}).
		Where("conversation_id = ? AND user_id = ?", convID, userID).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return r.db.WithContext(ctx).Model(&model.ConversationPlan{}).
			Where("conversation_id = ? AND user_id = ?", convID, userID).
			Updates(map[string]interface{}{"goal": goal, "steps_json": string(stepsJSON)}).Error
	}
	return r.db.WithContext(ctx).Create(&model.ConversationPlan{
		ID:             uuid.New().String(),
		UserID:         userID,
		ConversationID: convID,
		Goal:           goal,
		StepsJSON:      string(stepsJSON),
	}).Error
}

// Get 读取当前会话的计划，不存在返回 nil。
func (r *ConversationPlanRepo) Get(ctx context.Context, convID, userID string) (*model.ConversationPlan, error) {
	var p model.ConversationPlan
	if err := r.db.WithContext(ctx).
		Where("conversation_id = ? AND user_id = ?", convID, userID).
		First(&p).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &p, nil
}

// ParseSteps 解析 StepsJSON 为步骤数组（非法 JSON 返回空数组）。
func ParsePlanSteps(p *model.ConversationPlan) []model.PlanStep {
	if p == nil || p.StepsJSON == "" {
		return nil
	}
	var steps []model.PlanStep
	if err := json.Unmarshal([]byte(p.StepsJSON), &steps); err != nil {
		return nil
	}
	return steps
}
