package model

import "time"

// LlmCostDetail 单次 LLM 调用 token 消耗明细。
// 每个 Chat/ChatSync 调用写入一条记录。
type LlmCostDetail struct {
	ID               uint      `gorm:"primaryKey"`
	Date             string    `gorm:"type:date;not null;index"`
	UserID           string    `gorm:"size:36;not null;default:'';index"`
	Category         string    `gorm:"type:varchar(30);not null;index"`
	SubCategory      string    `gorm:"type:varchar(50);not null"`
	Model            string    `gorm:"type:varchar(50);not null"`
	ConvID           string    `gorm:"size:36;index"`
	RefType          string    `gorm:"type:varchar(30);index"`
	RefID            string    `gorm:"size:36;index"`
	PromptTokens     int64     `gorm:"not null;default:0"`
	CompletionTokens int64     `gorm:"not null;default:0"`
	TotalTokens      int64     `gorm:"not null;default:0"`
	MetaJSON         string    `gorm:"type:text"`
	CreatedAt        time.Time `gorm:"autoCreateTime;index"`
}
