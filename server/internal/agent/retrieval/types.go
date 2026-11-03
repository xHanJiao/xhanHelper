package retrieval

import (
	"context"

	"xhan-helper/internal/llm"
)

// 各域检索结果描述（供 system prompt 注入和 tool 结果共用）。
const (
	DescMemory = "大模型从对话记录里异步提取出的具有长期价值的信息，包括：用户的事实/偏好/习惯/计划、用户反复讨论的话题、" +
		"用户表达的观点和判断、用户分享的外部信息、用户的反思/自我剖析/复盘、用户做的重大决定及理由、用户从经历中总结的经验教训、" +
		"用户偏好或价值观的变化。本条通过双路向量检索（分块级+全文级）按实体去重后拼接，并经过时间衰减过滤。"
	DescNotes = "大模型在对话过程中记录的值得长期追踪的信息——事实、数据、观点、事件、个人状态、人物关系等。" +
		"本条通过双路向量检索（分块级+全文级）按实体去重后拼接。"
	DescThemes = "用户身上正在发生的事件主题，由系统自动发现和维护。所有活跃主题均会列出：" +
		"高相关主题（score >= 阈值）附带 desc 属性（当前状态简述），" +
		"低相关主题仅罗列描述文本。通过多查询向量（当前消息+近10轮用户消息）双路加权融合召回。" +
		"每个主题可通过 id 调用 theme_search.get_detail 获取完整状态页。"
	DescHistory = "之前的聊天记录，以 round 的方式组织，一个 round 代表了用户一次发送的消息和接收到的返回内容。" +
		"本条通过单路向量检索召回后，扩展为完整对话轮次。"
	DescMessages = "用户历史聊天记录中的发言内容，通过语义或关键词检索召回。仅返回用户自己发送的消息原文，不附带对话轮的上下文和 AI 回复。"
	DescSummary  = "大模型在每次对话 session 结束后异步提取的对话摘要，按时间从旧到新排列，" +
		"每条带有 created_at 创建时间，覆盖用户最近几天的所有会话，可用于快速了解用户最近聊了什么、发生了什么事。" +
		"每条摘要提供了覆盖的起始终止消息ID（cover_from/cover_to），" +
		"可以通过消息检索工具结合这两个ID来获取对应的详细聊天记录；" +
		"parent 属性指向更早一段摘要的ID，可用 get_summary 工具追溯，" +
		"也可用 get_summary.query_by_time 按时间范围查看更早的摘要。本条自动附着。"
)

// PacketMeta 标记接口——只允许包内定义的 struct 放入 ContextPacket.Meta。
// 每个 source 类型定义自己的 Meta struct 并实现此接口。
type PacketMeta interface {
	isPacketMeta()
}

// ContextPacket — 上下文片段，来自任意数据源（也是 SearchProvider 的返回类型）。
type ContextPacket struct {
	ID    string     // 唯一标识
	Type  string     // memory | note | history | theme | mission | summary
	Text  string     // 格式化文本（mission 仍依赖此字段；memory/note/history/theme 的 XML 生成已切到 Meta，Text 保留用于 trace 兼容）
	Score float64    // 向量检索原始分数
	Meta  PacketMeta `json:"-"` // source 自定义元数据（entityMeta / historyMeta / themeMeta）
}

// NearMissItem — 被阈值或规则过滤掉的候选项，用于 trace 调试
type NearMissItem struct {
	ID          string  `json:"id"`
	Text        string  `json:"text,omitempty"` // 截断文本
	VectorScore float64 `json:"vector_score"`
	Reason      string  `json:"reason"` // "below_threshold" | "wrong_prefix" | "same_conversation" | "db_not_found"
}

// SourceConfig 单个检索源的阈值和数量限制。
type SourceConfig struct {
	Threshold float64 `json:"threshold"`
	Limit     int     `json:"limit"`
}

// Resolve 用 cfg 填充当前值为 0 的字段。
func (s *SourceConfig) Resolve(cfg SourceConfig) {
	if s.Threshold <= 0 {
		s.Threshold = cfg.Threshold
	}
	if s.Limit <= 0 {
		s.Limit = cfg.Limit
	}
}

// SearchOptions — 检索参数，供 ContextSource.Fetch 和 SearchProvider.Search 共用。
type SearchOptions struct {
	Query             string // 用户原始文本（ranker 关键词打分用）
	UserID            string
	ConversationID    string
	Embedding         []float64   // 当前用户消息的 embedding（向量检索用）
	HistoryEmbeddings [][]float64 // 最近 N 条用户消息的 embedding，仅 ThemeSource 用于多历史消息搜索，解决单句模糊消息匹配不到主题的问题

	Memory  SourceConfig // 记忆检索
	Note    SourceConfig // 笔记检索
	History SourceConfig // 历史对话检索
	Theme   SourceConfig // 主题检索
	Summary SourceConfig // 摘要检索

	// 排除已在当前消息列表中的消息 ID（避免系统提示词与历史消息重复）
	ExcludeMsgIDs map[string]bool
}

// ContextSource — 上下文数据源接口
type ContextSource interface {
	Name() string
	Fetch(ctx context.Context, opts SearchOptions) (selected []ContextPacket, nearMiss []NearMissItem, err error)
	Warmup(ctx context.Context, embedder *llm.Embedder)
}

// Entry — 向量条目筛选视图
type Entry struct {
	ID       string
	Vec      []float64
	Metadata map[string]string
}

// SearchResult — 向量检索结果
type SearchResult struct {
	ID       string
	Score    float64
	Metadata map[string]string
}

// VectorStore — 向量存储接口（agent.VectorStore 满足此接口）
type VectorStore interface {
	Search(query []float64, topK int, filter func(Entry) bool) []SearchResult
	Upsert(id string, vec []float64, metadata map[string]string)
	Size() int
}
