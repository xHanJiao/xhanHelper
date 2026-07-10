package ctxflow

import (
	"math"
	"sort"
	"time"

	"xhan-helper/internal/agent/retrieval"
)

const (
	descMemories = "大模型从对话记录里异步提取出的具有长期价值的信息，包括：用户的事实/偏好/习惯/计划、用户反复讨论的话题、" +
		"用户表达的观点和判断、用户分享的外部信息、用户的反思/自我剖析/复盘、用户做的重大决定及理由、用户从经历中总结的经验教训、" +
		"用户偏好或价值观的变化。本条通过双路向量检索（分块级+全文级）按实体去重后拼接，并经过时间衰减过滤。"

	descNotes = "大模型在对话过程中记录的值得长期追踪的信息——事实、数据、观点、事件、个人状态、人物关系等。" +
		"本条通过双路向量检索（分块级+全文级）按实体去重后拼接。"

	descThemes = "通过记忆和笔记这些碎片化的信息聚类出的事件簇，代表真实世界中用户身上正在发生的事情，包含事件的描述和进行状态。" +
		"本条通过多查询向量（当前消息+近10轮用户消息）双路加权融合召回。"

	descHistory = "之前的聊天记录，以 round 的方式组织，一个 round 代表了用户一次发送的消息和接收到的返回内容。" +
		"本条通过单路向量检索召回后，扩展为完整对话轮次。"

	descSummary = "大模型在一次对话结束后，异步提取的对话摘要。每次开始新会话的时候，" +
		"会将上次会话的摘要附着在用户消息中。它提供了此次摘要覆盖的起始终止消息ID，" +
		"可以通过消息检索工具结合这两个ID来获取之前的详细聊天记录；" +
		"它还提供了在上次会话摘要之前，更早会话摘要的ID，可以通过这个ID查看更早的对话摘要，以快速浏览之前的聊天会话信息。本条在新会话开始时自动附着。"
)

const (
	TypeSummary         = "summary"
	TypeHistory         = "history"
	TypeMemory          = "memory"
	TypeNote            = "note"
	TypeTheme           = "theme"
	TypeCurrentDatetime = "current_datetime"
)

// typeOrder 定义所有块的渲染顺序，也是 typeRank 的单一数据源。
var typeOrder = []string{TypeSummary, TypeHistory, TypeMemory, TypeNote, TypeTheme, TypeCurrentDatetime}

// typeRank 从 typeOrder 派生，供排序时 O(1) 查找顺序。
var typeRank = func() map[string]int {
	m := make(map[string]int, len(typeOrder))
	for i, typ := range typeOrder {
		m[typ] = i
	}
	return m
}()

// Block 让所有格式化块可收集、排序、统一编码。
type Block interface {
	TypeKey() string // 对应 typeOrder 中的 key
}

// ==================== 数据结构 ====================
// 同一套 struct 可通过 xml/json tag 支持多种编码格式。

// --- memory ---
type memoryItem struct {
	Topic   string  `xml:"topic,attr,omitempty" json:"topic,omitempty"`
	Date    string  `xml:"date,attr,omitempty" json:"date,omitempty"`
	Score   float64 `xml:"score,attr,omitempty" json:"score,omitempty"`
	Content string  `xml:",chardata" json:"content"`
}
type memoriesBlock struct {
	XMLName     struct{}     `xml:"memories" json:"-"`
	Description string       `xml:"description,attr,omitempty" json:"description,omitempty"`
	Items       []memoryItem `xml:"memory" json:"items"`
}

func (*memoriesBlock) TypeKey() string { return TypeMemory }

// --- note ---
type noteItem struct {
	Topic   string  `xml:"topic,attr,omitempty" json:"topic,omitempty"`
	Date    string  `xml:"date,attr,omitempty" json:"date,omitempty"`
	Score   float64 `xml:"score,attr,omitempty" json:"score,omitempty"`
	Content string  `xml:",chardata" json:"content"`
}
type notesBlock struct {
	XMLName     struct{}   `xml:"notes" json:"-"`
	Description string     `xml:"description,attr,omitempty" json:"description,omitempty"`
	Items       []noteItem `xml:"note" json:"items"`
}

func (*notesBlock) TypeKey() string { return TypeNote }

