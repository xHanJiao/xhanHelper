package tool

import (
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"

	"xhan-helper/internal/app"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// ReminderTool — 提醒工具，LLM 在对话中直接调用
type ReminderTool struct {
	commitRepo *service.CommitmentRepo
	timer      app.CommitmentTimer
}

func NewReminderTool(commitRepo *service.CommitmentRepo, timer app.CommitmentTimer) *ReminderTool {
	return &ReminderTool{commitRepo: commitRepo, timer: timer}
}

func (t *ReminderTool) Name() string { return "reminder" }
func (t *ReminderTool) Description() string {
	return "设置待办提醒，支持一次性或循环提醒"
}

func (t *ReminderTool) Functions() []FuncSchema {
	return []FuncSchema{{
		Name:        "create_reminder",
		Description: "创建一个待办提醒。用户说「提醒我XX」「帮我记一下XX」「别忘了XX」时调用。deadline 为 ISO8601 时间，无法确定则不传。循环提醒传 recurrence_rule：每天→{\"type\":\"daily\",\"time\":\"HH:MM\"}；每周→{\"type\":\"weekly\",\"time\":\"HH:MM\",\"days\":[\"monday\",\"wednesday\"]}；每月→{\"type\":\"monthly\",\"time\":\"HH:MM\",\"month_days\":[1,15]}；工作日→{\"type\":\"weekday\",\"time\":\"HH:MM\"}",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"content":         map[string]interface{}{"type": "string", "description": "提醒内容"},
				"deadline":        map[string]interface{}{"type": "string", "description": "ISO8601 截止时间，如 2026-05-17T18:00:00+08:00"},
				"recurrence_rule": map[string]interface{}{"type": "object", "description": "循环规则"},
			},
			"required": []string{"content"},
		},
	}}
}

func (t *ReminderTool) Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result {
	var req struct {
		Content        string               `json:"content"`
		Deadline       string               `json:"deadline"`
		RecurrenceRule *recurrenceRuleInput `json:"recurrence_rule"`
	}
	if err := json.Unmarshal(args, &req); err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("参数解析失败: %v", err)}
	}
	if req.Content == "" {
		return &Result{Success: false, Error: "content 不能为空"}
	}

	commit := &model.Commitment{
		ID:      uuid.New().String(),
		UserID:  ec.UserID,
		Content: req.Content,
	}

	if req.Deadline != "" {
		if t, err := time.Parse(time.RFC3339, req.Deadline); err == nil {
			commit.Deadline = &t
			commit.NextDeadline = &t
		}
	}

	// 循环规则：直接存为 JSON
	if req.RecurrenceRule != nil && req.RecurrenceRule.Type != "" {
		ruleJSON, _ := json.Marshal(req.RecurrenceRule)
		ruleStr := string(ruleJSON)
		commit.RecurrenceRule = &ruleStr
	}

	if err := t.commitRepo.Create(ec.Ctx, commit); err != nil {
		log.Printf("[reminder] create error: %v", err)
		return &Result{Success: false, Error: fmt.Sprintf("创建提醒失败: %v", err)}
	}

	if t.timer != nil {
		t.timer.Schedule(commit)
	}

	log.Printf("[reminder] created: %s deadline=%v", commit.ID[:8], commit.NextDeadline)
	data, _ := json.Marshal(map[string]interface{}{
		"id":       commit.ID,
		"content":  commit.Content,
		"deadline": commit.NextDeadline,
		"status":   "created",
	})
	return &Result{Success: true, Data: data}
}

type recurrenceRuleInput struct {
	Type      string        `json:"type"`
	Time      string        `json:"time"`
	Days      []string      `json:"days,omitempty"`
	MonthDays []interface{} `json:"month_days,omitempty"`
}
