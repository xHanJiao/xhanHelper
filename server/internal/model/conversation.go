package model

import "time"

// Conversation — 对话会话
type Conversation struct {
	ID                 string     `gorm:"primaryKey;size:36" json:"id"`
	UserID             string     `gorm:"size:36;not null;index" json:"user_id"`
	Title              string     `gorm:"size:200;default:新对话" json:"title"`
	IsDeleted          bool       `gorm:"default:false" json:"-"`
	IsDefault          bool       `gorm:"default:false" json:"is_default"`
	LastExtractedMsgID *string    `gorm:"size:36" json:"last_extracted_msg_id,omitempty"`
	LastExtractedAt    *time.Time `json:"last_extracted_at,omitempty"`
	AgentID            *string    `gorm:"size:36" json:"agent_id,omitempty"`
	Extra              string     `gorm:"type:text;not null" json:"extra"` // JSON: {"used_tool_ids": ["..."]}
	CreatedAt          time.Time  `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt          time.Time  `gorm:"autoUpdateTime" json:"updated_at"`
	Messages           []Message  `gorm:"foreignKey:ConversationID" json:"-"`
}

// ConversationExtra — Conversation.Extra 的结构化数据
type ConversationExtra struct {
	UsedToolIDs []string         `json:"used_tool_ids"`
	Context     *ConversationCtx `json:"context,omitempty"` // 信箱跳转带上的主题上下文
}

// ConversationCtx — 会话上下文（从 mailbox 跳转带入）
type ConversationCtx struct {
	ThemeID          string `json:"theme_id"`
	EntityType       string `json:"entity_type"`
	EntityID         string `json:"entity_id"`
	MailboxItemID    string `json:"mailbox_item_id"`
	Title            string `json:"title"`
	ThemeStateCursor string `json:"theme_state_cursor,omitempty"` // 主题状态更新的 cursor（上次更新到的 messageID）
}
