package model

import "time"

// 会话模式常量：plan 模式下全部工具被服务端禁用（纯讨论状态，2026-08-14 起不再放行只读工具）。
// 模式只能由用户/客户端切换（PATCH /api/conversations/:id/mode），LLM 无控制权。
const (
	ConvModeWork = "work" // 正常工作模式（默认）：工具全量可用
	ConvModePlan = "plan" // 计划模式：纯讨论，全部工具被禁用
)

// Conversation — 对话会话
type Conversation struct {
	ID        string `gorm:"primaryKey;size:36" json:"id"`
	UserID    string `gorm:"size:36;not null;index" json:"user_id"`
	Title     string `gorm:"size:200;default:新对话" json:"title"`
	IsDeleted bool   `gorm:"default:false" json:"-"`
	IsDefault bool   `gorm:"default:false" json:"is_default"`
	// Deprecated: 旧的游标式记忆提取进度。post_chat_group 已改为 session-window 驱动，
	// 不再维护此字段，仅保留列用于旧数据兼容。
	LastExtractedMsgID *string `gorm:"size:36" json:"last_extracted_msg_id,omitempty"`
	// Deprecated: 同 LastExtractedMsgID，不再维护。
	LastExtractedAt *time.Time `json:"last_extracted_at,omitempty"`
	AgentID         *string    `gorm:"size:36;index" json:"agent_id,omitempty"`
	ParentID        *string    `gorm:"size:36" json:"parent_id,omitempty"` // 子会话（subAgent）的父会话 ID，nil=普通会话
	Mode            string     `gorm:"size:10;default:work" json:"mode"`   // 会话模式：work（默认）| plan（只读讨论）
	Extra           string     `gorm:"type:text;not null" json:"extra"`    // JSON: {"used_tool_ids": ["..."]}
	CreatedAt       time.Time  `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt       time.Time  `gorm:"autoUpdateTime" json:"updated_at"`
	Messages        []Message  `gorm:"foreignKey:ConversationID" json:"-"`
}

// ConversationExtra — Conversation.Extra 的结构化数据
type ConversationExtra struct {
	UsedToolIDs    []string         `json:"used_tool_ids"`
	Context        *ConversationCtx `json:"context,omitempty"`          // 信箱跳转带上的主题上下文
	SubAgentType   string           `json:"subagent_type,omitempty"`    // 子会话类型（"web_explore"/"file_explore"），空=普通会话
	SubAgentResult *SubAgentResult  `json:"sub_agent_result,omitempty"` // 子任务最近一次执行结果（Admin 子任务列表展示），空=未记录（改动前旧数据）
	ToolExposure   *ToolExposure    `json:"tool_exposure,omitempty"`    // 渐进式工具暴露注册表（窗口级，key=SessionStartMsgID）
}

// SubAgentResult — 子任务单次执行结果（RunSubAgent 结束时写回）。
type SubAgentResult struct {
	State   string    `json:"state"` // completed | timeout | failed
	Error   string    `json:"error,omitempty"`
	EndedAt time.Time `json:"ended_at"`
}

// ToolExposure — 渐进式工具暴露注册表（窗口级，函数粒度）。
// 同一会话窗口（SessionStartMsgID）内共享暴露集；窗口边界（新 SessionStartMsgID）自然失效，无清理逻辑。
type ToolExposure struct {
	SessionStartMsgID string   `json:"session_start_msg_id"`
	Tools             []string `json:"tools"` // 已暴露的函数条目："组名.函数名"（排序，字节稳定）。旧格式裸组名加载时展开为全部函数
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
