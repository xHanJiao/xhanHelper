package model

import "time"

// Mission — 后台追踪任务（独立于主题体系）
type Mission struct {
	ID          string     `gorm:"primaryKey;size:36" json:"id"`
	UserID      string     `gorm:"size:36;not null;index" json:"user_id"`
	ThemeID     *string    `gorm:"size:36;index" json:"theme_id,omitempty"`
	Goal        string     `gorm:"type:text;not null" json:"goal"`
	Context     string     `gorm:"type:text" json:"context"`
	ToolsHint   string     `gorm:"size:300" json:"tools_hint"`
	CheckCron   string     `gorm:"size:30;default:daily" json:"check_cron"` // daily | weekly | 2daily
	Status      string     `gorm:"size:20;default:suggested" json:"status"` // suggested | active | paused | completed | rejected
	LastSummary string     `gorm:"type:text" json:"last_summary"`
	LastCheckAt *time.Time `json:"last_check_at,omitempty"`
	NextCheckAt *time.Time `json:"next_check_at,omitempty"`
	CheckCount  int        `gorm:"default:0" json:"check_count"`
	Source      string     `gorm:"size:20;default:auto" json:"source"` // auto | user
	ConvID      *string    `gorm:"size:36" json:"conv_id,omitempty"`
	CreatedAt   time.Time  `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt   time.Time  `gorm:"autoUpdateTime" json:"updated_at"`
}
