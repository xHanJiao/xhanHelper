package model

import "time"

// EmbeddingChunk — 实体的句子级 chunk（notes/memories 拆分后每个句子一个 chunk）
type EmbeddingChunk struct {
	ID         string    `gorm:"primaryKey;size:36" json:"id"`
	ParentType string    `gorm:"index:idx_parent,priority:1;size:20;not null" json:"parent_type"` // "note" | "memory"
	ParentID   string    `gorm:"index:idx_parent,priority:2;size:36;not null" json:"parent_id"`
	UserID     string    `gorm:"size:36;not null;index" json:"user_id"`
	ChunkIndex int       `gorm:"not null" json:"chunk_index"`
	Content    string    `gorm:"type:text;not null" json:"content"`
	CreatedAt  time.Time `gorm:"autoCreateTime" json:"created_at"`
}
