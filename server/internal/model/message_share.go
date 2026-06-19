package model

import "time"

// MessageShare 消息分享链接
type MessageShare struct {
	ID             string    `gorm:"primaryKey;size:36" json:"id"`
	Token          string    `gorm:"uniqueIndex;size:64;not null" json:"token"`
	ConversationID string    `gorm:"index;size:36;not null" json:"conversation_id"`
	MessageIDs     string    `gorm:"type:mediumtext;not null" json:"message_ids"` // JSON array of message IDs
	CreatedBy      string    `gorm:"size:36;not null;index" json:"created_by"`
	ExpiresAt      time.Time `gorm:"not null" json:"expires_at"`
	VisitCount     int64     `gorm:"default:0" json:"visit_count"`
	CreatedAt      time.Time `gorm:"autoCreateTime" json:"created_at"`
}
