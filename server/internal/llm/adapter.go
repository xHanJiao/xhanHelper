package llm

import "context"

// LLMClient — 所有 LLM 厂商必须实现的接口
type LLMClient interface {
	Chat(ctx context.Context, req *ChatRequest) (<-chan *StreamEvent, error)
	ChatSync(ctx context.Context, req *ChatRequest) (string, error)
	// BuildSystemPrompt 根据厂商特性，为 base prompt 追加工具指令
	BuildSystemPrompt(base string, tools []ToolDefinition) string
}

// ChatRequest — LLM 对话请求
type ChatRequest struct {
	SystemPrompt string           // 系统提示词
	Messages     []Message        // 对话历史
	Tools        []ToolDefinition // 工具定义
}

// Message — 一条对话消息
type Message struct {
	Role    string // system | user | assistant | tool
	Content string
}

// FuncDef — 单个函数定义
type FuncDef struct {
	Name        string
	Description string
	Parameters  map[string]interface{} // JSON Schema
}

// ToolDefinition — 工具定义（供 function calling）
type ToolDefinition struct {
	Name        string
	Description string
	Parameters  map[string]interface{} // JSON Schema
	Functions   []FuncDef              // 该工具下的所有函数
}

// StreamEvent — 流式响应事件
type StreamEvent struct {
	Type    string // text_delta | done | error
	Content string // 增量文本（Type=text_delta 时）
	Error   string // 错误信息（Type=error 时）
}
