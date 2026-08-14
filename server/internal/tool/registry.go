package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
)

// Registry — 工具注册表
type Registry struct {
	tools       map[string]Tool
	hiddenNames map[string]bool
}

// NewRegistry 创建空的注册表
func NewRegistry() *Registry {
	return &Registry{
		tools:       make(map[string]Tool),
		hiddenNames: make(map[string]bool),
	}
}

// ValidateReadOnlyFuncs 校验所有工具的 ReadOnlyFuncs 声明：函数名必须存在于 Functions()。
// 在 Register 完成后调用，发现拼写错误直接 panic（防止只读声明失效导致 plan 模式漏拦）。
func (r *Registry) ValidateReadOnlyFuncs() {
	for toolName, t := range r.tools {
		if r.hiddenNames[toolName] {
			continue
		}
		defined := make(map[string]bool)
		for _, fn := range t.Functions() {
			defined[fn.Name] = true
		}
		for fnName := range t.ReadOnlyFuncs() {
			if !defined[fnName] {
				panic(fmt.Sprintf("[tool] ReadOnlyFuncs references undefined function: %s.%s (must match Functions())", toolName, fnName))
			}
		}
	}
}

// ValidateNativeFC 校验所有非隐藏工具的函数名不含 "__"（避免扁平化命名冲突）。
// 在 Register 完成后调用，发现冲突直接 panic。
func (r *Registry) ValidateNativeFC() {
	for toolName, t := range r.tools {
		if r.hiddenNames[toolName] {
			continue
		}
		for _, fn := range t.Functions() {
			if strings.Contains(fn.Name, "__") {
				panic(fmt.Sprintf("[tool] function name contains reserved separator \"__\": %s.%s", toolName, fn.Name))
			}
		}
	}
}

// HideTool 将工具从 prompt 中隐藏（不会告诉 LLM，但 ExecuteByName 仍可用）
func (r *Registry) HideTool(name string) {
	r.hiddenNames[name] = true
}

// SearchTools 在内存中的工具（含 MCP 占位工具）里关键词搜索。
// hidden 工具不参与搜索（tool_retriever 查不到，主循环 LLM 无途径获知）。
func (r *Registry) SearchTools(query string) []ToolInfo {
	var results []ToolInfo
	q := strings.ToLower(query)
	for name, t := range r.tools {
		if r.hiddenNames[name] {
			continue
		}
		desc := strings.ToLower(t.Description())
		if strings.Contains(name, q) || strings.Contains(desc, q) {
			funcs := t.Functions()
			funcInfos := make([]ToolFunctionInfo, len(funcs))
			for i, f := range funcs {
				funcInfos[i] = ToolFunctionInfo{
					Name:        f.Name,
					Description: f.Description,
					Parameters:  f.Parameters,
				}
			}
			results = append(results, ToolInfo{
				ID:          name,
				Name:        name,
				Description: t.Description(),
				Functions:   funcInfos,
			})
		}
	}
	return results
}

// DynamicToolLoader — 动态工具加载器接口（由 service.ToolRepo 实现）
type DynamicToolLoader interface {
	ListActiveTools(ctx context.Context) ([]model.ToolRegistry, error)
}

// Register 注册一个工具
func (r *Registry) Register(t Tool) {
	r.tools[t.Name()] = t
}

// Get 按名称获取工具
func (r *Registry) Get(name string) Tool {
	return r.tools[name]
}

// GetToolMap 返回所有工具的 map（用于创建 ReactLoop 等场景）
func (r *Registry) GetToolMap() map[string]Tool {
	out := make(map[string]Tool, len(r.tools))
	for k, v := range r.tools {
		out[k] = v
	}
	return out
}

// Has 判断注册表中是否存在指定名称的工具
func (r *Registry) Has(name string) bool {
	_, ok := r.tools[name]
	return ok
}

