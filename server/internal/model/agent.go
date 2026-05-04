package model

import "time"

// Agent — 用户自定义智能体
type Agent struct {
	ID           string    `gorm:"primaryKey;size:36" json:"id"`
	UserID       string    `gorm:"size:36;not null;index" json:"user_id"`
	Name         string    `gorm:"size:50;not null" json:"name"`
	SystemPrompt string    `gorm:"type:text;not null" json:"system_prompt"`
	Model        string    `gorm:"size:50" json:"model"`
	CreatedAt    time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt    time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}
