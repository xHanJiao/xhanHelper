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
	Thinking     *bool            // 思考模式：nil=不传(默认)，true=开启，false=关闭
	Effort       string           // reasoning_effort: high / max
	Temperature  *float64         // 采样温度 0~2，nil 用 LLM 默认
	MaxTokens    *int             // 最大输出 token，nil 用 LLM 默认
	UserID       string           // 传给 LLM 的 user_id 字段（不做公开选项）
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
	Type         string     // text_delta | thought_delta | tool_call | tool_result | plan | done | error
	Content      string     // 增量文本
	Error        string     // 错误信息（Type=error 时）
	ToolCalls    []ToolCall `json:"-"` // 内部：流式汇总的 tool calls（仅 done 事件携带）
	Usage        *Usage     `json:"-"` // 内部：token 用量
	RequestJSON  *string    `json:"-"` // 内部：LLM 请求 JSON（仅 done 事件携带）
	ResponseJSON *string    `json:"-"` // 内部：LLM 响应 JSON（仅 done 事件携带）
}

// Usage — token 用量信息
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}
