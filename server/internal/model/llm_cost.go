package model

import "time"

// LlmCostDetail 单次 LLM 调用 token 消耗明细。
// 每个 Chat/ChatSync 调用写入一条记录。
type LlmCostDetail struct {
	ID                    uint      `gorm:"primaryKey" json:"id"`
	Date                  string    `gorm:"type:date;not null;index:idx_date_user_cat,priority:1;index:idx_user_date,priority:2" json:"date"`
	UserID                string    `gorm:"size:36;not null;default:'';index:idx_date_user_cat,priority:2;index:idx_user_date,priority:1" json:"user_id"`
	Category              string    `gorm:"type:varchar(30);not null;index:idx_date_user_cat,priority:3;index" json:"category"`
	SubCategory           string    `gorm:"type:varchar(50);not null" json:"sub_category"`
	Model                 string    `gorm:"type:varchar(50);not null" json:"model"`
	ConvID                string    `gorm:"size:36;index" json:"conv_id,omitempty"`
	RefType               string    `gorm:"type:varchar(30);index:idx_ref_type_ref_id,priority:1" json:"ref_type,omitempty"`
	RefID                 string    `gorm:"size:36;index:idx_ref_type_ref_id,priority:2;index" json:"ref_id,omitempty"`
	PromptTokens          int64     `gorm:"not null;default:0" json:"prompt_tokens"`
	PromptCacheHitTokens  int64     `gorm:"not null;default:0" json:"prompt_cache_hit"`
	PromptCacheMissTokens int64     `gorm:"not null;default:0" json:"prompt_cache_miss"`
	CompletionTokens      int64     `gorm:"not null;default:0" json:"completion"`
	TotalTokens           int64     `gorm:"not null;default:0" json:"total_tokens"`
	MetaJSON              string    `gorm:"type:text" json:"meta_json,omitempty"`
	CreatedAt             time.Time `gorm:"autoCreateTime;index" json:"created_at"`
}
