package model

import "time"

// Document — 用户明确要求 LLM 生成的结构化 Markdown 文档，给人阅读
type Document struct {
	ID             string    `gorm:"primaryKey;size:36" json:"id"`
	UserID         string    `gorm:"index;size:36;not null" json:"user_id"`
	Title          string    `gorm:"size:500;not null" json:"title"`
	Content        string    `gorm:"type:mediumtext;not null" json:"content"`
	Tags           string    `gorm:"size:500" json:"tags"`
	ConversationID *string   `gorm:"size:36" json:"conversation_id,omitempty"`
	CreatedAt      time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt      time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}
