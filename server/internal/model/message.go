package model

import "time"

// 消息状态常量
const (
	MsgStatusNormal  = 1 // 正常
	MsgStatusDeleted = 2 // 已删除
)

// Message — 对话消息
type Message struct {
	ID               string    `gorm:"primaryKey;size:36" json:"id"`
	ConversationID   string    `gorm:"index:idx_conv_created,priority:1;index:idx_conv_role_created,priority:1;size:36;not null" json:"conversation_id"`
	UserID           string    `gorm:"size:36;not null;index" json:"user_id"`
	Role             string    `gorm:"index:idx_conv_role_created,priority:2;size:20;not null" json:"role"` // user | assistant | tool
	Content          string    `gorm:"type:text" json:"content"`
	OriginalContent  *string   `gorm:"type:text;column:original_content" json:"original_content,omitempty"`
	XMLContext       *string   `gorm:"type:mediumtext;column:xml_context" json:"xml_context,omitempty"`
	ReasoningContent *string   `gorm:"type:text;column:reasoning_content" json:"reasoning_content,omitempty"`
	ToolCallsJSON    *string   `gorm:"type:mediumtext;column:tool_calls_json" json:"tool_calls_json,omitempty"`
	ToolCallID       *string   `gorm:"size:36;column:tool_call_id" json:"tool_call_id,omitempty"`
	MemoryIDs        *string   `gorm:"type:mediumtext;column:memory_ids" json:"memory_ids,omitempty"`
	RequestJSON      *string   `gorm:"type:mediumtext;column:request_json" json:"request_json,omitempty"`
	ResponseJSON     *string   `gorm:"type:mediumtext;column:response_json" json:"response_json,omitempty"`
	IsInternal       bool      `gorm:"default:false" json:"is_internal"`        // 标记内部消息
	IsSubAgent       bool      `gorm:"default:false;index" json:"is_sub_agent"` // 子会话（subAgent）消息：不参与按用户扫描的后台统计/检索聚合
	Extra            *string   `gorm:"type:mediumtext;column:extra" json:"extra,omitempty"`
	Status           int       `gorm:"default:1" json:"status"` // 1=正常 2=已删除
	CreatedAt        time.Time `gorm:"index:idx_conv_created,priority:2;index:idx_conv_role_created,priority:3;autoCreateTime" json:"created_at"`
}
