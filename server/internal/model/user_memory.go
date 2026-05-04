package model

import "time"

// UserMemory — 长期记忆 K-V
type UserMemory struct {
	ID              string    `gorm:"primaryKey;size:36" json:"id"`
	UserID          string    `gorm:"size:36;not null;index" json:"user_id"`
	MemoryKey       string    `gorm:"size:200;not null" json:"memory_key"`
	MemoryValue     string    `gorm:"type:text;not null" json:"memory_value"`
	ExtractPrompt   *string   `gorm:"type:text" json:"extract_prompt,omitempty"`  // 提取该记忆时发给 LLM 的 prompt
	PromptHash      string    `gorm:"size:64;index" json:"prompt_hash"`           // prompt 的 MD5 哈希，用于输入去重
	Source          string    `gorm:"size:20;default:conversation" json:"source"` // conversation / csv_import
	ConfirmCount    int       `gorm:"default:1" json:"confirm_count"`
	LastConfirmedAt time.Time `json:"last_confirmed_at"`
	Embedding       *string   `gorm:"type:text" json:"embedding,omitempty"` // JSON 数组, 向量持久化
	CreatedAt       time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt       time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}
