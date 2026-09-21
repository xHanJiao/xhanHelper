package model

import "time"

// 文档软删状态：0 有效、1 已删除（回收站，7 天后物理清理）
const (
	DocumentStatusActive  = 0
	DocumentStatusDeleted = 1
)

// Document — 用户明确要求 LLM 生成的结构化 Markdown 文档，给人阅读
type Document struct {
	ID             string    `gorm:"primaryKey;size:36" json:"id"`
	UserID         string    `gorm:"index;size:36;not null" json:"user_id"`
	Title          string    `gorm:"size:500;not null" json:"title"`
	Content        string    `gorm:"type:mediumtext;not null" json:"content"`
	Tags           string    `gorm:"size:500" json:"tags"`
	Status         int       `gorm:"not null;default:0;index" json:"status"`
	ConversationID *string   `gorm:"size:36" json:"conversation_id,omitempty"`
	CreatedAt      time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt      time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}
