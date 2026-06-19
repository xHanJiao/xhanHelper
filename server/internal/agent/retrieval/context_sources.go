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

// MissionMeta 任务的元数据（预留，暂无实现）。
type MissionMeta struct {
	Goal        string
	LastSummary string
}

func (MissionMeta) isPacketMeta() {}
