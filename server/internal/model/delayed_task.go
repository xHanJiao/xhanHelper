package model

import (
	"encoding/json"
	"time"
)

// DelayedTask — 通用延迟任务表，cron 轮询到期执行
type DelayedTask struct {
	ID         string    `gorm:"primaryKey;size:36" json:"id"`
	UserID     string    `gorm:"size:36;not null;index" json:"user_id"`
	TaskType   string    `gorm:"size:50;not null;index" json:"task_type"`        // post_chat | ...
	TaskMeta   string    `gorm:"type:json;not null" json:"task_meta"`            // DelayedTaskMeta 序列化
	Status     string    `gorm:"size:20;not null;default:pending" json:"status"` // pending | done | failed
	RetryCount int       `gorm:"default:0" json:"retry_count"`
	ProcessAt  time.Time `gorm:"not null;index" json:"process_at"`
	CreatedAt  time.Time `gorm:"autoCreateTime" json:"created_at"`
}

// DelayedTaskMeta — TaskMeta 列的统一结构，不同 TaskType 使用不同字段组合
type DelayedTaskMeta struct {
	ConvID    string `json:"conv_id,omitempty"`
	LastMsgID string `json:"last_msg_id,omitempty"` // session_end_summary 使用的最后一条消息 ID
}

// SetMeta 将 DelayedTaskMeta 序列化写入 TaskMeta 列
func (t *DelayedTask) SetMeta(m DelayedTaskMeta) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	t.TaskMeta = string(b)
	return nil
}

// ParseMeta 将 TaskMeta 列反序列化为 DelayedTaskMeta
func (t *DelayedTask) ParseMeta() (DelayedTaskMeta, error) {
	var m DelayedTaskMeta
	err := json.Unmarshal([]byte(t.TaskMeta), &m)
	return m, err
}
