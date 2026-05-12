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
	dynamicLoader DynamicToolLoader
	sidecarURL    string
	cacheReader   CacheReader
	cacheWriter   CacheWriter
	lastLoadTime  time.Time
}

// NewRegistry 创建空的注册表
func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool)}
}

// SetDynamicLoader 设置动态工具加载器（由 main.go 调用）
func (r *Registry) SetDynamicLoader(loader DynamicToolLoader, sidecarURL string, cacheReader CacheReader, cacheWriter CacheWriter) {
	r.dynamicLoader = loader
	r.sidecarURL = sidecarURL
	r.cacheReader = cacheReader
	r.cacheWriter = cacheWriter
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
	buf.WriteString("2. 只有用户明确或根据上下文99%确定时，才调用工具。\n")

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
	return "\n\n===== 可用工具概览 =====\n" + result +
		"\n重要：以上工具在未获取详细函数定义时不能直接使用！你必须先调用 tool_retriever.search(工具名) 加载其详细函数定义。\n"
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
	buf.WriteString("\n\n===== 工具使用 =====\n\n")
	buf.WriteString("你可以使用以下工具来完成用户的请求。\n")
	buf.WriteString("当需要执行操作时，在回复中输出 JSON 格式。\n\n")

	for _, name := range names {
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
	buf.WriteString("2. 只有用户明确或根据上下文99%确定时，才调用工具。\n")

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
		return &Result{Success: false, Error: fmt.Sprintf("unknown tool: %s", toolName)}
	}
	return t.Execute(ec, funcName, args)
}
