package model

import "time"

// UserMemory — 长期记忆 K-V（P4 启用）
type UserMemory struct {
	ID         string    `gorm:"primaryKey;size:36" json:"id"`
	DeviceID   string    `gorm:"uniqueIndex:uk_device_key;size:36;not null" json:"device_id"`
	MemoryKey  string    `gorm:"uniqueIndex:uk_device_key;size:200;not null" json:"memory_key"`
	MemoryValue string   `gorm:"type:text;not null" json:"memory_value"`
	CreatedAt  time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt  time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}