// --- history ---
type historyMsg struct {
	Role    string `xml:"role,attr" json:"role"`
	Content string `xml:",chardata" json:"content"`
}
type historyRound struct {
	Messages []historyMsg `xml:"message" json:"messages"`
	Date     string       `xml:"date,attr,omitempty" json:"date,omitempty"`
	Score    float64      `xml:"score,attr,omitempty" json:"score,omitempty"`
}
type historyBlock struct {
	XMLName     struct{}       `xml:"history" json:"-"`
	Description string         `xml:"description,attr,omitempty" json:"description,omitempty"`
	Rounds      []historyRound `xml:"round" json:"rounds"`
}

func (*historyBlock) TypeKey() string { return TypeHistory }

// --- theme ---
type themeItem struct {
	Description string  `xml:"desc,attr,omitempty" json:"description,omitempty"`
	Score       float64 `xml:"score,attr,omitempty" json:"score,omitempty"`
	Content     string  `xml:",chardata" json:"content"`
}
type themesBlock struct {
	XMLName     struct{}    `xml:"topics" json:"-"`
	Description string      `xml:"description,attr,omitempty" json:"description,omitempty"`
	Items       []themeItem `xml:"topic" json:"items"`
}

func (*themesBlock) TypeKey() string { return TypeTheme }

// --- summary ---
type summaryItem struct {
	ID              string  `xml:"id,attr,omitempty" json:"id,omitempty"`
	Topics          string  `xml:"topics,attr,omitempty" json:"topics,omitempty"`
	CoverFrom       string  `xml:"cover_from,attr,omitempty" json:"cover_from,omitempty"`
	CoverTo         string  `xml:"cover_to,attr,omitempty" json:"cover_to,omitempty"`
	ParentSummaryID string  `xml:"parent,attr,omitempty" json:"parent_summary_id,omitempty"`
	Score           float64 `xml:"score,attr,omitempty" json:"score,omitempty"`
	Content         string  `xml:",chardata" json:"content"`
}
type summariesBlock struct {
	XMLName     struct{}      `xml:"summaries" json:"-"`
	Description string        `xml:"description,attr,omitempty" json:"description,omitempty"`
	Items       []summaryItem `xml:"summary" json:"items"`
}

func (*summariesBlock) TypeKey() string { return TypeSummary }

// --- current_datetime ---
type currentDatetimeBlock struct {
	XMLName struct{} `xml:"current_datetime" json:"-"`
	Content string   `xml:",chardata"`
}

func (*currentDatetimeBlock) TypeKey() string { return TypeCurrentDatetime }

// --- user_query ---
type userQueryBlock struct {
	XMLName struct{} `xml:"user_query" json:"-"`
	Content string   `xml:",chardata" json:"content"`
}

// ==================== 构建器注册 ====================

type blockBuilder func(items []retrieval.ContextPacket) Block

// blockBuilders 按 type 注册构建函数。新增类型只需加一行。
var blockBuilders = map[string]blockBuilder{
	TypeMemory:          buildMemoriesBlock,
	TypeNote:            buildNotesBlock,
	TypeHistory:         buildHistoryBlock,
	TypeTheme:           buildThemesBlock,
	TypeSummary:         buildSummariesBlock,
	TypeCurrentDatetime: buildCurrentDatetimeBlock,
}

// buildBlock 按 type 查找构建器并返回 Block，无匹配返回 nil。
func buildBlock(typ string, items []retrieval.ContextPacket) Block {
	if b, ok := blockBuilders[typ]; ok {
		return b(items)
	}
	return nil
}

func buildMemoriesBlock(items []retrieval.ContextPacket) Block {
	b := memoriesBlock{
		Description: descMemories,
	}
	for _, pkt := range items {
		it := memoryItem{Content: pkt.Text, Score: roundScore(pkt.Score)}
		if meta, ok := pkt.Meta.(retrieval.EntityMeta); ok {
			it.Content = meta.Content
			if meta.Topic != "" {
				it.Topic = meta.Topic
			}
			if !meta.CreatedAt.IsZero() {
				it.Date = meta.CreatedAt.Format("2006年01月02日")
			}
		}
		b.Items = append(b.Items, it)
	}
	return &b
}

func buildNotesBlock(items []retrieval.ContextPacket) Block {
	b := notesBlock{
		Description: descNotes,
	}
	for _, pkt := range items {
		it := noteItem{Content: pkt.Text, Score: roundScore(pkt.Score)}
		if meta, ok := pkt.Meta.(retrieval.EntityMeta); ok {
			it.Content = meta.Content
			if meta.Topic != "" {
				it.Topic = meta.Topic
			}
			if !meta.CreatedAt.IsZero() {
				it.Date = meta.CreatedAt.Format("2006年01月02日")
			}
		}
		b.Items = append(b.Items, it)
	}
	return &b
}

