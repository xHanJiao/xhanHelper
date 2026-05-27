package model

import "time"

// ContentVersion — 通用内容版本快照，支持 document/note/prompt 等任意实体类型
type ContentVersion struct {
	ID         string    `gorm:"primaryKey;size:36" json:"id"`
	EntityType string    `gorm:"index:idx_cv_entity,priority:1;size:50;not null" json:"entity_type"`
	EntityID   string    `gorm:"index:idx_cv_entity,priority:2;size:36;not null" json:"entity_id"`
	Version    int       `gorm:"not null" json:"version"`
	Snapshot   string    `gorm:"type:text;not null" json:"snapshot"`
	CreatedAt  time.Time `gorm:"autoCreateTime" json:"created_at"`
}
