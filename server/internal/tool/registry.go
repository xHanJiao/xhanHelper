package tool

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// Registry — 工具注册表
type Registry struct {
	tools map[string]Tool
}

// NewRegistry 创建空的注册表
func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool)}
}

// Register 注册一个工具
func (r *Registry) Register(t Tool) {
	r.tools[t.Name()] = t
}

// Get 按名称获取工具
func (r *Registry) Get(name string) Tool {
	return r.tools[name]
}

// GeneratePrompt 生成工具定义文本，追加到 system prompt 中
func (r *Registry) GeneratePrompt() string {
	var buf bytes.Buffer
	buf.WriteString("\n\n===== 工具使用 =====\n\n")
	buf.WriteString("你可以使用以下工具来完成用户的请求。\n")
	buf.WriteString("当需要执行操作时，在回复中输出 JSON 格式。\n\n")

	for _, t := range r.tools {
		buf.WriteString(fmt.Sprintf("## 工具: %s\n", t.Name()))
		buf.WriteString(fmt.Sprintf("描述: %s\n\n", t.Description()))
		buf.WriteString("函数:\n")
		for _, fn := range t.Functions() {
			buf.WriteString(fmt.Sprintf("  - %s: %s\n", fn.Name, fn.Description))
			if props, ok := fn.Parameters["properties"].(map[string]interface{}); ok {
				required := make(map[string]bool)
				if reqs, ok := fn.Parameters["required"].([]interface{}); ok {
					for _, r := range reqs {
						required[r.(string)] = true
					}
				}
				for key, val := range props {
					prop := val.(map[string]interface{})
					typ, _ := prop["type"].(string)
					desc, _ := prop["description"].(string)
					req := ""
					if required[key] {
						req = " (必填)"
					}
					buf.WriteString(fmt.Sprintf("    - %s (%s%s): %s\n", key, typ, req, desc))
					// 显示枚举值列表
					if enumVals, ok := prop["enum"].([]interface{}); ok && len(enumVals) > 0 {
						vals := make([]string, len(enumVals))
						for i, v := range enumVals {
							vals[i] = fmt.Sprint(v)
						}
						buf.WriteString(fmt.Sprintf("      可选值: %s\n", strings.Join(vals, "/")))
					}
				}
				// 显示 required 汇总（已在上面的循环标识，这里省略）
				// buf.WriteString(fmt.Sprintf("    必填字段: %s\n", ...))
			}
			buf.WriteString("\n")
		}
	}

	buf.WriteString("===== 输出格式 =====\n\n")
	buf.WriteString("需要调用工具时，回复一个 JSON 对象：\n\n")
	buf.WriteString("{\n")
	buf.WriteString(`    "reply": "给用户的自然语言回复",` + "\n")
	buf.WriteString(`    "tool_calls": [` + "\n")
	buf.WriteString("        {\n")
	buf.WriteString(`            "tool": "<工具名>",` + "\n")
	buf.WriteString(`            "function": "<函数名>",` + "\n")
	buf.WriteString(`            "args": { /* 参数 */ }` + "\n")
	buf.WriteString("        }\n")
	buf.WriteString("    ]\n")
	buf.WriteString("}\n\n")
	buf.WriteString("如果不需要使用工具（纯聊天），直接回复纯文本即可。\n")
	buf.WriteString("一条回复中 tool_calls 可以包含多个工具调用，按顺序执行。\n")
	buf.WriteString("工具执行结果会追加到对话中，你可以据此继续回复用户。\n\n")

	buf.WriteString("===== 重要规则 =====\n\n")
	buf.WriteString("1. 带枚举值的字段必须严格从可选值中选择，不得自创分类！\n")
	buf.WriteString("2. 如果用户描述模糊无法确定分类，必须用纯文本回复询问用户，不要猜测！\n")
	buf.WriteString("   示例回复: \"这个消费是餐饮还是购物呢？\"\n")
	buf.WriteString("3. 只有用户明确或根据上下文99%确定时，才调用工具。\n")

	return buf.String()
}

// ToolCall — 解析出的工具调用
type ToolCall struct {
	Tool     string          `json:"tool"`
	Function string          `json:"function"`
	Args     json.RawMessage `json:"args"`
}

// ExecuteByName 按名称查找工具并执行指定函数
func (r *Registry) ExecuteByName(ec *ExecuteContext, toolName, funcName string, args json.RawMessage) *Result {
	t := r.tools[toolName]
	if t == nil {
		return &Result{Success: false, Error: fmt.Sprintf("unknown tool: %s", toolName)}
	}
	return t.Execute(ec, funcName, args)
}
