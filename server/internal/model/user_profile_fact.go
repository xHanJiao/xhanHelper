package model

import "time"

// UserProfileFact — 用户画像事实（field → fact 三级结构）。
// 一个字段（field）由多条事实（fact）组成；status 区分 active（当前生效）与
// outdated（已被后续提取淘汰，保留审计，不进 LLM 上下文）。
// BatchID 关联同一次提取产生的批次，用于 outdated 版本清理（保留最近 N 批）。
// 无 source 列：唯一写入通道（后处理 worker）下，置信度差异由字段定义派生
// （仅"补充说明"字段是 LLM 自行判断，其余字段只提取用户明确陈述）。
type UserProfileFact struct {
	ID        string    `gorm:"primaryKey;size:36" json:"id"`
	UserID    string    `gorm:"size:36;not null;index:idx_facts_user_field" json:"user_id"`
	Field     string    `gorm:"size:64;not null;index:idx_facts_user_field" json:"field"`
	Value     string    `gorm:"type:text;not null" json:"value"`
	Status    string    `gorm:"size:16;not null;default:active;index:idx_facts_user_field" json:"status"`
	BatchID   string    `gorm:"size:36;not null;index:idx_facts_batch" json:"batch_id"`
	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
}

// FactStatus — 事实状态。
const (
	FactStatusActive   = "active"   // 当前生效
	FactStatusOutdated = "outdated" // 已被淘汰（审计保留）
)
