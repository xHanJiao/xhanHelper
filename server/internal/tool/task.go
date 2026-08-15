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
	return `启动一个子 agent 独立完成单一目标的小粒度任务，执行完毕后返回结果。

核心原则：一个 task 只做一件事。任务应在子 agent 有限的轮次预算内完成；大任务拆成多个 task 并行，不要塞进一个 task。

何时使用：
- 需要联网调研多步信息（搜索 → 浏览网页 → 提炼结论）→ web_explore
- 需要在用户本地文件系统上搜索/阅读多个文件（代码、文档、配置等）→ file_explore
- 多个独立子任务可并行（利用并行加速）
- 任务量大、需要独立上下文避免污染当前对话

何时不用：
- 单次简单的查询/计算/查笔记 → 直接用对应工具
- 目标不明确、方向需用户中途决策 → 先与用户澄清，不要贸然派 task
- 任务太大（预计超出子 agent 轮次预算）→ 拆成多个 task

用法：
- 子任务的内部思考与最终回复不展示给用户（用户仅看到工具调用过程），你必须用自然语言总结转发给用户
- prompt 要写详细：单一目标、明确要调研什么、返回什么格式`
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
					"description": "任务简短描述（3-5 词，用作子任务标题，如 调研XX技术）",
				},
				"subagent_type": map[string]interface{}{
					"type": "string",
					"description": `子 agent 类型：
- web_explore：联网调研（基于 Mac 浏览器，只读）
- file_explore：本地文件检索（搜索/阅读用户 Mac 上的文件，只读）
两者都需要 macOS 客户端在线；客户端离线时子任务无法执行。`,
				},
				"prompt": map[string]interface{}{
					"type": "string",
					"description": `给子 agent 的完整任务描述。必须自包含（子 agent 不共享当前对话上下文）。

任务必须单一目标、小粒度（一次信息收集循环能完成）；大任务拆成多个 task 并行。

拆分示例（用户请求 → 多个子 task）：
- "调研 Vue/React/Svelte 哪个适合我" → 拆 3 个 task："调研 Vue 3.5 特性与适用场景"、"调研 React 19 特性与适用场景"、"调研 Svelte 5 特性与适用场景"，全部完成后由你综合对比给出建议
- "调研 AI 行业动态" → 目标太宽泛，先与用户确认具体方向（如大模型/Agent/具身智能），再按主题拆 task
- "调研 DeepSeek 上下文缓存机制" → 单一主题，一个 task 搞定：机制原理、计费方式、适用场景
- "帮我搞定毕业论文" → 粒度太大且需用户中途决策，应分步进行，不要一次派 task`,
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
