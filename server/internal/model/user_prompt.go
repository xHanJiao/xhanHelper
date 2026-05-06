package model

import "time"

// UserPrompt — 用户自定义 System Prompt（支持版本历史）
type UserPrompt struct {
	ID           string    `gorm:"primaryKey;size:36" json:"id"`
	UserID       string    `gorm:"size:36;not null" json:"user_id"`
	PromptType   string    `gorm:"size:20;not null" json:"prompt_type"` // flash | pro
	SystemPrompt string    `gorm:"type:text;not null" json:"system_prompt"`
	Status       string    `gorm:"size:20;not null;default:active" json:"status"` // active | inactive
	CreatedAt    time.Time `gorm:"autoCreateTime" json:"created_at"`
}
