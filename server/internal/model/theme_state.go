package model

import "time"

// ThemeState — 主题状态页
type ThemeState struct {
	ID          string     `gorm:"primaryKey;size:36" json:"id"`
	UserID      string     `gorm:"size:36;not null;index" json:"user_id"`
	Name        string     `gorm:"size:100;not null" json:"name"`
	Description string     `gorm:"size:500" json:"description"`
	Status      string     `gorm:"size:20;default:active" json:"status"` // active | archived | closed
	StateText   string     `gorm:"type:text" json:"state_text"`          // LLM 维护的自由文本状态摘要
	EmbeddingID *string    `gorm:"size:36" json:"embedding_id,omitempty"`
	CreatedAt   time.Time  `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt   time.Time  `gorm:"autoUpdateTime" json:"updated_at"`
	LastRunAt   *time.Time `json:"last_run_at,omitempty"`
}
