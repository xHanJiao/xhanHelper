package model

import "time"

// MailboxItem — 信箱消息
type MailboxItem struct {
	ID         string    `gorm:"primaryKey;size:36" json:"id"`
	UserID     string    `gorm:"size:36;not null;index" json:"user_id"`
	Title      string    `gorm:"size:200;not null" json:"title"`
	Content    string    `gorm:"type:text;not null" json:"content"`   // 站内信/推送文案（短）
	Greeting   string    `gorm:"type:text" json:"greeting,omitempty"` // 对话开场白（长）
	Type       string    `gorm:"size:30;not null" json:"type"`        // daily_brief | theme_finding | candidate_theme
	Source     string    `gorm:"size:50" json:"source"`
	EntityType string    `gorm:"size:20" json:"entity_type,omitempty"`
	EntityID   string    `gorm:"size:36" json:"entity_id,omitempty"`
	IsRead     bool      `gorm:"default:false" json:"is_read"`
	IsArchived bool      `gorm:"default:false" json:"is_archived"`
	CreatedAt  time.Time `gorm:"autoCreateTime" json:"created_at"`
}

// PackedMailboxItem — 打包后的信箱消息（含实时计算的 tags，不持久化）
type PackedMailboxItem struct {
	ID         string   `json:"id"`
	UserID     string   `json:"user_id"`
	Title      string   `json:"title"`
	Content    string   `json:"content"`
	Greeting   string   `json:"greeting,omitempty"`
	Type       string   `json:"type"`
	Source     string   `json:"source,omitempty"`
	EntityType string   `json:"entity_type,omitempty"`
	EntityID   string   `json:"entity_id,omitempty"`
	IsRead     bool     `json:"is_read"`
	IsArchived bool     `json:"is_archived"`
	Tags       []string `json:"tags"`
	CreatedAt  string   `json:"created_at"`
}
