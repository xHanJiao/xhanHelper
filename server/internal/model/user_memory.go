package model

import "time"

// UserMemory — 长期记忆 K-V
type UserMemory struct {
	ID             string    `gorm:"primaryKey;size:36" json:"id"`
	UserID         string    `gorm:"size:36;not null;index" json:"user_id"`
	MemoryKey      string    `gorm:"size:200;not null" json:"memory_key"`
	MemoryValue    string    `gorm:"type:text;not null" json:"memory_value"`
	Source         string    `gorm:"size:20;default:conversation" json:"source"`        // conversation / csv_import
	ConfirmCount   int       `gorm:"default:1" json:"confirm_count"`
	LastConfirmedAt time.Time `json:"last_confirmed_at"`
	EmbeddingID    *string   `gorm:"size:36" json:"embedding_id,omitempty"`            // 向量库 ID，预留
	CreatedAt      time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt      time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}
