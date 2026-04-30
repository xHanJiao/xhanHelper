package model

import "time"

// Message — 对话消息
type Message struct {
	ID             string    `gorm:"primaryKey;size:36" json:"id"`
	ConversationID string    `gorm:"size:36;not null;index" json:"conversation_id"`
	Role           string    `gorm:"size:20;not null" json:"role"`           // user | assistant | tool
	Content        string    `gorm:"type:text" json:"content"`
	ToolCallsJSON  *string   `gorm:"type:json;column:tool_calls_json" json:"tool_calls_json,omitempty"`
	ToolCallID     *string   `gorm:"size:36;column:tool_call_id" json:"tool_call_id,omitempty"`
	CreatedAt      time.Time `gorm:"autoCreateTime" json:"created_at"`
}
