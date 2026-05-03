package tool

import (
	"context"
	"encoding/json"
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
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

// ExecuteContext — 工具执行的上下文（由 Orchestrator 注入）
type ExecuteContext struct {
	Ctx    context.Context
	UserID string
}

// Tool — 所有工具必须实现的接口
type Tool interface {
	Name() string
	Description() string
	Functions() []FuncSchema
	Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result
}