// sortedVisibleToolNames 返回所有非隐藏工具名，按字典序排序。
// Go map 遍历顺序随机，若直接遍历会导致 LLM 请求体（tools 数组 / 工具定义文本）
// 每次请求字节不同，破坏 DeepSeek 上下文前缀缓存。所有 LLM 可见输出必须走此函数。
func (r *Registry) sortedVisibleToolNames() []string {
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		if r.hiddenNames[name] {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// GetNativeFCTools 导出所有非隐藏工具的函数定义（供 LLM 原生 function calling）。
// 每个函数扁平化为独立的 tool definition，名称格式为 "toolName__funcName"。
// 输出按工具名排序，保证同集合请求的 tools 数组字节稳定。
func (r *Registry) GetNativeFCTools() []llm.ToolDefinition {
	defs := make([]llm.ToolDefinition, 0, len(r.tools))
	for _, name := range r.sortedVisibleToolNames() {
		t := r.tools[name]
		funcs := make([]llm.FuncDef, len(t.Functions()))
		for i, f := range t.Functions() {
			funcs[i] = llm.FuncDef{
				Name:        f.Name,
				Description: f.Description,
				Parameters:  f.Parameters,
			}
		}
		defs = append(defs, llm.ToolDefinition{
			Name:        name,
			Description: t.Description(),
			Functions:   funcs,
		})
	}
	return defs
}

// GetLLMTools 导出所有工具定义（供 LLM native function calling）。
// 输出按工具名排序，保证字节稳定。
func (r *Registry) GetLLMTools() []llm.ToolDefinition {
	defs := make([]llm.ToolDefinition, 0, len(r.tools))
	for _, name := range r.sortedVisibleToolNames() {
		t := r.tools[name]
		funcs := make([]llm.FuncDef, len(t.Functions()))
		for i, f := range t.Functions() {
			funcs[i] = llm.FuncDef{
				Name:        f.Name,
				Description: f.Description,
				Parameters:  f.Parameters,
			}
		}
		defs = append(defs, llm.ToolDefinition{
			Name:        name,
			Description: t.Description(),
			Functions:   funcs,
		})
	}
	return defs
}

// GeneratePromptWithOverview 混排模式：tool_retriever 完整 + note.record_note 内联，其他工具仅概览
func (r *Registry) GeneratePromptWithOverview() string {
	var buf bytes.Buffer
	buf.WriteString("你可以使用以下工具来完成用户的请求。\n")
	buf.WriteString("当需要执行操作时，在回复中输出 JSON 格式。\n\n")

	// tool_retriever 完整定义
	if tr := r.tools["tool_retriever"]; tr != nil {
		r.writeToolFullDef(&buf, tr)
	}

	// note 工具：仅内联 record_note 函数，其他函数仍需 search
	if nt := r.tools["note"]; nt != nil {
		buf.WriteString(fmt.Sprintf("## 工具: %s\n", nt.Name()))
		buf.WriteString(fmt.Sprintf("描述: %s\n\n", nt.Description()))
		buf.WriteString("直接可用函数（无需 search）:\n")
		var otherFuncs []string
		for _, fn := range nt.Functions() {
			if fn.Name == "record_note" {
				r.writeFunctionDef(&buf, fn)
			} else {
				otherFuncs = append(otherFuncs, fn.Name)
			}
		}
		if len(otherFuncs) > 0 {
			buf.WriteString(fmt.Sprintf("其他函数需先 tool_retriever.search(\"note\") 查询后调用: %s\n", strings.Join(otherFuncs, ", ")))
		}
		buf.WriteString("\n")
	}

	// 其他工具概览（需通过 tool_retriever.search 查询详细定义后才能调用）
	var overview []string
	for _, name := range r.sortedVisibleToolNames() {
		if name == "tool_retriever" || name == "note" {
			continue
		}
		t := r.tools[name]
		overview = append(overview, fmt.Sprintf("工具名: %s —— %s\n", name, t.Description()))
	}
	if len(overview) > 0 {
		buf.WriteString("\n## 其他可用工具\n")
		buf.WriteString("【强制规则】以下只是工具概览，不包含函数签名和参数定义。直接调用会失败！必须先 tool_retriever.search(工具名) 获取完整定义后才能调用。禁止凭猜测填写参数！\n\n")
		buf.WriteString(strings.Join(overview, "\n"))
		buf.WriteString("\n")
	}

	r.writePromptFooter(&buf)
	return buf.String()
}

// writeFunctionDef 写入单个函数的完整定义（含参数 schema）
func (r *Registry) writeFunctionDef(buf *bytes.Buffer, fn FuncSchema) {
	buf.WriteString(fmt.Sprintf("  - %s: %s\n", fn.Name, fn.Description))
	if props, ok := fn.Parameters["properties"].(map[string]interface{}); ok {
		required := make(map[string]bool)
		if reqs, ok := fn.Parameters["required"].([]interface{}); ok {
			for _, req := range reqs {
				if s, ok := req.(string); ok {
					required[s] = true
				}
			}
		}
		for key, val := range props {
			prop, ok := val.(map[string]interface{})
			if !ok {
				continue
			}
			typ, _ := prop["type"].(string)
			desc, _ := prop["description"].(string)
			req := ""
			if required[key] {
				req = " (必填)"
			}
			buf.WriteString(fmt.Sprintf("    - %s (%s%s): %s\n", key, typ, req, desc))
			if enumVals, ok := prop["enum"].([]interface{}); ok && len(enumVals) > 0 {
				vals := make([]string, len(enumVals))
				for i, v := range enumVals {
					vals[i] = fmt.Sprint(v)
				}
				buf.WriteString(fmt.Sprintf("      可选值: %s\n", strings.Join(vals, "/")))
			}
		}
	}
	buf.WriteString("\n")
}

func (r *Registry) writeToolFullDef(buf *bytes.Buffer, t Tool) {
	buf.WriteString(fmt.Sprintf("## 工具: %s\n", t.Name()))
	buf.WriteString(fmt.Sprintf("描述: %s\n\n", t.Description()))
	buf.WriteString("函数:\n")
	for _, fn := range t.Functions() {
		r.writeFunctionDef(buf, fn)
	}
}

func (r *Registry) writePromptFooter(buf *bytes.Buffer) {
	buf.WriteString("\n### 输出格式（必须严格遵守）\n\n")
	buf.WriteString("你的每次回复都必须是一个完整的 JSON 对象，即使一句简单的「好的」也必须包裹在 JSON 中。\n")
	buf.WriteString("**严禁以纯文本回复，严禁在 JSON 前后添加任何文字或 markdown 代码块，回复必须以 { 开头、以 } 结尾。**\n\n")
	buf.WriteString("格式如下：\n\n")
	buf.WriteString("{\n")
	buf.WriteString(`    "reply":"给用户的自然语言回复",` + "\n")
	buf.WriteString(`    "tool_calls": [` + "\n")
	buf.WriteString("        {\n")
	buf.WriteString(`            "tool": "<工具名>",` + "\n")
	buf.WriteString(`            "function": "<函数名>",` + "\n")
	buf.WriteString(`            "args": { /* 参数 */ }` + "\n")
	buf.WriteString("        }\n")
	buf.WriteString("    ]\n")
	buf.WriteString("}\n\n")
	buf.WriteString("规则：\n")
	buf.WriteString("- 不需要调用工具时，tool_calls 为空数组 []。\n")
	buf.WriteString("- 当本轮需要调用工具时（tool_calls 非空），reply 字段必须为空字符串 \"\"。只有不需要调用工具的最终回复才写 reply。例如：查账单时返回 {\"reply\":\"\",\"tool_calls\":[...]}，工具返回结果后再给出 {\"reply\":\"查到 12 笔...\",\"tool_calls\":[]}。\n")
	buf.WriteString("- 一条回复中 tool_calls 可以包含多个工具调用，按顺序执行。工具执行结果会追加到对话中，你可以据此继续回复用户。\n")
	buf.WriteString("- JSON 字符串值内的英文双引号必须用反斜杠转义（\\\"）。\n")
}

// GenerateImportantRules 生成输出规范文本（不含工具定义，放在角色定义之后、工具使用之前）
func (r *Registry) GenerateImportantRules() string {
	var buf bytes.Buffer
	buf.WriteString("【强制工具查询规则】除 tool_retriever 外，任何工具在调用前必须先通过 tool_retriever.search 查询其完整定义（函数签名和参数 schema）。禁止凭工具名称和概览描述猜测参数直接调用！例如：要用 bookkeeping 记账，必须先 search(\"bookkeeping\") 拿到 create_bill 的参数。不确定用什么工具时，用 tool_retriever.search 搜索关键词。\n")
	return buf.String()
}

// GeneratePrompt 生成工具定义文本，追加到 system prompt 中
func (r *Registry) GeneratePrompt() string {
	var buf bytes.Buffer
	buf.WriteString("你可以使用以下工具来完成用户的请求。\n")
	buf.WriteString("当需要执行操作时，在回复中输出 JSON 格式。\n\n")

	for _, name := range r.sortedVisibleToolNames() {
		t := r.tools[name]
		buf.WriteString(fmt.Sprintf("## 工具: %s\n", t.Name()))
		buf.WriteString(fmt.Sprintf("描述: %s\n\n", t.Description()))
		buf.WriteString("函数:\n")
		for _, fn := range t.Functions() {
			buf.WriteString(fmt.Sprintf("  - %s: %s\n", fn.Name, fn.Description))
			if props, ok := fn.Parameters["properties"].(map[string]interface{}); ok {
				required := make(map[string]bool)
				if reqs, ok := fn.Parameters["required"].([]interface{}); ok {
					for _, r := range reqs {
						if s, ok := r.(string); ok {
							required[s] = true
						}
					}
				}
				for key, val := range props {
					prop, ok := val.(map[string]interface{})
					if !ok {
						continue
					}
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

	buf.WriteString("### 输出格式（必须严格遵守）\n\n")
	buf.WriteString("每次回复都必须是一个 JSON 对象，格式如下：\n\n")
	buf.WriteString("{\n")
	buf.WriteString(`    "reply":"给用户的自然语言回复",` + "\n")
	buf.WriteString(`    "tool_calls": [` + "\n")
	buf.WriteString("        {\n")
	buf.WriteString(`            "tool": "<工具名>",` + "\n")
	buf.WriteString(`            "function": "<函数名>",` + "\n")
	buf.WriteString(`            "args": { /* 参数 */ }` + "\n")
	buf.WriteString("        }\n")
	buf.WriteString("    ]\n")
	buf.WriteString("}\n\n")
	buf.WriteString("不需要调用工具时，tool_calls 为空数组 []。**严禁在 JSON 前后添加任何文字、解释或 markdown 代码块。回复必须以 { 开头、以 } 结尾。**\n")
	buf.WriteString("一条回复中 tool_calls 可以包含多个工具调用，按顺序执行。\n")
	buf.WriteString("工具执行结果会追加到对话中，你可以据此继续回复用户。\n\n")

	return buf.String()
}

// ToolOverview 生成所有可用工具的名称+描述概览（不含详细参数），供 LLM 了解工具能力
func (r *Registry) ToolOverview() string {
	var buf bytes.Buffer
	for _, name := range r.sortedVisibleToolNames() {
		if name == "tool_retriever" {
			continue
		}
		t := r.tools[name]
		buf.WriteString(fmt.Sprintf("工具名: %s —— %s\n", name, t.Description()))
	}
	result := buf.String()
	if result == "" {
		return ""
	}
	return "\n\n## 可用工具概览\n" +
		"【强制规则】以上只是工具概览，不包含函数签名和参数定义。\n" +
		"如果用户的问题需要用到某个工具，你必须先用 tool_retriever.search 查询该工具的详细定义，然后才能调用它。\n" +
		"直接调用未查询过的工具会失败——例如调用 bookkeeping__create 前，必须先 search(\"bookkeeping\") 拿到 create 的参数 schema。\n" +
		"不确定工具名时也可以用 tool_retriever.search 搜索关键词。\n\n" +
		result +
		"\n再次强调：必须先 tool_retriever.search(工具名) 再调用工具，禁止跳过查询步骤！\n"
}

// GetLLMToolsFiltered 只导出指定名称的工具定义（供 Phase 5/6 按需加载）
func (r *Registry) GetLLMToolsFiltered(names []string) []llm.ToolDefinition {
	defs := make([]llm.ToolDefinition, 0, len(names))
	for _, name := range names {
		t := r.tools[name]
		if t == nil {
			continue
		}
		funcs := make([]llm.FuncDef, len(t.Functions()))
		for i, f := range t.Functions() {
			funcs[i] = llm.FuncDef{
				Name:        f.Name,
				Description: f.Description,
				Parameters:  f.Parameters,
			}
		}
		defs = append(defs, llm.ToolDefinition{
			Name:        t.Name(),
			Description: t.Description(),
			Functions:   funcs,
		})
	}
	return defs
}

// GeneratePromptFor 只为指定工具生成 prompt 文本
func (r *Registry) GeneratePromptFor(names []string) string {
	var buf bytes.Buffer
	buf.WriteString("你可以使用以下工具来完成用户的请求。\n")
	buf.WriteString("当需要执行操作时，在回复中输出 JSON 格式。\n\n")

	for _, name := range names {
		if r.hiddenNames[name] {
			continue
		}
		t := r.tools[name]
		if t == nil {
			continue
		}
		buf.WriteString(fmt.Sprintf("## 工具: %s\n", t.Name()))
		buf.WriteString(fmt.Sprintf("描述: %s\n\n", t.Description()))
		buf.WriteString("函数:\n")
		for _, fn := range t.Functions() {
			buf.WriteString(fmt.Sprintf("  - %s: %s\n", fn.Name, fn.Description))
			if props, ok := fn.Parameters["properties"].(map[string]interface{}); ok {
				required := make(map[string]bool)
				if reqs, ok := fn.Parameters["required"].([]interface{}); ok {
					for _, r := range reqs {
						if s, ok := r.(string); ok {
							required[s] = true
						}
					}
				}
				for key, val := range props {
					prop, ok := val.(map[string]interface{})
					if !ok {
						continue
					}
					typ, _ := prop["type"].(string)
					desc, _ := prop["description"].(string)
					req := ""
					if required[key] {
						req = " (必填)"
					}
					buf.WriteString(fmt.Sprintf("    - %s (%s%s): %s\n", key, typ, req, desc))
					if enumVals, ok := prop["enum"].([]interface{}); ok && len(enumVals) > 0 {
						vals := make([]string, len(enumVals))
						for i, v := range enumVals {
							vals[i] = fmt.Sprint(v)
						}
						buf.WriteString(fmt.Sprintf("      可选值: %s\n", strings.Join(vals, "/")))
					}
				}
			}
			buf.WriteString("\n")
		}
	}

	buf.WriteString("### 输出格式（必须严格遵守）\n\n")
	buf.WriteString("每次回复都必须是一个 JSON 对象，格式如下：\n\n")
	buf.WriteString("{\n")
	buf.WriteString(`    "reply":"给用户的自然语言回复",` + "\n")
	buf.WriteString(`    "tool_calls": [` + "\n")
	buf.WriteString("        {\n")
	buf.WriteString(`            "tool": "<工具名>",` + "\n")
	buf.WriteString(`            "function": "<函数名>",` + "\n")
	buf.WriteString(`            "args": { /* 参数 */ }` + "\n")
	buf.WriteString("        }\n")
	buf.WriteString("    ]\n")
	buf.WriteString("}\n\n")
	buf.WriteString("不需要调用工具时，tool_calls 为空数组 []。**严禁在 JSON 前后添加任何文字、解释或 markdown 代码块。回复必须以 { 开头、以 } 结尾。**\n")
	buf.WriteString("一条回复中 tool_calls 可以包含多个工具调用，按顺序执行。\n")
	buf.WriteString("工具执行结果会追加到对话中，你可以据此继续回复用户。\n\n")

	return buf.String()
}

// ToolCall — 解析出的工具调用
type ToolCall struct {
	Tool      string            `json:"tool"`
	Function  string            `json:"function"`
	Args      json.RawMessage   `json:"args"`
	Display   string            `json:"display,omitempty"`    // 前端展示用中文名
	ArgLabels map[string]string `json:"arg_labels,omitempty"` // 参数 key → 中文标签
}

// FillDisplay 从 Registry 查询工具的函数定义，填充 Display 和 ArgLabels。
func (r *Registry) FillDisplay(tc *ToolCall) {
	t := r.tools[tc.Tool]
	if t == nil {
		return
	}
	for _, fn := range t.Functions() {
		if fn.Name == tc.Function {
			if fn.Display != "" {
				tc.Display = fn.Display
			} else {
				tc.Display = fn.Description // fallback: 函数描述（可能较长）
			}
			if props, ok := fn.Parameters["properties"].(map[string]interface{}); ok {
				tc.ArgLabels = make(map[string]string, len(props))
				for key, val := range props {
					if prop, ok := val.(map[string]interface{}); ok {
						if desc, ok := prop["description"].(string); ok {
							tc.ArgLabels[key] = desc
						}
					}
				}
			}
			return
		}
	}
}

// String 返回 "tool.function" 格式的名称
func (tc ToolCall) String() string {
	return tc.Tool + "." + tc.Function
}

// ToolCallNames 批量格式化
func ToolCallNames(tcs []ToolCall) string {
	names := make([]string, len(tcs))
	for i, tc := range tcs {
		names[i] = tc.String()
	}
	return strings.Join(names, ", ")
}

// ExecuteByName 按名称查找工具并执行指定函数
func (r *Registry) ExecuteByName(ec *ExecuteContext, toolName, funcName string, args json.RawMessage) *Result {
	t := r.tools[toolName]
	if t == nil {
		// 模糊纠错：用失败的工具名搜索相似工具，引导 LLM 查询正确工具
		candidates := r.SearchTools(toolName)
		if len(candidates) > 0 {
			names := make([]string, len(candidates))
			for i, c := range candidates {
				names[i] = fmt.Sprintf("%s（%s）", c.Name, c.Description)
			}
			return &Result{Success: false, Error: fmt.Sprintf(
				"未找到工具 '%s'。你是否想使用以下工具？%s。请先调用 tool_retriever.search 查询其详细定义，不要凭猜测直接调用。",
				toolName, strings.Join(names, "；"))}
		}
		return &Result{Success: false, Error: fmt.Sprintf(
			"未找到工具 '%s'。请先调用 tool_retriever.search 用关键词搜索可用工具，例如 tool_retriever.search(\"%s\")。禁止在未查询工具定义的情况下凭猜测调用。",
			toolName, toolName)}
	}

	// —— Plan 模式工具全部禁用（集中式，所有工具一视同仁）——
	// plan 模式为纯讨论状态：不区分只读/写，任何工具调用一律拦截（2026-08-14 激进收紧）。
	// 工具仍保留 ReadOnlyFuncs 声明，仅为未来可能的"只读浏览模式"预留，当前拦截不再使用。
	if ec.Mode == model.ConvModePlan {
		return &Result{Success: false, Error: fmt.Sprintf(
			"当前处于计划模式（只读讨论状态），本模式下已禁用全部工具调用，禁止执行 %s.%s。请仅进行方案讨论；如需执行操作，请让用户切换到工作模式。",
			toolName, funcName)}
	}

	return t.Execute(ec, funcName, args)
}
