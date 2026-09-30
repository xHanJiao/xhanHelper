package retrieval

import (
	"time"

	"xhan-helper/internal/model"
)

// --- Meta structs — 每种 source 定义自己的元数据类型 ---

// EntityMeta 记忆/笔记的元数据（memory 和 note 共用）。
type EntityMeta struct {
	Content   string    // 原始文本
	Topic     string    // 主题标签
	CreatedAt time.Time // 创建/更新时间
}

func (EntityMeta) isPacketMeta() {}

// MessageMeta 用户消息的元数据（unified_search 的 history 域使用）。
// 仅包含用户自己发送的消息，不含对话轮上下文和 AI 回复。
type MessageMeta struct {
	Content        string    // 消息原文
	CreatedAt      time.Time // 消息时间
	ConversationID string    // 所属会话 ID
}

func (MessageMeta) isPacketMeta() {}

// RecentMeta 新窗口注入的最近对话元数据（资源区 recent 段）。
type RecentMeta struct {
	Content   string
	Role      string // user / assistant
	CreatedAt time.Time
}

func (RecentMeta) isPacketMeta() {}

// HistoryMeta 历史消息的元数据。
// 一轮对话含多条消息，所以在 Meta 里存完整消息切片，builder 做嵌套 XML 渲染时使用。
type HistoryMeta struct {
	Messages []*model.Message
	HitDate  *time.Time // 向量命中的消息时间；XML date 属性优先使用此字段，nil 时 fallback 到 Messages[0].CreatedAt
}

func (HistoryMeta) isPacketMeta() {}

// ThemeMeta 主题的元数据。
type ThemeMeta struct {
	Description string
	StateText   string
}

func (ThemeMeta) isPacketMeta() {}

// SummaryMeta 对话摘要的元数据。
type SummaryMeta struct {
	SummaryID       string    // 摘要自身的 ID
	SummaryText     string    // 摘要正文
	ConversationID  string    // 所属会话
	CoverFromMsgID  string    // 覆盖消息起始
	CoverToMsgID    string    // 覆盖消息结束
	ParentSummaryID string    // 前序摘要 ID（空=无）
	Topics          string    // 逗号分隔的话题标签
	CreatedAt       time.Time // 摘要创建时间
}

func (SummaryMeta) isPacketMeta() {}

// ProfileMeta 用户画像稳定层的元数据。
type ProfileMeta struct {
	Key       string    // 字段名（籍贯/伴侣/...）
	Value     string    // 字段值
	Source    string    // explicit / llm_additional
	UpdatedAt time.Time // 更新时间
}

func (ProfileMeta) isPacketMeta() {}
