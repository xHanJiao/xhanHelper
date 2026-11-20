package model

import "time"

// ProfileRewriteState — 画像每周整合重写的运行状态（每用户一行）。
//
// 为什么单独存一张表：重写的触发是「距上次 ≥7 天 且 有新活动」，而"上次"没法从
// user_profile_facts 推出来 —— 一次重写如果没有任何字段变化就不会产生新行，
// 推导出来的时间永远停在上一次真有变化的时刻，会退化成每天白跑一次。
type ProfileRewriteState struct {
	UserID           string     `gorm:"primaryKey;size:36" json:"user_id"`
	LastRunAt        *time.Time `json:"last_run_at,omitempty"`                        // 上次成功重写的时间
	LastEventCount   int        `gorm:"not null;default:0" json:"last_event_count"`   // 上次输入的事件条数
	LastSessionCount int        `gorm:"not null;default:0" json:"last_session_count"` // 上次输入的会话数
	LastFactCount    int        `gorm:"not null;default:0" json:"last_fact_count"`    // 重写后 active 事实条数
	LastStatus       string     `gorm:"size:16" json:"last_status"`                   // ok / error
	LastError        string     `gorm:"size:512" json:"last_error,omitempty"`         // 上次失败原因（成功时清空）
	CreatedAt        time.Time  `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt        time.Time  `gorm:"autoUpdateTime" json:"updated_at"`
}

// 重写运行状态取值。
const (
	ProfileRewriteStatusOK    = "ok"
	ProfileRewriteStatusError = "error"
)
