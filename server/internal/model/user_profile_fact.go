package model

import "time"

// UserProfileFact — 用户画像事实（field → fact 三级结构）。
// 一个字段（field）由多条事实（fact）组成；status 区分 active（当前生效，进注入）、
// pending（LLM 推断、观察中，不进注入）、outdated（已淘汰，审计保留）、
// rejected（用户手动删除，永不复活）。
// BatchID 关联同一次提取产生的批次，用于 outdated 版本清理（保留最近 N 批）。
//
// 时间与证据列（v2）：CreatedAt 是"这行写入库的时间"（每次重写都会刷新，不代表事实何时发生），
// 观察时间与证据另存 —— ObservedAt 首次观察到、LastSeenAt 最近一次被证实、
// EvidenceCount 被独立提及次数（pending → active 的晋升依据）、SourceMsgID 来源消息。
type UserProfileFact struct {
	ID      string `gorm:"primaryKey;size:36" json:"id"`
	UserID  string `gorm:"size:36;not null;index:idx_facts_user_field" json:"user_id"`
	Field   string `gorm:"size:64;not null;index:idx_facts_user_field" json:"field"`
	Value   string `gorm:"type:text;not null" json:"value"`
	Status  string `gorm:"size:16;not null;default:active;index:idx_facts_user_field" json:"status"`
	BatchID string `gorm:"size:36;not null;index:idx_facts_batch" json:"batch_id"`

	ObservedAt    *time.Time `json:"observed_at,omitempty"`                           // 首次被观察到
	LastSeenAt    *time.Time `json:"last_seen_at,omitempty"`                          // 最近一次被证实
	EvidenceCount int        `gorm:"not null;default:1" json:"evidence_count"`        // 被独立提及次数
	SourceMsgID   string     `gorm:"size:36" json:"source_msg_id"`                    // 最近一次来源消息
	Source        string     `gorm:"size:16;not null;default:inferred" json:"source"` // inferred / user_stated / user / migrated
	InvalidatedAt *time.Time `json:"invalidated_at,omitempty"`                        // 失效时间（outdated/rejected 时写入）

	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
}

// FactStatus — 事实状态。
const (
	FactStatusActive   = "active"   // 当前生效（进注入）
	FactStatusPending  = "pending"  // 观察中：LLM 推断、证据不足（不进注入）
	FactStatusOutdated = "outdated" // 已被淘汰（审计保留）
	FactStatusRejected = "rejected" // 用户手动删除，LLM 永不复活
)

// FactSource — 事实来源。
const (
	FactSourceInferred   = "inferred"    // LLM 从对话推断
	FactSourceUserStated = "user_stated" // 用户在本段对话中明确自陈
	FactSourceUser       = "user"        // 用户在设置页手写/手改（LLM 不得覆盖）
	FactSourceMigrated   = "migrated"    // 老数据重建/迁移写入
	FactSourceRewrite    = "rewrite"     // 每周整合重写压缩产出
)
