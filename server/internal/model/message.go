package model

import "time"

// Message — 对话消息
type Message struct {
	ID               string    `gorm:"primaryKey;size:36" json:"id"`
	ConversationID   string    `gorm:"index:idx_conv_created,priority:1;size:36;not null" json:"conversation_id"`
	UserID           string    `gorm:"size:36;not null;index" json:"user_id"`
	Role             string    `gorm:"size:20;not null" json:"role"` // user | assistant | tool
	Content          string    `gorm:"type:text" json:"content"`
	ReasoningContent *string   `gorm:"type:text;column:reasoning_content" json:"reasoning_content,omitempty"`
	ToolCallsJSON    *string   `gorm:"type:json;column:tool_calls_json" json:"tool_calls_json,omitempty"`
	ToolCallID       *string   `gorm:"size:36;column:tool_call_id" json:"tool_call_id,omitempty"`
	MemoryIDs        *string   `gorm:"type:json;column:memory_ids" json:"memory_ids,omitempty"`
	RequestJSON      *string   `gorm:"type:json;column:request_json" json:"request_json,omitempty"`
	ResponseJSON     *string   `gorm:"type:json;column:response_json" json:"response_json,omitempty"`
	IsInternal       bool      `gorm:"default:false" json:"is_internal"` // 标记内部消息
	Status           int       `gorm:"default:1" json:"status"` // 1=正常 2=已删除
	CreatedAt        time.Time `gorm:"index:idx_conv_created,priority:2;autoCreateTime" json:"created_at"`
}
