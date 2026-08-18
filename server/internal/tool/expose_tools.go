package tool

import (
	"encoding/json"
)

// ExposeSpec — expose_tools.enable 的单项：工具组名 + 要启用的函数名列表（必填非空）。
type ExposeSpec struct {
	Tool      string   `json:"name"`
	Functions []string `json:"functions"`
}

// ExposeResult — expose_tools 执行结果：成功启用的条目、被拒绝的条目、可用组全集。
// Enabled/Rejected 条目为函数粒度 "组名.函数名"；组级校验失败（不存在/隐藏/排除）时以组名入 Rejected。
// 由 Orchestrator 注入的回调（ExecuteContext.Expose）生成，工具只负责格式化返回给 LLM。
type ExposeResult struct {
	Enabled   []string `json:"enabled"`
	Rejected  []string `json:"rejected"`
	Available []string `json:"available"` // 可启用的全部工具组名（便于 LLM 纠正）
}

// ExposeToolsTool — 渐进式工具暴露元工具（唯一常驻工具）。
// LLM 通过 enable 指定需要启用的工具函数，Orchestrator 据此把对应函数 schema
// 加入后续请求体的 tools 字段。执行层不做事，只委托 ExecuteContext.Expose 回调。
type ExposeToolsTool struct{}

// NewExposeToolsTool 创建元工具
func NewExposeToolsTool() *ExposeToolsTool { return &ExposeToolsTool{} }

func (t *ExposeToolsTool) Name() string { return "expose_tools" }

func (t *ExposeToolsTool) Description() string {
	return "按函数粒度启用工具。传入 [{name:\"组名\", functions:[\"函数名\"]}]，只启用列出的函数，启用后下一轮起即可直接调用。组名与函数名见系统提示词中的工具目录。"
}

func (t *ExposeToolsTool) ReadOnlyFuncs() map[string]bool { return nil }

func (t *ExposeToolsTool) Functions() []FuncSchema {
	return []FuncSchema{
		{
			Name:        "enable",
			Display:     "启用工具函数",
			Description: "启用一个或多个函数：传入 [{name:\"组名\", functions:[\"函数名\",...]}]（组名与函数名见系统提示词中的工具目录）。一次可启用多个组的多个函数，启用后下一轮即可直接调用。返回已启用、被拒绝的条目（组级失败显示组名，函数级失败显示\"组名.函数名\"）及全部可用组名。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"tools": map[string]interface{}{
						"type": "array",
						"items": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"name":      map[string]interface{}{"type": "string", "description": "工具组名"},
								"functions": map[string]interface{}{"type": "array", "items": map[string]string{"type": "string"}, "description": "要启用的函数名列表"},
							},
							"required": []interface{}{"name", "functions"},
						},
						"description": "要启用的工具及函数列表",
					},
				},
				"required": []interface{}{"tools"},
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
		Tools []ExposeSpec `json:"tools"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return &Result{Success: false, Error: "invalid args: " + err.Error()}
	}
	if len(a.Tools) == 0 {
		return &Result{Success: false, Error: "tools 不能为空，请传入 [{name:\"组名\", functions:[\"函数名\"]}]"}
	}
	for _, s := range a.Tools {
		if len(s.Functions) == 0 {
			return &Result{Success: false, Error: "functions 不能为空，请为 " + s.Tool + " 至少指定一个函数名"}
		}
	}
	res := ec.Expose(a.Tools)
	data, _ := json.Marshal(res)
	return &Result{Success: true, Data: data}
}
