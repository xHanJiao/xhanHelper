package model

import "time"

// PendingFinding — 待投递的主题发现
type PendingFinding struct {
	ID           string    `gorm:"primaryKey;size:36" json:"id"`
	UserID       string    `gorm:"size:36;not null;index" json:"user_id"`
	ThemeID      string    `gorm:"size:36;not null;index" json:"theme_id"`
	ThemeName    string    `gorm:"size:100" json:"theme_name"`
	Title        string    `gorm:"size:200" json:"title"`
	Content      string    `gorm:"type:text" json:"content"`
	Urgency      string    `gorm:"size:20" json:"urgency"`
	SourceRefs   string    `gorm:"type:json" json:"source_refs"`
	RetryCount   int       `gorm:"default:0" json:"retry_count"`
	Status       string    `gorm:"size:20;default:pending" json:"status"` // pending | delivered | failed
	SourceConvID string    `gorm:"size:36" json:"source_conv_id,omitempty"`
	CreatedAt    time.Time `gorm:"autoCreateTime" json:"created_at"`
}
