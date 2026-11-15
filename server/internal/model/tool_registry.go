package model

import "time"

// ToolRegistry — 动态工具注册表
type ToolRegistry struct {
	ID          string    `gorm:"primaryKey;size:36" json:"id"`
	Name        string    `gorm:"size:100;not null" json:"name"`
	Description string    `gorm:"type:text;not null" json:"description"`
	SchemaJSON  string    `gorm:"type:json;not null;column:schema_json" json:"schema_json"`
	ScriptPath  string    `gorm:"size:500;not null;default:''" json:"script_path"`
	Status      string    `gorm:"size:20;not null;default:draft" json:"status"`
	Category    string    `gorm:"size:100;not null;default:''" json:"category"`
	EmbeddingID string    `gorm:"size:36;not null;default:''" json:"embedding_id"` // 指向 embeddings 表
	UserID      string    `gorm:"size:36;not null" json:"user_id"`
	CreatedAt   time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt   time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}
