package agent

import (
	"encoding/json"
	"log"

	"xhan-helper/internal/agent/promptbuilder"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
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

	// WindowSize — 扩张窗口的当前轮数。
	// 每条 user 消息记录当时的窗口大小，供下一条消息读取+递增。
	// 0 表示未设置（默认使用配置的 history_rounds）。
	WindowSize int `json:"window_size,omitempty"`

	// SessionStartMsgID — 当前 session 的第一条 user 消息 ID。
	// 同一 session 内所有消息共享此 ID，session 结束时用于确定摘要范围。
	SessionStartMsgID string `json:"session_start_msg_id,omitempty"`
}

// mergeMsgExtra 从消息中读取已有 Extra，调用 fn 修改后写回。
// 若消息原有 Extra 为 nil 或解析失败，fn 收到空 MsgExtra。
func mergeMsgExtra(msg *model.Message, fn func(extra *MsgExtra)) {
	var extra MsgExtra
	if msg.Extra != nil {
		if err := json.Unmarshal([]byte(*msg.Extra), &extra); err != nil {
			log.Printf("[msg_extra] unmarshal error for msg %s: %v", msg.ID[:8], err)
		}
	}
	fn(&extra)
	b, err := json.Marshal(extra)
	if err != nil {
		log.Printf("[msg_extra] marshal error for msg %s: %v", msg.ID[:8], err)
		return
	}
	s := string(b)
	msg.Extra = &s
}

// SetWindowSize 设置消息的扩张窗口轮数。自动与已有 Extra 合并。
func SetWindowSize(msg *model.Message, size int) {
	mergeMsgExtra(msg, func(extra *MsgExtra) {
		extra.WindowSize = size
	})
}

// SetUsage 设置消息的 LLM token 用量。自动与已有 Extra 合并。
func SetUsage(msg *model.Message, usage *llm.Usage) {
	mergeMsgExtra(msg, func(extra *MsgExtra) {
		extra.Usage = usage
	})
}

// SetSessionStart 设置消息的 session 起始 ID。自动与已有 Extra 合并。
func SetSessionStart(msg *model.Message, startMsgID string) {
	mergeMsgExtra(msg, func(extra *MsgExtra) {
		extra.SessionStartMsgID = startMsgID
	})
}

// GetSessionStart 从消息 Extra 中读取 SessionStartMsgID，未设置返回空字符串。
func GetSessionStart(msg *model.Message) string {
	if msg.Extra == nil {
		return ""
	}
	var extra MsgExtra
	if err := json.Unmarshal([]byte(*msg.Extra), &extra); err != nil {
		return ""
	}
	return extra.SessionStartMsgID
}

// GetWindowSize 从消息 Extra 中读取 WindowSize，未设置返回 0。
func GetWindowSize(msg *model.Message) int {
	if msg.Extra == nil {
		return 0
	}
	var extra MsgExtra
	if err := json.Unmarshal([]byte(*msg.Extra), &extra); err != nil {
		return 0
	}
	return extra.WindowSize
}
