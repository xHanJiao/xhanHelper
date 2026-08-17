package tool

import (
	"encoding/json"
	"log"

	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// PlanTool — 执行计划（todo）工具：save（全量替换）+ get（读取当前会话计划）。
// 轻量设计（对齐 opencode todo）：无复杂状态机，全量替换语义；
// 方案正文在消息流/文档中，本工具只管理执行步骤追踪。
// plan.save 是写操作：plan 模式（只读）下会被 Registry 拦截，仅 work 模式可用。
type PlanTool struct {
	planRepo *service.ConversationPlanRepo
}

func NewPlanTool(planRepo *service.ConversationPlanRepo) *PlanTool {
	return &PlanTool{planRepo: planRepo}
}

func (t *PlanTool) Name() string { return "plan" }

func (t *PlanTool) Description() string {
	return "执行计划（todo）管理：保存/更新当前会话的执行步骤清单，供执行时追踪进度。方案讨论产出后用 plan.save 保存实施步骤，执行中每完成一步重传完整列表（将该步标为 completed）。"
}

func (t *PlanTool) Catalog() (CatalogEntry, bool) {
	return CatalogEntry{
		ShortDesc: "执行计划。存/读当前会话步骤清单（todo）。",
		Funcs:     []string{"save", "get"},
	}, true
}

func (t *PlanTool) Functions() []FuncSchema {
	return []FuncSchema{
		{
			Name:    "save",
			Display: "保存执行计划",
			Description: "保存当前会话的执行计划（全量替换语义：每次传入完整步骤列表，覆盖之前保存的全部步骤）。" +
				"status 取值：pending（未开始，默认）/ in_progress（进行中，同时只能有一个）/ completed（已完成）/ cancelled（已取消）。" +
				"执行中每完成一步，重传完整列表并更新对应步骤 status。goal 为可选的计划标题。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"goal": map[string]interface{}{
						"type":        "string",
						"description": "计划标题/目标（可选，展示用）",
					},
					"steps": map[string]interface{}{
						"type": "array",
						"items": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"content": map[string]interface{}{
									"type":        "string",
									"description": "步骤内容",
								},
								"status": map[string]interface{}{
									"type":        "string",
									"enum":        []interface{}{"pending", "in_progress", "completed", "cancelled"},
									"description": "步骤状态，省略默认 pending",
								},
							},
							"required": []interface{}{"content"},
						},
						"description": "完整步骤列表（全量替换，非增量）",
					},
				},
				"required": []interface{}{"steps"},
			},
		},
		{
			Name:    "get",
			Display: "读取执行计划",
			Description: "读取当前会话已保存的执行计划（goal + 完整步骤列表）。" +
				"当需要确认当前计划内容、或历史消息过长时使用。",
			Parameters: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
	}
}

func (t *PlanTool) ReadOnlyFuncs() map[string]bool {
	return map[string]bool{"get": true} // save 是写：plan 模式被拦截，仅 work 模式可用
}

func (t *PlanTool) Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result {
	switch funcName {
	case "save":
		return t.save(ec, args)
	case "get":
		return t.get(ec)
	default:
		return &Result{Success: false, Error: "unknown function: " + funcName}
	}
}

func (t *PlanTool) save(ec *ExecuteContext, args json.RawMessage) *Result {
	if ec.ConvID == "" || ec.UserID == "" {
		return &Result{Success: false, Error: "缺少会话上下文"}
	}
	var req struct {
		Goal  string `json:"goal"`
		Steps []struct {
			Content string `json:"content"`
			Status  string `json:"status"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(args, &req); err != nil {
		return &Result{Success: false, Error: "参数解析失败: " + err.Error()}
	}
	if len(req.Steps) == 0 {
		return &Result{Success: false, Error: "steps 不能为空"}
	}
	steps := make([]model.PlanStep, 0, len(req.Steps))
	for _, s := range req.Steps {
		if s.Content == "" {
			return &Result{Success: false, Error: "步骤 content 不能为空"}
		}
		status := s.Status
		if status == "" {
			status = "pending"
		}
		switch status {
		case "pending", "in_progress", "completed", "cancelled":
		default:
			return &Result{Success: false, Error: "非法 status: " + status}
		}
		steps = append(steps, model.PlanStep{Content: s.Content, Status: status})
	}

	if err := t.planRepo.Upsert(ec.Ctx, ec.UserID, ec.ConvID, req.Goal, steps); err != nil {
		log.Printf("[plan] save error: %v", err)
		return &Result{Success: false, Error: "保存计划失败"}
	}

	data, _ := json.Marshal(map[string]interface{}{
		"goal":  req.Goal,
		"steps": steps,
		"note":  "计划已保存（全量替换）。执行中每完成一步，重传完整列表并更新对应步骤 status。",
	})
	return &Result{Success: true, Data: data}
}

func (t *PlanTool) get(ec *ExecuteContext) *Result {
	if ec.ConvID == "" || ec.UserID == "" {
		return &Result{Success: false, Error: "缺少会话上下文"}
	}
	p, err := t.planRepo.Get(ec.Ctx, ec.ConvID, ec.UserID)
	if err != nil {
		log.Printf("[plan] get error: %v", err)
		return &Result{Success: false, Error: "读取计划失败"}
	}
	if p == nil {
		data, _ := json.Marshal(map[string]interface{}{"goal": "", "steps": []model.PlanStep{}, "note": "当前会话没有已保存的执行计划"})
		return &Result{Success: true, Data: data}
	}
	data, _ := json.Marshal(map[string]interface{}{
		"goal":  p.Goal,
		"steps": service.ParsePlanSteps(p),
	})
	return &Result{Success: true, Data: data}
}
