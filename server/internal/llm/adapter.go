package llm

import "context"

// LLMClient — 所有 LLM 厂商必须实现的接口
type LLMClient interface {
	// Chat 流式对话，返回事件 channel。适合前端 SSE 推送
	Chat(ctx context.Context, req *ChatRequest) (<-chan *StreamEvent, error)

	// ChatSync 非流式对话，等待完整回复。适合后台任务
	ChatSync(ctx context.Context, req *ChatRequest) (string, error)
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

// ToolDefinition — 工具定义（供 function calling）
type ToolDefinition struct {
	Name        string
	Description string
	Parameters  map[string]interface{} // JSON Schema
}

// StreamEvent — 流式响应事件
type StreamEvent struct {
	Type    string // text_delta | done | error
	Content string // 增量文本（Type=text_delta 时）
	Error   string // 错误信息（Type=error 时）
}
