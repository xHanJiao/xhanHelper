package model

import "time"

// Fact — 单条长期事实。content 是嵌入/注入/比对的三重单位。
// 与 UserMemory 的区别：subject/kind/fields 三字段结构化，status 软删，
// 且 content 之外不存 raw JSON。
type Fact struct {
	ID     string `gorm:"primaryKey;size:36" json:"id"`
	UserID string `gorm:"index:idx_fact_user_subject;index:idx_fact_user_hash;size:36;not null" json:"user_id"`
	// Subject 这条事实关于谁/什么，不一定是用户本人。分组/统计标签，不参与检索。
	Subject string `gorm:"index:idx_fact_user_subject;size:200;not null;default:''" json:"subject"`
	// Kind 见 FactKindState / FactKindEvent。
	Kind    string `gorm:"size:16;not null" json:"kind"`
	Content string `gorm:"type:text;not null" json:"content"`
	// ContentHash = md5(normalize(content))，精确判重用。
	ContentHash string `gorm:"index:idx_fact_user_hash;size:64;not null" json:"content_hash"`
	// Fields 可空 JSON：{"due_at":"...","has_doc_ref":"..."}。只落库，当前不注入。
	Fields *string `gorm:"type:json" json:"fields,omitempty"`
	// Status 见 FactStatusActive / FactStatusDeleted。
	Status    string    `gorm:"size:16;not null;default:active" json:"status"`
	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

// FactKind — 事实类型。只统计，不参与检索。
const (
	FactKindState = "state" // 当前状态/身份/关系/计划/判断，会被后续事实取代
	FactKindEvent = "event" // 已发生的事件/观点，只增不改
)

// FactStatusDeleted 软删状态。active 复用 UserProfileFact 的 FactStatusActive（同语义）。
const FactStatusDeleted = "deleted"
