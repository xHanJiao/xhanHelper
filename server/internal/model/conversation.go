package model

import "time"

// Conversation — 对话会话
type Conversation struct {
	ID                 string     `gorm:"primaryKey;size:36" json:"id"`
	UserID             string     `gorm:"size:36;not null;index" json:"user_id"`
	Title              string     `gorm:"size:200;default:新对话" json:"title"`
	IsDeleted          bool       `gorm:"default:false" json:"-"`
	LastExtractedMsgID *string    `gorm:"size:36" json:"last_extracted_msg_id,omitempty"`
	LastExtractedAt    *time.Time `json:"last_extracted_at,omitempty"`
	AgentID            *string    `gorm:"size:36" json:"agent_id,omitempty"`
	Extra              string     `gorm:"type:text;not null" json:"extra"` // JSON: {"used_tool_ids": ["..."]}
	CreatedAt          time.Time  `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt          time.Time  `gorm:"autoUpdateTime" json:"updated_at"`
	Messages           []Message  `gorm:"foreignKey:ConversationID" json:"-"`
}

// ConversationExtra — Conversation.Extra 的结构化数据
type ConversationExtra struct {
	UsedToolIDs []string `json:"used_tool_ids"`
}
