package ctxflow

import (
	"bytes"
	"encoding/xml"
	"log"
	"strings"
	"time"
	"unicode/utf8"

	"xhan-helper/internal/agent/retrieval"
	"xhan-helper/internal/config"
)

// ==================== 公共资源区 ====================
// 背景：历史 user 消息不再重放完整 XMLContext（含当时注入的检索资源），
// 注入资源收敛到"最新消息内的公共资源区"——历史累计资源 + 本轮检索结果，
// 消除历史重复放大（窗口 6 轮 ≈ 34k tokens/轮 的重复注入）。
//
// 增量式构建：只解析上一条消息的 XMLContext（零额外 DB 读），
// 与本轮检索 merge 后写入当前消息。资源区为 LLM 的显式工作记忆。

// ZoneQuotas 资源区每类型条目上限（数据驱动，见 phase-10 文档）。
type ZoneQuotas struct {
	Summary int
	Memory  int
	Note    int
	Topic   int
}

// DefaultZoneQuotas 默认配额：note 10 / memory 8 / summary 2 / topic 5（只收 matched）。
func DefaultZoneQuotas() ZoneQuotas {
	return ZoneQuotas{Summary: 2, Memory: 8, Note: 10, Topic: 5}
}

// zoneContainer XML 反解容器：只提取资源类块（memories/notes/summaries/topics），
// history/profile/reminders/datetime/notice/user_query 忽略（不进资源区）。
type zoneContainer struct {
	Memories  *memoriesBlock  `xml:"memories"`
	Notes     *notesBlock     `xml:"notes"`
	Summaries *summariesBlock `xml:"summaries"`
	Topics    *themesBlock    `xml:"topics"`
}

// ParseResourceZone 从历史 XMLContext 提取资源类块，还原为 packets（保持块内原有顺序）。
// 返回 nil 表示无资源或解析失败（解析失败打日志降级，不允许静默）。
func ParseResourceZone(xmlText string) []retrieval.ContextPacket {
	if strings.TrimSpace(xmlText) == "" {
		return nil
	}
	var c zoneContainer
	// encoding/xml 对多顶层元素只解析第一个（其余静默忽略），需包一层假根。
	if err := xml.Unmarshal([]byte("<zone>"+xmlText+"</zone>"), &c); err != nil {
		log.Printf("[resource_zone] parse previous XMLContext error: %v", err)
		return nil
	}

	var pkts []retrieval.ContextPacket
	if c.Memories != nil {
		for i := range c.Memories.Items {
			it := &c.Memories.Items[i]
			pkts = append(pkts, retrieval.ContextPacket{
				ID:    it.ID,
				Type:  TypeMemory,
				Text:  it.Content,
				Score: it.Score,
				Meta: retrieval.EntityMeta{
					Content:   it.Content,
					Topic:     it.Topic,
					CreatedAt: parseZoneDate(it.Date),
				},
			})
		}
	}
	if c.Notes != nil {
		for i := range c.Notes.Items {
			it := &c.Notes.Items[i]
			pkts = append(pkts, retrieval.ContextPacket{
				ID:    it.ID,
				Type:  TypeNote,
				Text:  it.Content,
				Score: it.Score,
				Meta: retrieval.EntityMeta{
					Content:   it.Content,
					Topic:     it.Topic,
					CreatedAt: parseZoneDate(it.Date),
				},
			})
		}
	}
	if c.Summaries != nil {
		for i := range c.Summaries.Items {
			it := &c.Summaries.Items[i]
			pkts = append(pkts, retrieval.ContextPacket{
				ID:    it.ID,
				Type:  TypeSummary,
				Text:  it.Content,
				Score: it.Score,
				Meta: retrieval.SummaryMeta{
					SummaryID:       it.ID,
					SummaryText:     it.Content,
					CoverFromMsgID:  it.CoverFrom,
					CoverToMsgID:    it.CoverTo,
					ParentSummaryID: it.ParentSummaryID,
					Topics:          it.Topics,
					CreatedAt:       parseZoneDateTime(it.CreatedAt),
				},
			})
		}
	}
	if c.Topics != nil {
		for i := range c.Topics.Items {
			it := &c.Topics.Items[i]
			pkts = append(pkts, retrieval.ContextPacket{
				ID:    it.ID,
				Type:  TypeTheme,
				Text:  it.Content,
				Score: it.Score,
				Meta: retrieval.ThemeMeta{
					Description: it.Content,     // XML: content = 主题描述
					StateText:   it.Description, // XML: desc attr = 状态文本（matched 才有）
				},
			})
		}
	}
	return pkts
}

