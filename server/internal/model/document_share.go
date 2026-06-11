package model

import "time"

// DocumentShare 文档分享链接
type DocumentShare struct {
	ID         string    `gorm:"primaryKey;size:36" json:"id"`
	DocumentID string    `gorm:"index;size:36;not null" json:"document_id"`
	Token      string    `gorm:"uniqueIndex;size:64;not null" json:"token"`
	ExpiresAt  time.Time `gorm:"not null" json:"expires_at"`
	CreatedAt  time.Time `gorm:"autoCreateTime" json:"created_at"`
	VisitCount int64     `gorm:"default:0" json:"visit_count"`
}
