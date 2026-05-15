package model

import "time"

// MailboxItem — 信箱消息
type MailboxItem struct {
	ID         string    `gorm:"primaryKey;size:36" json:"id"`
	UserID     string    `gorm:"size:36;not null;index" json:"user_id"`
	Title      string    `gorm:"size:200;not null" json:"title"`
	Content    string    `gorm:"type:text;not null" json:"content"`
	Type       string    `gorm:"size:30;not null" json:"type"` // daily_brief | theme_finding | candidate_theme
	Source     string    `gorm:"size:50" json:"source"`        // 来源主题名或 "system"
	IsRead     bool      `gorm:"default:false" json:"is_read"`
	IsArchived bool      `gorm:"default:false" json:"is_archived"`
	Metadata   *string   `gorm:"type:json" json:"metadata,omitempty"`
	CreatedAt  time.Time `gorm:"autoCreateTime" json:"created_at"`
}