// parseZoneDate 解析 memory/note 的日期属性（格式 "2006年01月02日"），失败返回零值。
func parseZoneDate(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.ParseInLocation("2006年01月02日", s, config.BJ)
	if err != nil {
		return time.Time{}
	}
	return t
}

// parseZoneDateTime 解析 summary 的 created_at（格式 "2006-01-02 15:04"，BJ），失败返回零值。
func parseZoneDateTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{"2006-01-02 15:04", "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, s, config.BJ); err == nil {
			return t
		}
	}
	return time.Time{}
}

// zoneKey 生成去重 key：优先 ID；旧 XML 无 id 的 memory/note 用 topic+content 精确匹配。
func zoneKey(pkt retrieval.ContextPacket) string {
	if pkt.ID != "" {
		return pkt.Type + ":id:" + pkt.ID
	}
	return pkt.Type + ":content:" + strings.TrimSpace(zoneContent(pkt))
}

// zoneContentKey 内容级去重 key（ID 与内容双索引：旧 XML 无 id 的资源后续被带 id 的新版命中）。
func zoneContentKey(pkt retrieval.ContextPacket) string {
	return pkt.Type + ":content:" + strings.TrimSpace(zoneContent(pkt))
}

// zoneContent 取 packet 的正文（Meta 优先，Text 兜底）。
func zoneContent(pkt retrieval.ContextPacket) string {
	switch meta := pkt.Meta.(type) {
	case retrieval.EntityMeta:
		return meta.Content
	case retrieval.SummaryMeta:
		return meta.SummaryText
	case retrieval.ThemeMeta:
		return meta.Description
	}
	return pkt.Text
}

// zoneQuota 返回类型配额；类型不进资源区返回 0。
func zoneQuota(pkt retrieval.ContextPacket, q ZoneQuotas) int {
	switch pkt.Type {
	case TypeSummary:
		return q.Summary
	case TypeMemory:
		return q.Memory
	case TypeNote:
		return q.Note
	case TypeTheme:
		return q.Topic
	}
	return 0
}

// zoneTypeOrder 资源区类型展示顺序（summary 最早，时间线阅读）。
var zoneTypeOrder = []string{TypeSummary, TypeMemory, TypeNote, TypeTheme}

