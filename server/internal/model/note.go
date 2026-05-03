package model

import "time"

// Note — 自由变量追踪笔记
type Note struct {
	ID        string    `gorm:"primaryKey;size:36" json:"id"`
	UserID  string    `gorm:"size:36;not null;index" json:"user_id"`
	Topic     string    `gorm:"size:100;not null;index" json:"topic"`
	Content   string    `gorm:"type:text;not null" json:"content"`
	Variables *string   `gorm:"type:json" json:"variables,omitempty"`
	Tags      string    `gorm:"size:500" json:"tags"`
	Embedding *string   `gorm:"type:text" json:"embedding,omitempty"` // JSON 数组, 向量持久化
	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
}
