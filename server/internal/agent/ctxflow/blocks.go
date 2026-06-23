package ctxflow

import (
	"sort"

	"xhan-helper/internal/agent/retrieval"
)

// typeOrder 定义所有块的渲染顺序，也是 typeRank 的单一数据源。
var typeOrder = []string{"summary", "history", "memory", "note", "theme", "current_datetime"}

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
	Topic   string `xml:"topic,attr,omitempty" json:"topic,omitempty"`
	Date    string `xml:"date,attr,omitempty" json:"date,omitempty"`
	Content string `xml:",chardata" json:"content"`
}
type memoriesBlock struct {
	XMLName struct{}     `xml:"memories" json:"-"`
	Items   []memoryItem `xml:"memory" json:"items"`
}

func (*memoriesBlock) TypeKey() string { return "memory" }

// --- note ---
type noteItem struct {
	Topic   string `xml:"topic,attr,omitempty" json:"topic,omitempty"`
	Date    string `xml:"date,attr,omitempty" json:"date,omitempty"`
	Content string `xml:",chardata" json:"content"`
}
type notesBlock struct {
	XMLName struct{}   `xml:"notes" json:"-"`
	Items   []noteItem `xml:"note" json:"items"`
}

func (*notesBlock) TypeKey() string { return "note" }

// --- history ---
type historyMsg struct {
	Role    string `xml:"role,attr" json:"role"`
	Content string `xml:",chardata" json:"content"`
}
type historyRound struct {
	Messages []historyMsg `xml:"message" json:"messages"`
	Date     string       `xml:"date,attr,omitempty" json:"date,omitempty"`
}
type historyBlock struct {
	XMLName struct{}       `xml:"history" json:"-"`
	Rounds  []historyRound `xml:"round" json:"rounds"`
}

func (*historyBlock) TypeKey() string { return "history" }

// --- theme ---
type themeItem struct {
	Description string `xml:"desc,attr,omitempty" json:"description,omitempty"`
	Content     string `xml:",chardata" json:"content"`
}
type themesBlock struct {
	XMLName struct{}    `xml:"topics" json:"-"`
	Items   []themeItem `xml:"topic" json:"items"`
}

func (*themesBlock) TypeKey() string { return "theme" }

// --- summary ---
type summaryItem struct {
	Topics  string `xml:"topics,attr,omitempty" json:"topics,omitempty"`
	Content string `xml:",chardata" json:"content"`
}
type summariesBlock struct {
	XMLName struct{}      `xml:"summaries" json:"-"`
	Items   []summaryItem `xml:"summary" json:"items"`
}

func (*summariesBlock) TypeKey() string { return "summary" }

// --- current_datetime ---
type currentDatetimeBlock struct {
	XMLName struct{} `xml:"current_datetime" json:"-"`
	Content string   `xml:",chardata"`
}

func (*currentDatetimeBlock) TypeKey() string { return "current_datetime" }

// --- user_query ---
type userQueryBlock struct {
	XMLName struct{} `xml:"user_query" json:"-"`
	Content string   `xml:",chardata" json:"content"`
}

// ==================== 构建 ====================

// buildBlock 将某 type 的 packets 构建为可序列化的 Block。
// 返回 nil 表示无有效内容（如 history 的 messages 全为空）。
func buildBlock(typ string, items []retrieval.ContextPacket) Block {
	switch typ {
	case "memory":
		b := memoriesBlock{}
		for _, pkt := range items {
			it := memoryItem{Content: pkt.Text}
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
	case "note":
		b := notesBlock{}
		for _, pkt := range items {
			it := noteItem{Content: pkt.Text}
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
	case "history":
		b := historyBlock{}
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
			b.Rounds = append(b.Rounds, historyRound{Messages: msgs, Date: dateStr})
		}
		if len(b.Rounds) == 0 {
			return nil
		}
		return &b
	case "theme":
		b := themesBlock{}
		for _, pkt := range items {
			it := themeItem{Content: pkt.Text}
			if meta, ok := pkt.Meta.(retrieval.ThemeMeta); ok {
				it.Content = meta.Description
				if meta.StateText != "" {
					it.Description = meta.StateText
				}
			}
			b.Items = append(b.Items, it)
		}
		return &b
	case "summary":
		b := summariesBlock{}
		for _, pkt := range items {
			it := summaryItem{Content: pkt.Text}
			if meta, ok := pkt.Meta.(retrieval.SummaryMeta); ok {
				it.Content = meta.SummaryText
				if meta.Topics != "" {
					it.Topics = meta.Topics
				}
			}
			b.Items = append(b.Items, it)
		}
		return &b
	case "current_datetime":
		if len(items) == 0 {
			return nil
		}
		return &currentDatetimeBlock{Content: items[0].Text}
	}
	return nil
}

// sortBlocks 按 typeRank 对 blocks 排序。
func sortBlocks(blocks []Block) {
	sort.Slice(blocks, func(i, j int) bool {
		return typeRank[blocks[i].TypeKey()] < typeRank[blocks[j].TypeKey()]
	})
}
