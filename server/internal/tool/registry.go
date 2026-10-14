package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"xhan-helper/internal/activity"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
)

// Registry — 工具注册表
type Registry struct {
	tools       map[string]Tool
	hiddenNames map[string]bool
	activity    *activity.Store // 活动埋点 sink；nil = 不采集（tool.call.* 不发）
}

// NewRegistry 创建空的注册表
func NewRegistry() *Registry {
	return &Registry{
		tools:       make(map[string]Tool),
		hiddenNames: make(map[string]bool),
	}
}

// SetActivityStore 注入活动埋点 sink；由启动装配调用一次。nil = 关闭采集。
func (r *Registry) SetActivityStore(s *activity.Store) { r.activity = s }

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

// IsHidden 判断工具是否被隐藏（expose_tools 校验用）
func (r *Registry) IsHidden(name string) bool {
	return r.hiddenNames[name]
}

// VisibleToolNames 返回所有非隐藏工具名（按字典序排序，字节稳定）。
func (r *Registry) VisibleToolNames() []string {
	return r.sortedVisibleToolNames()
}

// ToolFunctionInfo — 工具函数信息（SearchTools 返回结构）。
type ToolFunctionInfo struct {
	Name        string
	Description string
	Parameters  map[string]interface{}
}

// ToolInfo — 工具信息（SearchTools 返回结构）。
type ToolInfo struct {
	ID          string
	Name        string
	Description string
	Functions   []ToolFunctionInfo
}

// SearchTools 在内存中的工具（含 MCP 占位工具）里关键词搜索。
// hidden 工具不参与搜索（主循环 LLM 无途径获知）。
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

// ReadOnlyToolDefinitions 导出只读函数的工具定义（供 plan 模式 native FC 请求体）。
// 仅保留 ReadOnlyFuncs() 声明的函数；无只读函数的工具（含 task/expose_tools）整体跳过。
// 输出按工具名排序，保证字节稳定（前缀缓存友好）。
func (r *Registry) ReadOnlyToolDefinitions() []llm.ToolDefinition {
	defs := make([]llm.ToolDefinition, 0)
	for _, name := range r.sortedVisibleToolNames() {
		t := r.tools[name]
		ro := t.ReadOnlyFuncs()
		if len(ro) == 0 {
			continue
		}
		funcs := make([]llm.FuncDef, 0, len(ro))
		for _, f := range t.Functions() {
			if ro[f.Name] {
				funcs = append(funcs, llm.FuncDef{
					Name:        f.Name,
					Description: f.Description,
					Parameters:  f.Parameters,
				})
			}
		}
		if len(funcs) == 0 {
			continue
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

// GeneratePrompt 生成工具定义文本（prompt engineering 格式，仅供反思链路 ReactLoop 使用）。
func (r *Registry) GeneratePrompt() string {
	return r.generatePrompt()
}

// generatePrompt 渲染全部可见工具的函数定义文本。
func (r *Registry) generatePrompt() string {
	var buf bytes.Buffer
	buf.WriteString("你可以使用以下工具来完成用户的请求。\n")
	buf.WriteString("当需要执行操作时，在回复中输出 JSON 格式。\n\n")

	for _, name := range r.sortedVisibleToolNames() {
		t := r.tools[name]
		funcs := t.Functions()
		if len(funcs) == 0 {
			continue
		}
		buf.WriteString(fmt.Sprintf("## 工具: %s\n", t.Name()))
		buf.WriteString(fmt.Sprintf("描述: %s\n\n", t.Description()))
		buf.WriteString("函数:\n")
		for _, fn := range funcs {
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
					// 显示枚举值列表
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

// ToolOverview 生成所有可用工具的名称+描述概览（不含详细参数），供 LLM 了解工具能力
func (r *Registry) ToolOverview() string {
	var buf bytes.Buffer
	for _, name := range r.sortedVisibleToolNames() {
		t := r.tools[name]
		buf.WriteString(fmt.Sprintf("工具名: %s —— %s\n", name, t.Description()))
	}
	result := buf.String()
	if result == "" {
		return ""
	}
	return "\n\n## 可用工具概览\n" + result
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
	ID        string            `json:"id,omitempty"` // LLM 提供的调用 id（活动埋点业务键）；旧文本路径为空
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
				"未找到工具 '%s'。你是否想使用以下工具？%s。请核对工具名后重试。",
				toolName, strings.Join(names, "；"))}
		}
		return &Result{Success: false, Error: fmt.Sprintf(
			"未找到工具 '%s'。请核对工具名后重试。", toolName)}
	}

	// —— 计划模式：只读放行，写操作拦截 ——
	// plan 模式仅允许 ReadOnlyFuncs() 声明的只读函数执行（检索/查询），其余一律拦截。
	// ReadOnlyFuncs() 返回 nil/空 map 的工具（含 task、expose_tools、theme、mailbox）全拦。
	if ec.Mode == model.ConvModePlan && !t.ReadOnlyFuncs()[funcName] {
		return &Result{Success: false, Error: fmt.Sprintf(
			"当前处于计划模式（只读讨论状态），本模式仅允许只读操作，禁止执行写操作 %s.%s。如需执行写操作，请让用户切换到工作模式。",
			toolName, funcName)}
	}

	// 活动埋点：服务端工具调度器统一收口（设计篇 6.3.3 / 附表 A `tool.call.*`）。
	// 采集关闭时零开销透传。defer 保证 panic 时也补上 ended。
	if r.activity == nil {
		return t.Execute(ec, funcName, args)
	}
	startID, endID := r.emitToolCallStarted(ec, toolName, funcName, args)
	// 让工具内的仓储写入把变更事件挂在这次 tool.call.started 下（P6 doc.*/memory.*）。
	ec.Ctx = activity.WithToolCall(ec.Ctx, startID)
	var res *Result
	defer func() { r.emitToolCallEnded(ec, toolName, funcName, startID, endID, res) }()
	res = t.Execute(ec, funcName, args)
	return res
}
