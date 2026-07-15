package tool

import (
	"context"
	"encoding/json"

	"xhan-helper/internal/llm"
)

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

	// Events — SSE 事件通道，工具可以通过此通道向客户端推送自定义事件。
	// 重要：必须在工具 Execute() 方法返回前发送，不可在异步 goroutine 中使用 channel。
	// channel 在 ReAct 循环结束后关闭，延迟发送会 Panic。replay/测试场景下为 nil。
	Events chan<- *llm.StreamEvent
}

// Tool — 所有工具必须实现的接口
type Tool interface {
	Name() string
	Description() string
	Functions() []FuncSchema
	Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result
}
