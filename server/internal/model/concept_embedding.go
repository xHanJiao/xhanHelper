package model

import "time"

// ConceptEmbedding — 短文本概念嵌入（topic / tag 等）
type ConceptEmbedding struct {
	ID         string    `gorm:"primaryKey;size:20" json:"id"`                                              // 雪花 ID（有序）
	Type       string    `gorm:"uniqueIndex:idx_type_value_user,priority:1;size:20;not null" json:"type"`   // "topic" | "tag"
	Value      string    `gorm:"uniqueIndex:idx_type_value_user,priority:2;size:200;not null" json:"value"` // "职业规划"
	UserID     string    `gorm:"uniqueIndex:idx_type_value_user,priority:3;index:idx_type_user,priority:1;size:36;not null" json:"user_id"`
	ParentType string    `gorm:"size:20" json:"parent_type"` // "note" | "memory"（来源追溯）
	ParentID   string    `gorm:"size:36" json:"parent_id"`   // 来源实体的 UUID
	CreatedAt  time.Time `gorm:"autoCreateTime" json:"created_at"`
}
