package model

import "time"

// Message — 对话消息
type Message struct {
	ID               string    `gorm:"primaryKey;size:36" json:"id"`
	ConversationID   string    `gorm:"index:idx_conv_created,priority:1;size:36;not null" json:"conversation_id"`
	Role             string    `gorm:"size:20;not null" json:"role"` // user | assistant | tool
	Content          string    `gorm:"type:text" json:"content"`
	ReasoningContent *string   `gorm:"type:text;column:reasoning_content" json:"reasoning_content,omitempty"`
	ToolCallsJSON    *string   `gorm:"type:json;column:tool_calls_json" json:"tool_calls_json,omitempty"`
	ToolCallID       *string   `gorm:"size:36;column:tool_call_id" json:"tool_call_id,omitempty"`
	Embedding        *string   `gorm:"type:text" json:"embedding,omitempty"` // JSON 数组, 用户消息向量持久化
	CreatedAt        time.Time `gorm:"index:idx_conv_created,priority:2;autoCreateTime" json:"created_at"`
}
