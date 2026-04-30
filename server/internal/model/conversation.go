package model

import "time"

// Conversation — 对话会话
type Conversation struct {
	ID        string    `gorm:"primaryKey;size:36" json:"id"`
	DeviceID  string    `gorm:"size:36;not null;index" json:"device_id"`
	Title     string    `gorm:"size:200;default:新对话" json:"title"`
	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt time.Time `gorm:"autoUpdateTime" json:"updated_at"`
	Messages  []Message `gorm:"foreignKey:ConversationID" json:"-"`
}
