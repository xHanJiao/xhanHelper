package ctxflow

import (
	"math"
	"sort"
	"time"

	"xhan-helper/internal/agent/retrieval"
)

const (
	TypeSummary         = "summary"
	TypeReminder        = "reminder"
	TypeHistory         = "history"
	TypeMemory          = "memory"
	TypeNote            = "note"
	TypeTheme           = "theme"
	TypeCurrentDatetime = "current_datetime"
)

// typeOrder 定义所有块的渲染顺序，也是 typeRank 的单一数据源。
var typeOrder = []string{TypeSummary, TypeReminder, TypeHistory, TypeMemory, TypeNote, TypeTheme, TypeCurrentDatetime}

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
	ID          string  `xml:"id,attr" json:"id"`
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

// --- reminder ---
type reminderItem struct {
	ID      string `xml:"id,attr" json:"id"`
	Content string `xml:",chardata" json:"content"`
}

type remindersBlock struct {
	XMLName     struct{}       `xml:"reminders" json:"-"`
	Description string         `xml:"description,attr" json:"description"`
	Items       []reminderItem `xml:"reminder" json:"items"`
}

func (*remindersBlock) TypeKey() string { return TypeReminder }

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
	TypeReminder:        buildRemindersBlock,
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
		Description: retrieval.DescMemory,
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
		Description: retrieval.DescNotes,
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
		Description: retrieval.DescHistory,
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
		Description: retrieval.DescThemes,
	}
	for _, pkt := range items {
		it := themeItem{ID: pkt.ID, Content: pkt.Text, Score: roundScore(pkt.Score)}
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
		Description: retrieval.DescSummary,
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

func buildRemindersBlock(items []retrieval.ContextPacket) Block {
	b := remindersBlock{
		Description: retrieval.DescReminder,
	}
	for _, pkt := range items {
		b.Items = append(b.Items, reminderItem{ID: pkt.ID, Content: pkt.Text})
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
