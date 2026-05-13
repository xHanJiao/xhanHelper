package model

import "time"

// UserMemory — 长期记忆 K-V
type UserMemory struct {
	ID     string `gorm:"primaryKey;size:36" json:"id"`
	UserID string `gorm:"uniqueIndex:idx_user_content;size:36;not null" json:"user_id"`
	// DEPRECATED: MemoryKey 已废弃，后续版本会移除，建议使用 ContentHash 进行去重
	MemoryKey       string    `gorm:"size:200;not null" json:"memory_key"`
	ContentHash     string    `gorm:"uniqueIndex:idx_user_content;size:64;not null" json:"content_hash"` // MD5(MemoryValue)
	MemoryValue     string    `gorm:"type:text;not null" json:"memory_value"`
	ExtractPrompt   *string   `gorm:"type:text" json:"extract_prompt,omitempty"`           // 提取该记忆时发给 LLM 的 prompt
	PromptHash      string    `gorm:"size:64;index" json:"prompt_hash"`                    // prompt 的 MD5 哈希，用于输入去重
	Source          string    `gorm:"size:20;default:conversation" json:"source"`          // conversation / csv_import
	RawJSON         *string   `gorm:"type:json;column:raw_json" json:"raw_json,omitempty"` // LLM 原始输出 JSON
	ConfirmCount    int       `gorm:"default:1" json:"confirm_count"`
	LastConfirmedAt time.Time `json:"last_confirmed_at"`
	CreatedAt       time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt       time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}