// MergeResourceZone 旧资源（prev，时间线序）+ 本轮检索（fresh）→ 去重 + quota + 时间线序。
// 规则：
//   - 同 key（ID 优先，旧 XML 降级内容匹配）只保留一份；fresh 中的新版本替换 prev 中旧版本
//     （保留原位置，时间线稳定），全新的追加到尾部
//   - 每类型超过 quota 时从头部（最旧）丢弃
//   - 输出按类型分组（同类型连续，组内旧→新），类型顺序 zoneTypeOrder——LLM 阅读最自然
func MergeResourceZone(prev, fresh []retrieval.ContextPacket, q ZoneQuotas) []retrieval.ContextPacket {
	if q.Summary == 0 && q.Memory == 0 && q.Note == 0 && q.Topic == 0 {
		q = DefaultZoneQuotas()
	}

	// 每类型维护有序列表 + 双索引（ID key / 内容 key → 组内下标）
	groups := make(map[string][]retrieval.ContextPacket)
	posID := make(map[string]int)      // id key → 组内下标
	posContent := make(map[string]int) // content key → 组内下标
	add := func(pkt retrieval.ContextPacket) {
		idx := len(groups[pkt.Type])
		groups[pkt.Type] = append(groups[pkt.Type], pkt)
		if pkt.ID != "" {
			posID[zoneKey(pkt)] = idx
		}
		posContent[zoneContentKey(pkt)] = idx
	}
	replace := func(pkt retrieval.ContextPacket, idx int) {
		old := groups[pkt.Type][idx]
		// 清理旧索引，防止残留 key 把"同内容不同 ID"的新资源误定位到本位置（数据丢失）
		if old.ID != "" {
			delete(posID, zoneKey(old))
		}
		delete(posContent, zoneContentKey(old))
		groups[pkt.Type][idx] = pkt
		if pkt.ID != "" {
			posID[zoneKey(pkt)] = idx
		}
		posContent[zoneContentKey(pkt)] = idx
	}

	for _, pkt := range prev {
		if zoneQuota(pkt, q) <= 0 {
			continue
		}
		if _, ok := posID[zoneKey(pkt)]; ok {
			continue
		}
		if _, ok := posContent[zoneContentKey(pkt)]; ok {
			continue
		}
		add(pkt)
	}
	for _, pkt := range fresh {
		if zoneQuota(pkt, q) <= 0 {
			continue
		}
		// 先按 ID 匹配，再按内容匹配（旧 XML 无 id 资源的升级命中）
		if idx, ok := posID[zoneKey(pkt)]; ok {
			replace(pkt, idx)
			continue
		}
		if idx, ok := posContent[zoneContentKey(pkt)]; ok {
			replace(pkt, idx) // 内容更新（版本新），位置不变
			continue
		}
		add(pkt)
	}

	// quota：每类型从头部砍到上限
	for typ, list := range groups {
		quota := 0
		switch typ {
		case TypeSummary:
			quota = q.Summary
		case TypeMemory:
			quota = q.Memory
		case TypeNote:
			quota = q.Note
		case TypeTheme:
			quota = q.Topic
		}
		if len(list) > quota {
			groups[typ] = list[len(list)-quota:]
		}
	}

	// 按 zoneTypeOrder 展平
	var out []retrieval.ContextPacket
	for _, typ := range zoneTypeOrder {
		out = append(out, groups[typ]...)
	}
	return out
}

// FormatResourceZone 编码资源区（保持时间线序，不按 score 重排）。
func FormatResourceZone(pkts []retrieval.ContextPacket) string {
	var buf bytes.Buffer
	enc := XMLEncoder(&buf)
	enc.SetIndent("", "  ")
	for _, pkt := range pkts {
		if blk := buildBlock(pkt.Type, []retrieval.ContextPacket{pkt}); blk != nil {
			if err := enc.Encode(blk); err != nil {
				log.Printf("[resource_zone] encode %s error: %v", pkt.Type, err)
			}
		}
	}
	enc.Flush()
	return buf.String()
}

// budgetTypePriority 预算帽砍序：memory > note > theme > summary
// （memory 数量多价值密度低先砍，summary 浓缩价值最高最后砍）。
var budgetTypePriority = []string{TypeMemory, TypeNote, TypeTheme, TypeSummary}

// BudgetResourceZone 预算帽：编码后字符数（rune count，中文 1 字 = 1）超限时，
// 按类型优先级从最旧砍直到达标。预算单位与调研数据口径一致（quota 后 median 3.1k / p90 4.5k 字符）。
// 输入 pkts 需为 MergeResourceZone 输出（类型分组、组内时间线序）。
func BudgetResourceZone(pkts []retrieval.ContextPacket, maxChars int) []retrieval.ContextPacket {
	if maxChars <= 0 || len(pkts) == 0 {
		return pkts
	}
	for {
		if utf8.RuneCountInString(FormatResourceZone(pkts)) <= maxChars {
			return pkts
		}
		// 找优先级最高且非空的类型
		var victim string
		for _, typ := range budgetTypePriority {
			if hasType(pkts, typ) {
				victim = typ
				break
			}
		}
		if victim == "" {
			return pkts // 无可砍，接受超限（理论不应发生）
		}
		// 砍该类型最旧一条（该类型在 pkts 中的第一条）
		pkts = dropFirstOfType(pkts, victim)
	}
}

func hasType(pkts []retrieval.ContextPacket, typ string) bool {
	for _, p := range pkts {
		if p.Type == typ {
			return true
		}
	}
	return false
}

func dropFirstOfType(pkts []retrieval.ContextPacket, typ string) []retrieval.ContextPacket {
	for i, p := range pkts {
		if p.Type == typ {
			return append(pkts[:i], pkts[i+1:]...)
		}
	}
	return pkts
}
