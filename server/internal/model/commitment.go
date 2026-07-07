package model

import "time"

// Commitment — 待办/承诺（独立于主题体系）
type Commitment struct {
	ID             string     `gorm:"primaryKey;size:36" json:"id"`
	UserID         string     `gorm:"size:36;not null;index" json:"user_id"`
	Content        string     `gorm:"type:text;not null" json:"content"`                                                      // 承诺或待办内容
	Deadline       *time.Time `json:"deadline,omitempty"`                                                                     // 截止时间（可为空）
	RecurrenceRule *string    `gorm:"type:json" json:"recurrence_rule,omitempty"`                                             // null=一次性
	NextDeadline   *time.Time `gorm:"index:idx_commitments_status_deadline,priority:2" json:"next_deadline,omitempty"`        // 下一次触发时间
	Status         string     `gorm:"size:20;default:pending;index:idx_commitments_status_deadline,priority:1" json:"status"` // pending | triggered | done | expired
	SourceConvID   string     `gorm:"size:36" json:"source_conv_id,omitempty"`                                                // 来源会话
	CreatedAt      time.Time  `gorm:"autoCreateTime" json:"created_at"`
}
