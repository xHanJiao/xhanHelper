package tool

import (
	"context"

	"xhan-helper/internal/llm"
)

// ClientToolInfo — 客户端工具信息的最小化结构（tool 包不能 import agent 包，故独立定义）
type ClientToolInfo struct {
	ServerName  string                 `json:"server_name"`
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
	IsReadOnly  bool                   `json:"is_read_only"`
}

// SubAgentRunOptions — task 工具向 runner 传递的参数
type SubAgentRunOptions struct {
	SubAgentType string                  // 子 agent 类型："web_explore" / "file_explore"
	Description  string                  // 3-5 词任务描述
	Prompt       string                  // 任务全文
	ParentConvID string                  // 主会话 ID
	ClientTools  []ClientToolInfo        // 透传客户端工具定义（子任务依赖 macOS 客户端）
	MainEvents   chan<- *llm.StreamEvent // 主 SSE events 通道（子循环需转发 client_tool_call 给客户端）
}

// SubAgentRunResult — runner 返回的结果
type SubAgentRunResult struct {
	Success bool
	Error   string
	Text    string // 子 agent 最终回复（未包裹 <task> 标签，由 task 工具包裹）。
	// 超时/失败时 Text 为工具日志摘要（部分成果），task 工具以 state=timeout/failed 包裹回传主 LLM
	ConvID string // 子会话 ID
	State  string // 执行状态：completed | timeout | failed。空默认 completed
}

// SubAgentRunner — 子 agent 执行器接口
// 由 agent 包 Orchestrator 实现，解决 tool → agent 循环依赖。
type SubAgentRunner interface {
	RunSubAgent(ctx context.Context, opts *SubAgentRunOptions) *SubAgentRunResult
}
