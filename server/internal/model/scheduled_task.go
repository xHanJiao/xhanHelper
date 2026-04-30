package model

import "time"

// ScheduledTask — 定时任务（P4 启用）
type ScheduledTask struct {
	ID         string    `gorm:"primaryKey;size:36" json:"id"`
	DeviceID   string    `gorm:"size:36;not null" json:"device_id"`
	ToolName   string    `gorm:"size:50;not null" json:"tool_name"`
	FuncName   string    `gorm:"size:50;not null" json:"func_name"`
	ParamsJSON *string   `gorm:"type:json;column:params_json" json:"params_json,omitempty"`
	CronExpr   string    `gorm:"size:50;not null" json:"cron_expr"`
	Enabled    bool      `gorm:"default:true" json:"enabled"`
	CreatedAt  time.Time `gorm:"autoCreateTime" json:"created_at"`
}
