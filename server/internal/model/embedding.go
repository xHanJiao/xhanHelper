package model

import "time"

// EntityType 实体类型
const (
	EntityTypeMessage = "message"
	EntityTypeNote    = "note"
	EntityTypeMemory  = "memory"
	EntityTypeTool    = "tool"
)

// Embedding — 向量存储
type Embedding struct {
	ID         string `gorm:"primaryKey;size:36" json:"id"`
	EntityType string `gorm:"uniqueIndex:idx_entity,priority:1;size:20;not null" json:"entity_type"`
	EntityID   string `gorm:"uniqueIndex:idx_entity,priority:2;size:36;not null" json:"entity_id"`
	UserID     string `gorm:"index:idx_user_type,priority:1;size:36;not null" json:"user_id"`
	Model      string `gorm:"size:100;not null" json:"model"`
	// TODO 增加把嵌入之前的内容也保存进来以便追查
	Embedding *string   `gorm:"type:text" json:"-"` // JSON 数组向量数据
	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}
