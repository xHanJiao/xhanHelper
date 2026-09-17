package model

import "time"

// DocumentAnnotation — 文档划线批注。note 为空 = 纯划线，非空 = 划线 + 批注。
// 锚定四元组（Quote / Prefix / Suffix / 偏移）由服务端在 Markdown 源文上计算，
// 正文变化后由 service.RelocateAll 重算，详见 docs/design/document-highlight-annotation/design.md
type DocumentAnnotation struct {
	ID         string `gorm:"primaryKey;size:36" json:"id"`
	UserID     string `gorm:"index;size:36;not null" json:"user_id"`
	DocumentID string `gorm:"index;size:36;not null" json:"document_id"`

	// Quote 用户选中的原文（DOM 文本，不含 Markdown 标记），≤500 字
	Quote string `gorm:"type:text;not null" json:"quote"`
	// Prefix / Suffix 创建时的前后文快照（各 ≤60 字），多重命中时用于消歧；
	// 重定位成功后不刷新，避免漂移累积
	Prefix string `gorm:"type:text" json:"prefix"`
	Suffix string `gorm:"type:text" json:"suffix"`
	// StartOffset / EndOffset 源文 rune 偏移；Orphaned 时保留上次已知位置仅供排查
	StartOffset int `gorm:"not null;default:0" json:"start_offset"`
	EndOffset   int `gorm:"not null;default:0" json:"end_offset"`

	// Note 批注正文，空 = 纯划线
	Note string `gorm:"type:text" json:"note"`
	// Orphaned 重定位失败（原文已被改写）时置 true，由服务端唯一裁定
	Orphaned bool `gorm:"index;not null;default:false" json:"orphaned"`
	// QuoteRewritten 重定位时走了夹逼（原文被局部改写），Quote 已被服务端改写成新文本。
	// 记下来是为了让"改动了用户数据"这件事可见，而不是静默发生。
	QuoteRewritten bool `gorm:"not null;default:false" json:"quote_rewritten"`

	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}
