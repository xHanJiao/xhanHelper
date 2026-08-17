package tool

import (
	"context"
	"encoding/json"

	"xhan-helper/internal/llm"
)

// AllReadOnly 返回工具全部函数的只读声明（供全只读工具实现 ReadOnlyFuncs 用）。
// 基于 Functions() 自动生成，保证与函数定义一致、不会拼写错误。
func AllReadOnly(t Tool) map[string]bool {
	m := make(map[string]bool)
	for _, f := range t.Functions() {
		m[f.Name] = true
	}
	return m
}

// Result — 工具函数执行结果
type Result struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data,omitempty"`
	Error   string          `json:"error,omitempty"`
}

// FuncSchema — 一个工具函数的定义，包含 JSON Schema 参数
type FuncSchema struct {
	Name        string                 `json:"name"`
	Display     string                 `json:"display,omitempty"` // 前端展示用简短中文名，空则用 Name
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
	Cacheable   bool                   `json:"cacheable,omitempty"`
}

// ExecuteContext — 工具执行的上下文（由 Orchestrator 注入）
type ExecuteContext struct {
	Ctx    context.Context
	UserID string
	ConvID string // 当前会话 ID，绑定/解绑 agent 用
	Mode   string // 会话模式：model.ConvModeWork / model.ConvModePlan（plan 模式拦截写函数）

	// Events — SSE 事件通道，工具可以通过此通道向客户端推送自定义事件。
	// 重要：必须在工具 Execute() 方法返回前发送，不可在异步 goroutine 中使用 channel。
	// channel 在 ReAct 循环结束后关闭，延迟发送会 Panic。replay/测试场景下为 nil。
	Events chan<- *llm.StreamEvent

	// ClientTools — 客户端上报的本地工具定义（task 工具用此字段传给子循环）。
	// 普通工具无需关注，仅 task 工具使用。replay/测试场景下为 nil 或空。
	ClientTools []ClientToolInfo

	// Expose — 渐进式工具暴露回调（仅 expose_tools 工具使用）。
	// Orchestrator 注入，把指定工具组加入本会话窗口的暴露集。nil 表示当前上下文不支持暴露。
	// 可选依赖（回调），nil 时 expose_tools 返回不可用错误。
	Expose func(groups []string) *ExposeResult
}

// Tool — 所有工具必须实现的接口
type Tool interface {
	Name() string
	Description() string
	Functions() []FuncSchema
	Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result

	// ReadOnlyFuncs 返回该工具的只读函数名集合（plan 模式下仅这些函数可执行）。
	// 返回 nil 或空 map = 全部函数可写（plan 模式下全部拦截）。
	// 注意：函数名必须与 Functions() 中的定义一致，Registry 启动时会校验，拼错会 panic。
	ReadOnlyFuncs() map[string]bool
}

// CatalogEntry — 工具组在渐进式工具目录（ToolCatalog）中的展示信息。
// 由工具自身提供，替代原 catalog.go 中的独立 map，新增工具不实现即启动 panic，杜绝"LLM 永远看不到"。
type CatalogEntry struct {
	ShortDesc string   // 组级 caveman 描述
	Funcs     []string // 目录中透出的高频函数名（须与 Functions() 一致，ValidateCatalog 校验）
	Others    string   // 未列出函数的概略描述，如 "删除、统计、导出、导入 等"
}

// CatalogProvider — 可选接口：工具实现后，其目录条目（描述 + 高频函数过滤）由自身提供。
// 不实现则回退到 Description() + 全部函数名（兼容 mock/未适配工具）。
type CatalogProvider interface {
	Catalog() (CatalogEntry, bool)
}
