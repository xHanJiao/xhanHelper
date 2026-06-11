package model

import "time"

// SearchUsage — 搜索 API 用量追踪，DB 持久化，重启不丢失
type SearchUsage struct {
	ID         string    `gorm:"primaryKey;size:36" json:"id"`
	Provider   string    `gorm:"size:50;uniqueIndex;not null" json:"provider"`
	TotalQuota int       `gorm:"not null;default:0" json:"total_quota"`
	UsedCount  int       `gorm:"not null;default:0" json:"used_count"`
	ExpiresAt  time.Time `gorm:"not null" json:"expires_at"`
	CreatedAt  time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt  time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}
