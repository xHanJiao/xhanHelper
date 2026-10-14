package model

import "time"

// 会话模式常量（二档）：
//   - work   关闭：工具全量可用
//   - plan   计划模式：仅只读工具可用，写操作服务端执行级拦截（2026-09-15 只读化）
//
// 模式只能由用户/客户端切换（PATCH /api/conversations/:id/mode），LLM 无控制权。
const (
	ConvModeWork = "work" // 正常工作模式（默认）：工具全量可用
	ConvModePlan = "plan" // 计划模式：仅只读工具
	// 历史值：严格计划模式（无任何工具），2026-10-14 撤档。库里旧行可能仍是它，
	// 读侧一律经 NormalizeConvMode 归到 plan——否则会被当成不受限模式、静默放开全部工具。
	legacyConvModeStrict = "strict"
)

// ModeRestrictsTools 报告该模式下工具能力是否受限（plan 只读）。
// 用于编排器统一短路渐进式暴露、客户端工具目录/执行等能力。
func ModeRestrictsTools(mode string) bool {
	return mode == ConvModePlan
}

// NormalizeConvMode 归一会话模式：空值与历史 strict 档 → 默认/plan，其余原样返回。
func NormalizeConvMode(mode string) string {
	switch mode {
	case "":
		return ConvModeWork
	case legacyConvModeStrict:
		return ConvModePlan
	default:
		return mode
	}
}

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
	Mode            string     `gorm:"size:10;default:work" json:"mode"`   // 会话模式：work（默认）| plan（只读）
	Extra           string     `gorm:"type:text;not null" json:"extra"`    // JSON: {"used_tool_ids": ["..."]}
	PinnedAt        *time.Time `json:"pinned_at,omitempty"`                // 置顶时间，非空=已 pin；列表排序用（pin 时间倒排）
	CreatedAt       time.Time  `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt       time.Time  `gorm:"autoUpdateTime" json:"updated_at"`
	Messages        []Message  `gorm:"foreignKey:ConversationID" json:"-"`
}

// ConversationExtra — Conversation.Extra 的结构化数据
type ConversationExtra struct {
	UsedToolIDs    []string        `json:"used_tool_ids"`
	Ephemeral      bool            `json:"ephemeral,omitempty"`        // 无痕会话：窗口结束后硬销毁（Stage A 隐藏 + Stage B 物理删除）
	SubAgentType   string          `json:"subagent_type,omitempty"`    // 子会话类型（"web_explore"/"file_explore"），空=普通会话
	SubAgentResult *SubAgentResult `json:"sub_agent_result,omitempty"` // 子任务最近一次执行结果（Admin 子任务列表展示），空=未记录（改动前旧数据）
	ToolExposure   *ToolExposure   `json:"tool_exposure,omitempty"`    // 渐进式工具暴露注册表（窗口级，key=SessionStartMsgID）
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