func buildHistoryBlock(items []retrieval.ContextPacket) Block {
	b := historyBlock{
		Description: descHistory,
	}
	for _, pkt := range items {
		var msgs []historyMsg
		var dateStr string
		if meta, ok := pkt.Meta.(retrieval.HistoryMeta); ok {
			if meta.HitDate != nil {
				dateStr = meta.HitDate.Format("2006年01月02日")
			}
			for _, m := range meta.Messages {
				if m == nil {
					continue
				}
				if dateStr == "" {
					dateStr = m.CreatedAt.Format("2006年01月02日")
				}
				msgs = append(msgs, historyMsg{Role: m.Role, Content: m.Content})
			}
		}
		if len(msgs) == 0 {
			continue
		}
		b.Rounds = append(b.Rounds, historyRound{Messages: msgs, Date: dateStr, Score: roundScore(pkt.Score)})
	}
	if len(b.Rounds) == 0 {
		return nil
	}
	return &b
}

func buildThemesBlock(items []retrieval.ContextPacket) Block {
	b := themesBlock{
		Description: descThemes,
	}
	for _, pkt := range items {
		it := themeItem{Content: pkt.Text, Score: roundScore(pkt.Score)}
		if meta, ok := pkt.Meta.(retrieval.ThemeMeta); ok {
			it.Content = meta.Description
			if meta.StateText != "" {
				it.Description = meta.StateText
			}
		}
		b.Items = append(b.Items, it)
	}
	return &b
}

func buildSummariesBlock(items []retrieval.ContextPacket) Block {
	b := summariesBlock{
		Description: descSummary,
	}
	for _, pkt := range items {
		it := summaryItem{Content: pkt.Text, Score: roundScore(pkt.Score)}
		if meta, ok := pkt.Meta.(retrieval.SummaryMeta); ok {
			it.Content = meta.SummaryText
			it.ID = meta.SummaryID
			it.CoverFrom = meta.CoverFromMsgID
			it.CoverTo = meta.CoverToMsgID
			it.ParentSummaryID = meta.ParentSummaryID
			if meta.Topics != "" {
				it.Topics = meta.Topics
			}
		}
		b.Items = append(b.Items, it)
	}
	return &b
}

func buildCurrentDatetimeBlock(items []retrieval.ContextPacket) Block {
	if len(items) == 0 {
		return nil
	}
	return &currentDatetimeBlock{Content: items[0].Text}
}

// roundScore 将相似度分数保留 4 位小数，避免 XML 中输出过长的浮点字面量。
func roundScore(s float64) float64 {
	return math.Round(s*10000) / 10000
}

var weekdaysCN = [...]string{"星期日", "星期一", "星期二", "星期三", "星期四", "星期五", "星期六"}

// DateTimeText 返回当前时间的格式化中文字符串。
func DateTimeText(t time.Time) string {
	ampm := "上午"
	if t.Hour() >= 12 {
		ampm = "下午"
	}
	return t.Format("2006年1月2日 ") + weekdaysCN[t.Weekday()] + " " + ampm + t.Format("03:04 MST")
}

// DateTimePacket 生成 current_datetime 类型的 ContextPacket。
func DateTimePacket(t time.Time) retrieval.ContextPacket {
	return retrieval.ContextPacket{Type: TypeCurrentDatetime, Text: DateTimeText(t)}
}

// MergePackets 合并附加 packet 与检索结果，按 type 归并。
func MergePackets(retrieveResult *RetrieveResult, extra ...retrieval.ContextPacket) map[string][]retrieval.ContextPacket {
	packets := make(map[string][]retrieval.ContextPacket)
	for _, pkt := range extra {
		packets[pkt.Type] = append(packets[pkt.Type], pkt)
	}
	if retrieveResult != nil {
		for k, v := range retrieveResult.Packets {
			packets[k] = append(packets[k], v...)
		}
	}
	return packets
}

// sortBlocks 按 typeRank 对 blocks 排序。
func sortBlocks(blocks []Block) {
	sort.Slice(blocks, func(i, j int) bool {
		return typeRank[blocks[i].TypeKey()] < typeRank[blocks[j].TypeKey()]
	})
}
