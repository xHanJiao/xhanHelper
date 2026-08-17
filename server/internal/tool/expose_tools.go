package tool

import (
	"encoding/json"
)

// ExposeResult — expose_tools 执行结果：成功启用的组、被拒绝的组、可用组全集。
// 由 Orchestrator 注入的回调（ExecuteContext.Expose）生成，工具只负责格式化返回给 LLM。
type ExposeResult struct {
	Enabled   []string `json:"enabled"`
	Rejected  []string `json:"rejected"`
	Available []string `json:"available"` // 可启用的全部工具组名（便于 LLM 纠正）
}

// ExposeToolsTool — 渐进式工具暴露元工具（唯一常驻工具）。
// LLM 通过 enable 指定需要启用的工具组，Orchestrator 据此把该组完整 schema
// 加入后续请求体的 tools 字段。执行层不做事，只委托 ExecuteContext.Expose 回调。
type ExposeToolsTool struct{}

// NewExposeToolsTool 创建元工具
func NewExposeToolsTool() *ExposeToolsTool { return &ExposeToolsTool{} }

func (t *ExposeToolsTool) Name() string { return "expose_tools" }

func (t *ExposeToolsTool) Description() string {
	return "启用工具组。传组名列表（见系统提示词工具目录）。启用后下一轮起其函数即可直接调用。"
}

func (t *ExposeToolsTool) ReadOnlyFuncs() map[string]bool { return nil }

func (t *ExposeToolsTool) Functions() []FuncSchema {
	return []FuncSchema{
		{
			Name:        "enable",
			Display:     "启用工具组",
			Description: "启用一个或多个工具组，传入组名列表（组名见系统提示词中的工具目录）。一次可启用多个组，启用后下一轮即可直接调用这些组的函数。返回已启用、被拒绝及全部可用组名。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"groups": map[string]interface{}{
						"type":        "array",
						"items":       map[string]string{"type": "string"},
						"description": "要启用的工具组名列表",
					},
				},
				"required": []interface{}{"groups"},
			},
		},
	}
}

func (t *ExposeToolsTool) Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result {
	if funcName != "enable" {
		return &Result{Success: false, Error: "unknown function: " + funcName}
	}
	if ec.Expose == nil {
		return &Result{Success: false, Error: "工具暴露功能当前上下文不可用（非主循环场景），请直接使用已提供的工具"}
	}
	var a struct {
		Groups []string `json:"groups"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return &Result{Success: false, Error: "invalid args: " + err.Error()}
	}
	if len(a.Groups) == 0 {
		return &Result{Success: false, Error: "groups 不能为空，请传入要启用的工具组名"}
	}
	res := ec.Expose(a.Groups)
	data, _ := json.Marshal(res)
	return &Result{Success: true, Data: data}
}
