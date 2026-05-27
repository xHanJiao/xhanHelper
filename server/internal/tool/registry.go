package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
)

// Registry — 工具注册表
type Registry struct {
	tools         map[string]Tool
	hiddenNames   map[string]bool
	dynamicLoader DynamicToolLoader
	sidecarURL    string
	cacheReader   CacheReader
	cacheWriter   CacheWriter
	lastLoadTime  time.Time
}

// NewRegistry 创建空的注册表
func NewRegistry() *Registry {
	return &Registry{
		tools:       make(map[string]Tool),
		hiddenNames: make(map[string]bool),
	}
}

// HideTool 将工具从 prompt 中隐藏（不会告诉 LLM，但 ExecuteByName 仍可用）
func (r *Registry) HideTool(name string) {
	r.hiddenNames[name] = true
}

// SetDynamicLoader 设置动态工具加载器（由 main.go 调用）
func (r *Registry) SetDynamicLoader(loader DynamicToolLoader, sidecarURL string, cacheReader CacheReader, cacheWriter CacheWriter) {
	r.dynamicLoader = loader
	r.sidecarURL = sidecarURL
	r.cacheReader = cacheReader
	r.cacheWriter = cacheWriter
}

// SearchTools 在内存中的工具（含 MCP 占位工具）里关键词搜索
func (r *Registry) SearchTools(query string) []ToolInfo {
	var results []ToolInfo
	q := strings.ToLower(query)
	for name, t := range r.tools {
		if name == "tool_retriever" {
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

var reloadMu sync.Mutex

// maybeReloadDynamic 30s TTL，过期自动从 DB 刷新动态工具
func (r *Registry) maybeReloadDynamic() {
	if r.dynamicLoader == nil {
		return
	}
	if time.Since(r.lastLoadTime) < 30*time.Second {
		return
	}
	reloadMu.Lock()
	defer reloadMu.Unlock()
	// 双重检查
	if time.Since(r.lastLoadTime) < 30*time.Second {
		return
	}
	r.loadFromDB()
}

func (r *Registry) loadFromDB() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	records, err := r.dynamicLoader.ListActiveTools(ctx)
	if err != nil {
		log.Printf("[tool] reload dynamic tools error: %v", err)
		return // 失败时保留原有工具
	}
	// 替换旧的动态工具，保留硬编码工具
	for name := range r.tools {
		if _, ok := r.tools[name].(*DynamicTool); ok {
			delete(r.tools, name)
		}
	}
	for _, rec := range records {
		dt := NewDynamicTool(ToolRegistryRecord{
			Name:        rec.Name,
			Description: rec.Description,
			SchemaJSON:  rec.SchemaJSON,
		}, r.sidecarURL)
		if r.cacheReader != nil {
			dt.SetCache(r.cacheReader, r.cacheWriter)
		}
		r.Register(dt)
		log.Printf("[tool] dynamic tool loaded: %s", rec.Name)
	}
	r.lastLoadTime = time.Now()
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

// GetLLMTools 导出所有工具定义（供 LLM native function calling）
func (r *Registry) GetLLMTools() []llm.ToolDefinition {
	defs := make([]llm.ToolDefinition, 0, len(r.tools))
	for _, t := range r.tools {
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

// GeneratePromptWithOverview 混排模式：reminder + note + tool_retriever 完整定义，其他工具仅概览
func (r *Registry) GeneratePromptWithOverview() string {
	var buf bytes.Buffer
	buf.WriteString("\n\n## 工具使用\n\n")
	buf.WriteString("你可以使用以下工具来完成用户的请求。\n")
	buf.WriteString("当需要执行操作时，在回复中输出 JSON 格式。\n\n")

	// 常驻工具（完整定义，可直接调用）
	alwaysVisible := []string{"tool_retriever"}
	for _, name := range alwaysVisible {
		t := r.tools[name]
		if t == nil {
			continue
		}
		r.writeToolFullDef(&buf, t)
	}

	// 其他工具概览（需通过 tool_retriever.search 查询详细定义后才能调用）
	var overview []string
	for name, t := range r.tools {
		if name == "tool_retriever" {
			continue
		}
		if r.hiddenNames[name] {
			continue
		}
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

func (r *Registry) writeToolFullDef(buf *bytes.Buffer, t Tool) {
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

func (r *Registry) writePromptFooter(buf *bytes.Buffer) {
	buf.WriteString("\n### 输出格式（必须严格遵守）\n\n")
	buf.WriteString("你的每次回复都必须是一个 JSON 对象，格式如下：\n\n")
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
	buf.WriteString("不需要调用工具时，tool_calls 为空数组 []。\n")
	buf.WriteString("一条回复中 tool_calls 可以包含多个工具调用，按顺序执行。\n")
	buf.WriteString("工具执行结果会追加到对话中，你可以据此继续回复用户。\n")
	buf.WriteString("**严禁**在 JSON 前后添加任何文字、解释或 markdown 代码块。你的回复必须以 { 开头、以 } 结尾。\n")
}

// GenerateImportantRules 生成重要规则文本（不含工具定义，可放在工具使用之前）
func (r *Registry) GenerateImportantRules() string {
	var buf bytes.Buffer
	buf.WriteString("\n## 重要规则\n\n")
	buf.WriteString("1. 每次回复都必须输出完整 JSON 对象，即使是一句简单的好的也必须包裹在 JSON 中，例如 {\"reply\":\"好的\",\"tool_calls\":[]}。不需要工具时 tool_calls 为空数组 []。JSON 字符串值内的英文双引号必须用反斜杠转义（\\\"）。**严禁以纯文本回复。严禁在 JSON 前后添加任何文字。回复必须以 { 开头、以 } 结尾。**\n")
	buf.WriteString("2. 【强制工具查询规则】除 tool_retriever 外，任何工具在调用前必须先通过 tool_retriever.search 查询其完整定义（函数签名和参数 schema）。禁止凭工具名称和概览描述猜测参数直接调用！例如：要用 bookkeeping 记账，必须先 search(\"bookkeeping\") 拿到 create_bill 的参数。不确定用什么工具时，用 tool_retriever.search 搜索关键词。\n")
	buf.WriteString("3. 只有用户明确或根据上下文99%确定时，才调用工具。\n")
	buf.WriteString("4. 历史消息中的 [MM-DD HH:MM] 前缀是系统时间戳，不是对话内容，严禁模仿此格式输出时间戳。\n")
	buf.WriteString("5. 当本轮需要调用工具时（tool_calls 非空），reply 字段必须为空字符串 \"\"；只有不需要调用工具的最终回复才写 reply。例如：查账单时返回 {\"reply\":\"\",\"tool_calls\":[...]}，工具返回结果后再给出 {\"reply\":\"查到 12 笔...\",\"tool_calls\":[]}。\n")
	return buf.String()
}

// GeneratePrompt 生成工具定义文本，追加到 system prompt 中
func (r *Registry) GeneratePrompt() string {
	var buf bytes.Buffer
	buf.WriteString("\n\n## 工具使用\n\n")
	buf.WriteString("你可以使用以下工具来完成用户的请求。\n")
	buf.WriteString("当需要执行操作时，在回复中输出 JSON 格式。\n\n")

	for _, t := range r.tools {
		if r.hiddenNames[t.Name()] {
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

	buf.WriteString("## 重要规则\n\n")
	buf.WriteString("1. 只能调用上面【工具使用】部分列出的函数。\n")
	buf.WriteString("2. 只有用户明确或根据上下文99%确定时，才调用工具。\n")
	buf.WriteString("3. 历史消息中的 [MM-DD HH:MM] 前缀是系统时间戳，不是对话内容，严禁模仿此格式输出时间戳。\n")

	return buf.String()
}

// ToolOverview 生成所有可用工具的名称+描述概览（不含详细参数），供 LLM 了解工具能力
func (r *Registry) ToolOverview() string {
	var buf bytes.Buffer
	for _, t := range r.tools {
		if t.Name() == "tool_retriever" {
			continue
		}
		buf.WriteString(fmt.Sprintf("工具名: %s —— %s\n", t.Name(), t.Description()))
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
	buf.WriteString("\n\n## 工具使用\n\n")
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

	buf.WriteString("## 重要规则\n\n")
	buf.WriteString("1. 只能调用上面【工具使用】部分列出的函数。\n")
	buf.WriteString("2. 只有用户明确或根据上下文99%确定时，才调用工具。\n")
	buf.WriteString("3. 历史消息中的 [MM-DD HH:MM] 前缀是系统时间戳，不是对话内容，严禁模仿此格式输出时间戳。\n")

	return buf.String()
}

// ToolCall — 解析出的工具调用
type ToolCall struct {
	Tool     string          `json:"tool"`
	Function string          `json:"function"`
	Args     json.RawMessage `json:"args"`
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
	return t.Execute(ec, funcName, args)
}
