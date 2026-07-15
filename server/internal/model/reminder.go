package model

import "time"

const (
	ReminderStatusGenerating   = "generating"
	ReminderStatusPending      = "pending"
	ReminderStatusAcknowledged = "acknowledged"
	ReminderStatusCancelled    = "cancelled"
)

type Reminder struct {
	ID string `gorm:"primaryKey;size:36" json:"id"`
	// 所属用户
	UserID string `gorm:"size:36;not null;index:idx_reminders_query,priority:1" json:"user_id"`
	// 提醒原文
	Content string `gorm:"type:text;not null" json:"content"`
	// 触发时间（精确到分钟）
	Deadline time.Time `gorm:"not null;index:idx_reminders_query,priority:3" json:"deadline"`
	// 循环规则 JSON，空串=一次性，预留
	RecurrenceRule string `gorm:"type:json;not null;default:''" json:"recurrence_rule"`
	// generating | pending | acknowledged | cancelled
	Status string `gorm:"size:20;not null;default:'generating';index:idx_reminders_query,priority:2" json:"status"`
	// 系统通知标题，异步 LLM 生成
	NotificationTitle string `gorm:"type:text;not null;default:''" json:"notification_title"`
	// 系统通知正文，异步 LLM 生成
	NotificationBody string `gorm:"type:text;not null;default:''" json:"notification_body"`
	// 用户点通知后助手说的话，异步 LLM 生成
	TriggerMessage string `gorm:"type:text;not null;default:''" json:"trigger_message"`
	// ack 时写入 messages 表的消息 ID，用于幂等控制
	TriggerMessageMsgID string `gorm:"size:36;not null;default:''" json:"trigger_message_msg_id"`
	// 用户点通知时间，zero=未处理
	AcknowledgedAt time.Time `gorm:"not null" json:"acknowledged_at"`
	// 助手提起过的时间，zero=未提过
	MentionedAt time.Time `gorm:"not null" json:"mentioned_at"`
	CreatedAt   time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt   time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}
