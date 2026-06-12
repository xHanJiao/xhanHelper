package model

import "time"

// ThemeState — 主题状态页
type ThemeState struct {
	ID               string     `gorm:"primaryKey;size:36" json:"id"`
	UserID           string     `gorm:"size:36;not null;index" json:"user_id"`
	Name             string     `gorm:"size:100;not null" json:"name"`
	Description      string     `gorm:"size:500" json:"description"`
	Status           string     `gorm:"size:20;default:draft" json:"status"`           // draft | active | closed
	Duration         string     `gorm:"size:20;default:''" json:"duration"`            // long | medium | short | ephemeral（LLM 标注的关注周期）
	StateText        string     `gorm:"type:text" json:"state_text"`                   // LLM 阅读的叙事摘要（异步从 facts 生成，≤500字）
	StateFacts       string     `gorm:"type:text" json:"state_facts"`                  // JSONL 原子事实，只追加不修改，用于向量搜索
	HistoryStateText *string    `gorm:"type:text" json:"history_state_text,omitempty"` // 历史状态页（Phase 15 迁移后不再使用）
	Metadata         *string    `gorm:"type:json" json:"metadata,omitempty"`           // LLM 原始输出（candidate_info 等）
	EmbeddingID      *string    `gorm:"size:36" json:"embedding_id,omitempty"`
	CreatedAt        time.Time  `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt        time.Time  `gorm:"autoUpdateTime" json:"updated_at"`
	LastRunAt        *time.Time `json:"last_run_at,omitempty"`
	ConversationID   *string    `gorm:"size:36" json:"conversation_id,omitempty"` // 关联的对话 ID
}
