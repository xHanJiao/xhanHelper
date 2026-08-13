package tool

import (
	"encoding/json"
	"fmt"
	"strings"
)

// TaskTool — 启动子 agent 独立完成任务的工具（opencode task 机制）。
// 仅由 Orchestrator 实现 SubAgentRunner 后二次注册（见 appinit/agents.go），本文件不注册。
type TaskTool struct {
	runner SubAgentRunner // nil = 未启用（Phase 18-2 接线前不应触发）
}

func NewTaskTool(runner SubAgentRunner) *TaskTool {
	return &TaskTool{runner: runner}
}

func (t *TaskTool) Name() string                   { return "task" }
func (t *TaskTool) ReadOnlyFuncs() map[string]bool { return AllReadOnly(t) }

func (t *TaskTool) Description() string {
	return `启动一个子 agent 独立完成复杂任务，执行完毕后返回结果。

何时使用：
- 需要联网调研多步信息（搜索 → 抓取多个网页 → 提炼结论）
- 多个独立任务可并行执行
- 任务量大、需要独立上下文避免污染当前对话

何时不用：
- 单次简单的查询/计算/查笔记 → 直接用对应工具
- 任务短小、一两个工具调用就能完成

用法：
- 可一次并发多个 task（利用并行加速）
- 子任务的内部思考与最终回复不展示给用户（用户仅看到子任务的工具调用过程），你必须用自然语言总结转发给用户
- prompt 要写详细：明确要做什么、期望返回什么、用什么格式`
}

func (t *TaskTool) Functions() []FuncSchema {
	return []FuncSchema{{
		Name:        "run",
		Display:     "启动子任务",
		Description: "启动子 agent 执行任务。子 agent 在独立上下文中运行，完成后返回结果。可并发执行多个 task。",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"description": map[string]interface{}{
					"type":        "string",
					"description": "任务简短描述（3-5 词，如 调研XX技术）",
				},
				"subagent_type": map[string]interface{}{
					"type":        "string",
					"description": "子 agent 类型，当前可用：explore（联网调研，只读）",
				},
				"prompt": map[string]interface{}{
					"type":        "string",
					"description": "给子 agent 的完整任务描述。必须自包含（子 agent 不共享当前对话上下文）。写明要调研什么、要返回什么、遵循什么格式。",
				},
			},
			"required": []interface{}{"description", "subagent_type", "prompt"},
		},
	}}
}

func (t *TaskTool) Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result {
	if funcName != "run" {
		return &Result{Success: false, Error: fmt.Sprintf("未知函数: %s", funcName)}
	}
	if t.runner == nil {
		return &Result{Success: false, Error: "task 工具尚未启用（runner 未注入）"}
	}
	var params struct {
		Description  string `json:"description"`
		SubAgentType string `json:"subagent_type"`
		Prompt       string `json:"prompt"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("参数解析失败: %v", err)}
	}

	opts := &SubAgentRunOptions{
		SubAgentType: params.SubAgentType,
		Description:  params.Description,
		Prompt:       params.Prompt,
		ParentConvID: ec.ConvID,
		ClientTools:  ec.ClientTools,
		MainEvents:   ec.Events,
	}
	result := t.runner.RunSubAgent(ec.Ctx, opts)

	// 失败且无任何产出 → 硬错误（主 LLM 需知道子任务不可用）
	if !result.Success && strings.TrimSpace(result.Text) == "" {
		return &Result{Success: false, Error: result.Error}
	}

	// 正常完成，或超时/失败但有部分产出（Text 为工具日志摘要）→ 包裹返回，
	// 让主 LLM 利用部分成果。state 由 runner 标注实际状态。
	state := result.State
	if state == "" {
		state = "completed"
	}
	summary := fmt.Sprintf("子任务完成: %s", params.Description)
	if !result.Success {
		summary = fmt.Sprintf("子任务%s（含部分结果）: %s", state, params.Description)
	}

	// 包裹结果（opencode renderOutput 风格）
	output := fmt.Sprintf(
		`<task id="%s" state="%s">
<summary>%s</summary>
<task_result>
%s
</task_result>
</task>`,
		result.ConvID, state, summary, result.Text,
	)
	data, _ := json.Marshal(map[string]string{"result": output})
	return &Result{Success: true, Data: data}
}
