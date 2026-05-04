package llm

import "context"

// LLMClient — 所有 LLM 厂商必须实现的接口
type LLMClient interface {
	Chat(ctx context.Context, req *ChatRequest) (<-chan *StreamEvent, error)
	ChatSync(ctx context.Context, req *ChatRequest) (Message, error)
	BuildSystemPrompt(base string, tools []ToolDefinition) string
	ModelName() string
}

// ChatRequest — LLM 对话请求
type ChatRequest struct {
	SystemPrompt string           // 系统提示词
	Messages     []Message        // 对话历史
	Tools        []ToolDefinition // 工具定义
	Thinking     bool             // 是否启用思考模式
	Effort       string           // reasoning_effort: high / max
}

// Message — 一条对话消息
type Message struct {
	Role             string     // system | user | assistant | tool
	Content          string     // 消息正文
	ReasoningContent string     // 思维链内容（DeepSeek 思考模式）
	ToolCalls        []ToolCall // 原生 tool_calls
	ToolCallID       string     // tool 消息的调用 ID
	RequestJSON      *string    // 发给 LLM 的请求 JSON（调试用）
	ResponseJSON     *string    // LLM 返回的响应 JSON（调试用）
}

// ToolCall — 原生 function calling 的工具调用
type ToolCall struct {
	ID       string
	Function ToolCallFunction
}

// ToolCallFunction — 工具调用中的函数引用
type ToolCallFunction struct {
	Name      string
	Arguments string // JSON string
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
	Type    string // text_delta | thought_delta | tool_call | tool_result | plan | done | error
	Content string // 增量文本
	Error   string // 错误信息（Type=error 时）
}
