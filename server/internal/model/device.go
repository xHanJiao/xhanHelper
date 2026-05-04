package model

import "time"

// Device — 设备（iOS / macOS 客户端）
type Device struct {
	ID         string    `gorm:"primaryKey;size:36" json:"id"`
	Token      string    `gorm:"uniqueIndex;size:64;not null" json:"token"`
	Name       string    `gorm:"size:100" json:"name"`
	Platform   string    `gorm:"size:20;not null" json:"platform"` // ios | macos
	CreatedAt  time.Time `gorm:"autoCreateTime" json:"created_at"`
	LastSeenAt time.Time `gorm:"autoUpdateTime" json:"last_seen_at"`
}
