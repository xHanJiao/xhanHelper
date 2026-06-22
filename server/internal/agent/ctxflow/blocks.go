package ctxflow

import "xhan-helper/internal/agent/retrieval"

// typeOrder 定义检索类型在 user 消息中的渲染顺序。
var typeOrder = []string{"summary", "history", "memory", "note", "theme"}

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

// --- theme ---
type themeItem struct {
	Description string `xml:"desc,attr,omitempty" json:"description,omitempty"`
	Content     string `xml:",chardata" json:"content"`
}
type themesBlock struct {
	XMLName struct{}    `xml:"topics" json:"-"`
	Items   []themeItem `xml:"topic" json:"items"`
}

// --- summary ---
type summaryItem struct {
	Topics  string `xml:"topics,attr,omitempty" json:"topics,omitempty"`
	Content string `xml:",chardata" json:"content"`
}
type summariesBlock struct {
	XMLName struct{}      `xml:"summaries" json:"-"`
	Items   []summaryItem `xml:"summary" json:"items"`
}

// --- user_query ---
type userQueryBlock struct {
	XMLName struct{} `xml:"user_query" json:"-"`
	Content string   `xml:",chardata" json:"content"`
}

// ==================== 构建 ====================

// buildBlock 将某 type 的 packets 构建为可被 xml/json encoder 序列化的元素。
// 返回 nil 表示无有效内容（如 history 的 messages 全为空）。
func buildBlock(typ string, items []retrieval.ContextPacket) any {
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
	}
	return nil
}
