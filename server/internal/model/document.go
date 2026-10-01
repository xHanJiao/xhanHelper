package model

import (
	"strings"
	"time"
)

// 文档软删状态：0 有效、1 已删除（回收站，7 天后物理清理）
const (
	DocumentStatusActive  = 0
	DocumentStatusDeleted = 1
)

// 文档读者层级：回答「这篇给谁读」。
// finished = 给用户读（列表默认呈现）；internal = 给助手后续工作用（默认不呈现，拿到链接仍可读）。
// 定义成「读者是谁」而非「要不要显示」——过滤规则是消费方，不是定义，见 docs/design/document-audience/。
const (
	DocumentAudienceFinished = "finished"
	DocumentAudienceInternal = "internal"
)

// DocumentAudienceFilterAll 过滤哨兵（不是落库值）：列表/词云想「不过滤」时用它。
const DocumentAudienceFilterAll = "all"

// ValidDocumentAudience 校验 audience 落库值合法（仅 finished / internal，不含 all）。
func ValidDocumentAudience(s string) bool {
	return s == DocumentAudienceFinished || s == DocumentAudienceInternal
}

// ParseDocumentAudienceFilter 解析过滤参数：缺省/空 → finished（列表默认只给成品）；
// internal → 只看材料；all → 不过滤（返回空串）。ok=false 表示非法取值。
func ParseDocumentAudienceFilter(raw string) (audience string, ok bool) {
	switch strings.TrimSpace(raw) {
	case "", DocumentAudienceFinished:
		return DocumentAudienceFinished, true
	case DocumentAudienceInternal:
		return DocumentAudienceInternal, true
	case DocumentAudienceFilterAll:
		return "", true
	default:
		return "", false
	}
}

// Document — 结构化 Markdown 文档。audience 决定它给用户还是给助手后续工作用。
type Document struct {
	ID             string    `gorm:"primaryKey;size:36" json:"id"`
	UserID         string    `gorm:"index;size:36;not null" json:"user_id"`
	Title          string    `gorm:"size:500;not null" json:"title"`
	Content        string    `gorm:"type:mediumtext;not null" json:"content"`
	Tags           string    `gorm:"size:500" json:"tags"`
	Audience       string    `gorm:"size:20;not null;default:'internal'" json:"audience"`
	Status         int       `gorm:"not null;default:0;index" json:"status"`
	ConversationID *string   `gorm:"size:36" json:"conversation_id,omitempty"`
	CreatedAt      time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt      time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}
