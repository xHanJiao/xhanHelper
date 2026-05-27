package model

import "time"

// Note — 自由变量追踪笔记
type Note struct {
	ID        string    `gorm:"primaryKey;size:36" json:"id"`
	UserID    string    `gorm:"index:idx_user_topic,priority:1;size:36;not null" json:"user_id"`
	Topic     string    `gorm:"index:idx_user_topic,priority:2;size:100;not null" json:"topic"`
	Content   string    `gorm:"type:text;not null" json:"content"`
	Variables *string   `gorm:"type:json" json:"variables,omitempty"`
	Tags      string    `gorm:"size:500" json:"tags"`
	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}
