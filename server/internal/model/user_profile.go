package model

import "time"

// UserProfile — 用户画像稳定层。
// 一字段一行（user_id + field 复合主键），不存整份 JSON，
// 避免全量覆盖、并发冲突、回滚粒度粗。
// source 取值：explicit（用户明确陈述）/ llm_additional（LLM 补充说明专用）。
type UserProfile struct {
	UserID     string    `gorm:"primaryKey;size:36" json:"user_id"`
	Field      string    `gorm:"primaryKey;size:64" json:"field"`
	FieldValue string    `gorm:"type:text;not null" json:"field_value"`
	Source     string    `gorm:"size:16;not null;default:explicit" json:"source"`
	UpdatedAt  time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

// ProfileSource — 画像来源类型。
const (
	ProfileSourceExplicit      = "explicit"       // 用户明确陈述，高置信度
	ProfileSourceLLMAdditional = "llm_additional" // LLM 补充说明，置信度较低
)
