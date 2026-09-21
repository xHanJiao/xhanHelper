package model

import "time"

// FactChangelog — 事实变更审计。仅 UPDATE / DELETE 记录（ADD 无前值）。
// 保留 old_content 作为未来回滚能力的数据基础。
type FactChangelog struct {
	ID          string    `gorm:"primaryKey;size:36" json:"id"` // idgen.Next()
	UserID      string    `gorm:"size:36;not null;index:idx_factlog_user" json:"user_id"`
	FactID      string    `gorm:"size:36;not null;index:idx_factlog_fact" json:"fact_id"`
	Op          string    `gorm:"size:16;not null" json:"op"` // 见 FactOpUpdate / FactOpDelete
	OldContent  string    `gorm:"type:text;not null" json:"old_content"`
	NewContent  string    `gorm:"type:text;not null;default:''" json:"new_content"` // 仅 update
	SourceMsgID string    `gorm:"size:36;not null;default:''" json:"source_msg_id"`
	CreatedAt   time.Time `gorm:"autoCreateTime" json:"created_at"`
}

// FactChangelogOp — 变更类型。
const (
	FactOpUpdate = "update"
	FactOpDelete = "delete"
)
