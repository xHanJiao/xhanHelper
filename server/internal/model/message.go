package model

import "time"

// 消息状态常量
const (
	MsgStatusNormal  = 1 // 正常
	MsgStatusDeleted = 2 // 已删除
	// MsgStatusHidden = 3 隐藏：接力等控制轮。落库留档，但不进对话记录——
	// 不展示、不参与 LLM 上下文/检索/搜索/分享，也不参与任何后台后处理（摘要/记忆/主题/反思/画像）。
	// 所有消息读取路径均带 status = MsgStatusNormal，故置为 3 即自动隔离。
	MsgStatusHidden = 3
)

// Message — 对话消息
type Message struct {
	ID               string  `gorm:"primaryKey;size:36" json:"id"`
	ConversationID   string  `gorm:"index:idx_conv_created,priority:1;index:idx_conv_role_created,priority:1;size:36;not null" json:"conversation_id"`
	UserID           string  `gorm:"size:36;not null;index" json:"user_id"`
	Role             string  `gorm:"index:idx_conv_role_created,priority:2;size:20;not null" json:"role"` // user | assistant | tool
	Content          string  `gorm:"type:longtext" json:"content"`
	OriginalContent  *string `gorm:"type:text;column:original_content" json:"original_content,omitempty"`
	XMLContext       *string `gorm:"type:mediumtext;column:xml_context" json:"xml_context,omitempty"`
	ReasoningContent *string `gorm:"type:text;column:reasoning_content" json:"reasoning_content,omitempty"`
	EncryptedContent *string `gorm:"type:text;column:encrypted_content" json:"-"` // 方舟思考加密原文，仅服务器回传用，不暴露前端
	ToolCallsJSON    *string `gorm:"type:mediumtext;column:tool_calls_json" json:"tool_calls_json,omitempty"`
	ToolCallID       *string `gorm:"size:36;column:tool_call_id" json:"tool_call_id,omitempty"`
	MemoryIDs        *string `gorm:"type:mediumtext;column:memory_ids" json:"memory_ids,omitempty"`
	// ImageIDs — 本条消息附带的图片 id 列表（JSON 数组字符串，存 ImageAsset.ID）。
	// 只写 user 消息；图片本身不入 content（TEXT 存不下），走引用。
	ImageIDs     *string   `gorm:"type:mediumtext;column:image_ids" json:"image_ids,omitempty"`
	RequestJSON  *string   `gorm:"type:mediumtext;column:request_json" json:"request_json,omitempty"`
	ResponseJSON *string   `gorm:"type:mediumtext;column:response_json" json:"response_json,omitempty"`
	IsInternal   bool      `gorm:"default:false" json:"is_internal"`        // 标记内部消息
	IsSubAgent   bool      `gorm:"default:false;index" json:"is_sub_agent"` // 子会话（subAgent）消息：不参与按用户扫描的后台统计/检索聚合
	Extra        *string   `gorm:"type:mediumtext;column:extra" json:"extra,omitempty"`
	Status       int       `gorm:"default:1" json:"status"` // 1=正常 2=已删除
	CreatedAt    time.Time `gorm:"index:idx_conv_created,priority:2;index:idx_conv_role_created,priority:3;autoCreateTime" json:"created_at"`
}
