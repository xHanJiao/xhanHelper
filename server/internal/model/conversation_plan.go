package model

import "time"

// PlanStep — 执行计划中的单个步骤
type PlanStep struct {
	Content  string `json:"content"`
	Status   string `json:"status"` // pending | in_progress | completed | cancelled
	Priority string `json:"priority,omitempty"`
}

// ConversationPlan — 会话执行计划（todo），每会话一行，steps 全量替换。
// 轻量设计（对齐 opencode todo）：无复杂状态机，stale/过期无所谓。
type ConversationPlan struct {
	ID             string    `gorm:"primaryKey;size:36" json:"id"`
	UserID         string    `gorm:"size:36;not null;index" json:"user_id"`
	ConversationID string    `gorm:"size:36;not null;index" json:"conversation_id"`
	Goal           string    `gorm:"size:500" json:"goal"`
	StepsJSON      string    `gorm:"type:mediumtext" json:"-"` // JSON 数组 [PlanStep]
	UpdatedAt      time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}
