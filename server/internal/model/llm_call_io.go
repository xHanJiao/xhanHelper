package model

import "time"

// LLMCallIO — 单次 LLM 调用审计（I/O 内容）。
// 覆盖 post_chat（memory/profile/summary）+ admin/agent_summary + tool/reminder_gen，
// 不含主聊天（已有 request_json）与 reflection（该分类自 2026-11-15 起无产出）。
// 保留 7 天，超期由清理任务删除。
type LLMCallIO struct {
	ID               string    `gorm:"primaryKey;size:36" json:"id"`
	Category         string    `gorm:"type:varchar(30);not null" json:"category"`
	SubCategory      string    `gorm:"type:varchar(50);not null" json:"sub_category"`
	UserID           string    `gorm:"size:36;not null;default:''" json:"user_id,omitempty"`
	ConvID           string    `gorm:"size:36;not null;default:'';index:idx_io_conv" json:"conv_id,omitempty"`
	RefType          string    `gorm:"type:varchar(30);not null;default:''" json:"ref_type,omitempty"`
	RefID            string    `gorm:"size:36;not null;default:''" json:"ref_id,omitempty"`
	Model            string    `gorm:"type:varchar(50);not null" json:"model"`
	SystemPrompt     string    `gorm:"type:text" json:"system_prompt,omitempty"`
	MessagesJSON     string    `gorm:"type:text" json:"messages_json,omitempty"`
	Response         string    `gorm:"type:text" json:"response,omitempty"`
	PromptTokens     int64     `gorm:"not null;default:0" json:"prompt_tokens"`
	CompletionTokens int64     `gorm:"not null;default:0" json:"completion_tokens"`
	LatencyMs        int64     `gorm:"not null;default:0" json:"latency_ms"`
	ErrorMsg         string    `gorm:"type:text" json:"error_msg,omitempty"`
	CreatedAt        time.Time `gorm:"autoCreateTime;index:idx_io_created" json:"created_at"`
}
