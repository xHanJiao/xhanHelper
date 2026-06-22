package model

import "time"

// ConversationSummary — 对话摘要，覆盖窗口外的历史消息。
// 增量合并模式：每次生成新摘要时旧段标记 superseded，始终只有 1 段 active。
//
// 实现 model.ChunkEntity 接口，可复用 EntitySource 做 chunk+full-embedding 两阶段检索。
type ConversationSummary struct {
	ID              string    `gorm:"primaryKey;size:36" json:"id"`
	ConversationID  string    `gorm:"index:idx_conv_status,priority:1;size:36;not null" json:"conversation_id"`
	UserID          string    `gorm:"size:36;not null" json:"user_id"`
	SummaryText     string    `gorm:"type:text;not null" json:"summary_text"`
	CoverFromMsgID  string    `gorm:"size:36" json:"cover_from_msg_id"`
	CoverToMsgID    string    `gorm:"size:36" json:"cover_to_msg_id"`
	ParentSummaryID *string   `gorm:"size:36" json:"parent_summary_id,omitempty"`
	Topics          string    `gorm:"size:500" json:"topics"`
	RequestJSON     *string   `gorm:"type:mediumtext" json:"request_json,omitempty"`
	ResponseJSON    *string   `gorm:"type:mediumtext" json:"response_json,omitempty"`
	Status          string    `gorm:"index:idx_conv_status,priority:2;size:20;not null;default:active" json:"status"`
	CreatedAt       time.Time `gorm:"autoCreateTime" json:"created_at"`
}

// ChunkEntity 接口实现
func (s ConversationSummary) GetID() string           { return s.ID }
func (s ConversationSummary) GetUserID() string       { return s.UserID }
func (s ConversationSummary) GetContent() string      { return s.SummaryText }
func (s ConversationSummary) GetCreatedAt() time.Time { return s.CreatedAt }
func (s ConversationSummary) GetTopic() string        { return s.Topics }
