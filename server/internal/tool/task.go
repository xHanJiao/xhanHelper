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
	// 子 agent 预算（描述动态注入，主 LLM 据此判断任务该不该拆）：
	maxIterations int // 含强制收敛轮的最大轮次，0 = 用默认 20
	timeoutSecs   int // 执行超时秒数，0 = 默认 600
}

func NewTaskTool(runner SubAgentRunner, maxIterations, timeoutSecs int) *TaskTool {
	return &TaskTool{runner: runner, maxIterations: maxIterations, timeoutSecs: timeoutSecs}
}

func (t *TaskTool) Name() string                   { return "task" }
func (t *TaskTool) ReadOnlyFuncs() map[string]bool { return AllReadOnly(t) }

func (t *TaskTool) Description() string {
	maxIter := t.maxIterations
	if maxIter <= 0 {
		maxIter = 20
	}
	timeout := t.timeoutSecs
	if timeout <= 0 {
		timeout = 600
	}
	return fmt.Sprintf(`启动一个子 agent 独立完成单一目标的小粒度任务，执行完毕后返回结果。

核心原则：一个 task 只做一件事。任务应在子 agent 有限的轮次预算内完成；大任务拆成多个 task 并行，不要塞进一个 task。

子 agent 预算：最多执行 %d 轮工具调用（第 %d 轮为强制总结轮，届时禁止调用工具），总时长约 %d 秒。超出预算会被强制收敛/超时截断，任务颗粒度必须明显小于该预算——预计接近或超过预算的任务必须拆成多个 task。

何时使用：
- 需要联网调研多步信息（搜索 → 浏览网页 → 提炼结论）→ web_explore
- 需要在用户本地文件系统上搜索/阅读多个文件（代码、文档、配置等）→ file_explore
- 需要检索用户自身长期积累的信息（画像/记忆/笔记/摘要/主题/历史对话）做多轮深挖 → user_context（纯服务端，无需 Mac 客户端）
- 多个独立子任务可并行（利用并行加速）
- 任务量大、需要独立上下文避免污染当前对话

何时不用：
- 单次简单的查询/计算/查笔记 → 直接用对应工具
- 目标不明确、方向需用户中途决策 → 先与用户澄清，不要贸然派 task
- 任务太大（预计超出子 agent 轮次预算）→ 拆成多个 task

派发纪律：
- 派之前先列清每个 task 负责哪些文件/函数，确认互不交叠；宁可 2 个切干净，不要 3 个有交叠
- 每个 task 的 prompt 里写一行优先级（预算不足时先保证哪几条有结论）
- 任务描述里的前提标注【已确认】/【推断】，并要求子任务顺手证伪
- 上批 task 的 open_questions 未读项，贴进新任务 prompt，或带 task_id 续跑同一子任务

用法：
- 子任务的内部思考与最终回复不展示给用户（用户仅看到工具调用过程），你必须用自然语言总结转发给用户
- prompt 要写详细：单一目标、明确要调研什么、返回什么格式
- 续跑：任务因预算耗尽/超时只返回部分结果时（返回里带 <task id state>），携带其中 id 作为 task_id 参数再次调用即可续跑同一子任务——复用此前全部调研上下文（注意：上一轮结束时浏览器标签已被自动关闭，需重新打开），预算重新计算`, maxIter-1, maxIter, timeout)
}

func (t *TaskTool) Catalog() (CatalogEntry, bool) {
	return CatalogEntry{
		ShortDesc: "子任务。派子 agent 独立干活：联网调研 web_explore、本地文件 file_explore、用户信息检索 user_context。",
		Funcs:     []string{"run"},
	}, true
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
- user_context：用户信息检索（画像/记忆/笔记/摘要/主题/历史，只读，纯服务端无需客户端）
web_explore / file_explore 需要 macOS 客户端在线；客户端离线时这两个子任务无法执行。user_context 不依赖客户端。`,
				},
				"prompt": map[string]interface{}{
					"type": "string",
					"description": `给子 agent 的完整任务描述。必须自包含（子 agent 不共享当前对话上下文）。

任务必须单一目标、小粒度（一次信息收集循环能完成）；大任务拆成多个 task 并行。

写法要求：
- 开头写一行优先级：预算不足时优先保证哪几条有结论
- 前提标注【已确认】/【推断】，并要求子任务顺手验证（无法验证要标出矛盾或存疑）
- 派多个 task 前确认各自负责的文件/函数不交叠；宁可 2 个切干净，不要 3 个有交叠
- 上批 task 返回里的 open_questions 未读项，贴进新任务的 prompt，或带 task_id 续跑同一子任务

拆分示例（用户请求 → 多个子 task）：
- "调研 Vue/React/Svelte 哪个适合我" → 拆 3 个 task："调研 Vue 3.5 特性与适用场景"、"调研 React 19 特性与适用场景"、"调研 Svelte 5 特性与适用场景"，全部完成后由你综合对比给出建议
- "调研 AI 行业动态" → 目标太宽泛，先与用户确认具体方向（如大模型/Agent/具身智能），再按主题拆 task
- "调研 DeepSeek 上下文缓存机制" → 单一主题，一个 task 搞定：机制原理、计费方式、适用场景
- "帮我搞定毕业论文" → 粒度太大且需用户中途决策，应分步进行，不要一次派 task`,
				},
				"task_id": map[string]interface{}{
					"type":        "string",
					"description": "可选。继续之前返回的 <task id='...'> 中的任务 ID，复用同一子会话上下文继续工作（注意：上一轮结束时浏览器标签已被自动关闭，续跑需重新打开）；不传则新建子任务。",
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
		TaskID       string `json:"task_id"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("参数解析失败: %v", err)}
	}

	opts := &SubAgentRunOptions{
		SubAgentType: params.SubAgentType,
		Description:  params.Description,
		Prompt:       params.Prompt,
		TaskID:       params.TaskID,
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
