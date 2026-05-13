package model

import "time"

// Agent — 用户自定义智能体
type Agent struct {
	ID           string    `gorm:"primaryKey;size:36" json:"id"`
	UserID       string    `gorm:"size:36;not null;index" json:"user_id"`
	Name         string    `gorm:"size:50;not null" json:"name"`
	SystemPrompt string    `gorm:"type:text;not null" json:"system_prompt"`
	Summary      string    `gorm:"type:varchar(100)" json:"summary"` // LLM 自动生成的 50 字内摘要
	Model        string    `gorm:"size:50" json:"model"`
	CreatedAt    time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt    time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}
