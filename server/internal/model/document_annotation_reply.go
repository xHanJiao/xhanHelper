package model

import "time"

// DocumentAnnotationReply — 文档批注下的讨论消息（用户追问 / AI 回复）。
// 根批注在 DocumentAnnotation.note；本表只放批注之后的往来。
// 回复没有锚点语义，与批注分开存，避免污染 RelocateAll / 配额 / list_annotations。
type DocumentAnnotationReply struct {
	ID           string `gorm:"primaryKey;size:36" json:"id"`
	AnnotationID string `gorm:"index;size:36;not null" json:"annotation_id"`
	// DocumentID 冗余：批注不会换文档，关系不可变；换来文档级级联删 / 按文档批量取，免 JOIN。
	DocumentID string `gorm:"index;size:36;not null" json:"document_id"`
	UserID     string `gorm:"index;size:36;not null" json:"user_id"`
	// Role 角色：user（用户追问）| assistant（AI 回复）
	Role    string `gorm:"size:20;not null" json:"role"`
	Content string `gorm:"type:text;not null" json:"content"`
	// Source 来源（可选，便于以后筛选）：button | chat | followup
	Source    string    `gorm:"size:20" json:"source"`
	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
}
