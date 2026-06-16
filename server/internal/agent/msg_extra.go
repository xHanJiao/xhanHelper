package agent

import (
	"xhan-helper/internal/agent/promptbuilder"
	"xhan-helper/internal/llm"
)

// MsgExtra — message.extra 字段的序列化结构。
// 在保存 assistant 消息前，由 orchestrator 从不同来源合并构造后 JSON 编码写入 DB。
type MsgExtra struct {
	// Providers — prompt 构建追溯。
	// 来源：promptbuilder.PromptTrace.Providers。
	// 每个 ProviderTrace 记录一个 provider（static 或 search）的构建详情，
	// 供前端 AdminView / MessageDebugView 展示 prompt 构建过程。
	Providers []promptbuilder.ProviderTrace `json:"providers,omitempty"`

	// UserMessagePrefix — 检索内容的 XML 前缀，拼接到当前轮 user message 前面。
	// 来源：promptbuilder.BuildResult.UserMessagePrefix。
	// 供前端 MessageDebugView 展示完整的 user 消息体（含检索上下文）。
	UserMessagePrefix string `json:"user_message_prefix,omitempty"`

	// Usage — LLM token 用量及缓存命中信息。
	// 来源：llm.StreamEvent.Usage（DeepSeek SSE done 事件中解析）。
	// prompt_cache_hit_tokens / prompt_cache_miss_tokens 反映 DeepSeek 上下文硬盘缓存命中情况。
	Usage *llm.Usage `json:"usage,omitempty"`
}
