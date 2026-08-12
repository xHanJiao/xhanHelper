package llmcost

import "time"

// UsageRecord 代表一次 LLM 调用的完整记录。
// 字段含义见设计文档 llm-cost-center.md。
type UsageRecord struct {
	Date             time.Time
	UserID           string
	Category         string
	SubCategory      string
	Model            string
	ConvID           string
	RefType          string
	RefID            string
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	Meta             map[string]interface{}
}

// Recorder 记录器接口。
// 实现者负责将 UsageRecord 持久化（DB / 日志 / 消息队列等）。
// Record 方法不应 panic，不应阻塞过长时间（建议异步）。
type Recorder interface {
	Record(record UsageRecord)
}
