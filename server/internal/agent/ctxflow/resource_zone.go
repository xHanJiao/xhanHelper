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

// DefaultZoneQuotas 默认配额：note 5 / memory 3 / summary 1 / topic 5。
func DefaultZoneQuotas() ZoneQuotas {
	return ZoneQuotas{Summary: 1, Memory: 3, Note: 5, Topic: 5}
}

// ZoneBudgets 资源区每类型字符预算（chars，UTF-8 rune count）。
// 数据依据（近30天实测）：note p75 272×10、memory p75 62×8、summary p75 265×2、
// topic 16×5、profile 块 p90 1407。各类型独立封顶，防止跨类挤占。
type ZoneBudgets struct {
	Profile int
	Summary int
	Memory  int
	Note    int
	Topic   int
}

// DefaultZoneBudgets 默认预算：profile 1600 / note 3000 / summary 800 / memory 600 / topic 200。
func DefaultZoneBudgets() ZoneBudgets {
	return ZoneBudgets{Profile: 1600, Summary: 800, Memory: 600, Note: 3000, Topic: 200}
}

// zoneContainer XML 反解容器：只提取资源类块（profile/memories/notes/summaries/topics/recent），
// history/datetime/notice/user_query 忽略（不进资源区）。
type zoneContainer struct {
	Profile   *profileBlock   `xml:"profile"`
	Memories  *memoriesBlock  `xml:"memories"`
	Notes     *notesBlock     `xml:"notes"`
	Summaries *summariesBlock `xml:"summaries"`
	Topics    *themesBlock    `xml:"topics"`
	Recent    *recentBlock    `xml:"recent"`
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
	if c.Profile != nil {
		for i := range c.Profile.Items {
			it := &c.Profile.Items[i]
			pkts = append(pkts, retrieval.ContextPacket{
				ID:   it.Key,
				Type: TypeProfile,
				Text: it.Value,
				Meta: retrieval.ProfileMeta{
					Key:       it.Key,
					Value:     it.Value,
					Source:    it.Source,
					UpdatedAt: parseZoneDate(it.UpdatedAt),
				},
			})
		}
	}
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
	if c.Recent != nil {
		for i := range c.Recent.Items {
			it := &c.Recent.Items[i]
			pkts = append(pkts, retrieval.ContextPacket{
				ID:   it.ID,
				Type: TypeRecent,
				Text: it.Content,
				Meta: retrieval.RecentMeta{
					Content:   it.Content,
					Role:      it.Role,
					CreatedAt: parseZoneDateTime(it.Date),
				},
			})
		}
	}
	return pkts
}

// parseZoneDate 解析日期属性（"2006年01月02日" 或 "2006-01-02"），失败返回零值。
func parseZoneDate(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{"2006年01月02日", "2006-01-02", "2006-01-02 15:04"} {
		if t, err := time.ParseInLocation(layout, s, config.BJ); err == nil {
			return t
		}
	}
	return time.Time{}
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
	case retrieval.ProfileMeta:
		return meta.Value
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
	case TypeProfile:
		// 画像字段数天然少（8-9 个），不参与条数 quota
		return 1 << 30
	case TypeRecent:
		// recent 为窗口首条注入的固定 3 条，量小且定，不参与条数 quota
		return 1 << 30
	}
	return 0
}

// zoneBudget 返回类型预算；非资源类型返回 0。
func zoneBudget(pkt retrieval.ContextPacket, b ZoneBudgets) int {
	switch pkt.Type {
	case TypeProfile:
		return b.Profile
	case TypeSummary:
		return b.Summary
	case TypeMemory:
		return b.Memory
	case TypeNote:
		return b.Note
	case TypeTheme:
		return b.Topic
	}
	return 0
}

// zoneTypeOrder 资源区类型展示顺序（profile 稳定层最先，时间线阅读）。
var zoneTypeOrder = []string{TypeProfile, TypeSummary, TypeRecent, TypeMemory, TypeNote, TypeTheme}

// MergeResourceZone 旧资源（prev，时间线序）+ 本轮检索（fresh）→ 去重 + quota + 时间线序。
// 规则：
//   - 同 key（ID 优先，旧 XML 降级内容匹配）只保留一份；fresh 中的新版本替换 prev 中旧版本
//     （保留原位置，时间线稳定），全新的追加到尾部
//   - 每类型超过 quota 时从头部（最旧）丢弃（profile 不参与 quota）
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

	// quota：每类型从头部砍到上限（profile quota 极大，不砍）
	preQuota := flattenGroups(groups)
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
		default:
			continue // profile 不参与 quota
		}
		if len(list) > quota {
			groups[typ] = list[len(list)-quota:]
		}
	}
	zoneTrimLog("quota", preQuota, flattenGroups(groups))

	// 按 zoneTypeOrder 展平
	var out []retrieval.ContextPacket
	for _, typ := range zoneTypeOrder {
		out = append(out, groups[typ]...)
	}
	return out
}

// FormatResourceZone 编码资源区：按类型分组（每类型一个块），保持类型顺序 zoneTypeOrder。
// 组内保持时间线序，不按 score 重排。
func FormatResourceZone(pkts []retrieval.ContextPacket) string {
	groups := make(map[string][]retrieval.ContextPacket)
	for _, pkt := range pkts {
		groups[pkt.Type] = append(groups[pkt.Type], pkt)
	}

	var buf bytes.Buffer
	enc := XMLEncoder(&buf)
	enc.SetIndent("", "  ")
	for _, typ := range zoneTypeOrder {
		group := groups[typ]
		if len(group) == 0 {
			continue
		}
		if blk := buildBlock(typ, group); blk != nil {
			if err := enc.Encode(blk); err != nil {
				log.Printf("[resource_zone] encode %s error: %v", typ, err)
			}
		}
	}
	enc.Flush()
	return buf.String()
}

// budgetTypePriority 总预算兜底砍序：memory > note > theme > summary > profile
// （memory 数量多价值密度低先砍，profile 稳定画像最后砍）。
var budgetTypePriority = []string{TypeMemory, TypeNote, TypeTheme, TypeSummary, TypeProfile}

// BudgetResourceZone 两阶段预算：
//  1. per-type：每类型独立从最旧砍到该类预算内（防止跨类挤占，notes 满不伤 summary/profile）
//  2. 总预算兜底：仍超 totalChars 时按 budgetTypePriority 从最旧砍
//
// 输入 pkts 需为 MergeResourceZone 输出（类型分组、组内时间线序）。
// 预算单位：UTF-8 字符数（rune count，中文 1 字 = 1）。
func BudgetResourceZone(pkts []retrieval.ContextPacket, budgets ZoneBudgets, totalChars int) (out []retrieval.ContextPacket) {
	if len(pkts) == 0 {
		return pkts
	}
	before := pkts
	defer func() { zoneTrimLog("budget", before, out) }()
	// 阶段 1：per-type 截断
	pkts = budgetPerType(pkts, budgets)
	// 阶段 2：总预算兜底
	if totalChars <= 0 {
		return pkts
	}
	for {
		if utf8.RuneCountInString(FormatResourceZone(pkts)) <= totalChars {
			return pkts
		}
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
		pkts = dropFirstOfType(pkts, victim)
	}
}

// budgetPerType 每类型独立截断：从该类型最旧开始砍，直到该类编码字符数 ≤ 预算。
// 每类型的块单独编码估算（group 连续，块大小即该类总量）。
func budgetPerType(pkts []retrieval.ContextPacket, budgets ZoneBudgets) []retrieval.ContextPacket {
	groups := make(map[string][]retrieval.ContextPacket)
	var order []string
	for _, p := range pkts {
		if _, ok := groups[p.Type]; !ok {
			order = append(order, p.Type)
		}
		groups[p.Type] = append(groups[p.Type], p)
	}
	for _, typ := range order {
		budget := zoneBudgetOf(typ, budgets)
		if budget <= 0 {
			continue
		}
		list := groups[typ]
		for {
			if len(list) == 0 {
				break
			}
			if utf8.RuneCountInString(FormatResourceZone(list)) <= budget {
				break
			}
			list = list[1:] // 砍最旧
		}
		groups[typ] = list
	}
	// 按原 pkts 顺序重建（zoneTypeOrder 展平已由上层保证，这里保持原序）
	var out []retrieval.ContextPacket
	for _, typ := range zoneTypeOrder {
		out = append(out, groups[typ]...)
	}
	return out
}

func zoneBudgetOf(typ string, b ZoneBudgets) int {
	switch typ {
	case TypeProfile:
		return b.Profile
	case TypeSummary:
		return b.Summary
	case TypeMemory:
		return b.Memory
	case TypeNote:
		return b.Note
	case TypeTheme:
		return b.Topic
	}
	return 0
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

// flattenGroups 按 zoneTypeOrder 展平类型分组（MergeResourceZone 内部快照用）。
func flattenGroups(groups map[string][]retrieval.ContextPacket) []retrieval.ContextPacket {
	var out []retrieval.ContextPacket
	for _, typ := range zoneTypeOrder {
		out = append(out, groups[typ]...)
	}
	return out
}

// zoneCharStats 统计资源区各子区（按类型）编码字符数（rune），与裁剪计量口径一致。
func zoneCharStats(pkts []retrieval.ContextPacket) map[string]int {
	stats := make(map[string]int)
	groups := make(map[string][]retrieval.ContextPacket)
	for _, p := range pkts {
		groups[p.Type] = append(groups[p.Type], p)
	}
	for typ, group := range groups {
		stats[typ] = utf8.RuneCountInString(FormatResourceZone(group))
	}
	return stats
}

// zoneShortKey 日志友好的条目标识：优先 ID，无 ID 的内容 key 截断到 40 字。
func zoneShortKey(pkt retrieval.ContextPacket) string {
	if pkt.ID != "" {
		return pkt.Type + ":" + pkt.ID
	}
	k := zoneContentKey(pkt)
	if r := []rune(k); len(r) > 40 {
		return string(r[:40]) + "…"
	}
	return k
}

// zoneTrimLog 输出一次裁剪日志：总字符与各子区字符的前后变化、选中条目、淘汰条目。
// phase 区分阶段（quota 条数帽 / budget 字符帽）。
func zoneTrimLog(phase string, before, after []retrieval.ContextPacket) {
	if len(before) == 0 && len(after) == 0 {
		return
	}
	bStats, aStats := zoneCharStats(before), zoneCharStats(after)
	bTotal, aTotal := 0, 0
	for _, n := range bStats {
		bTotal += n
	}
	for _, n := range aStats {
		aTotal += n
	}
	selected := make([]string, 0, len(after))
	for _, p := range after {
		selected = append(selected, zoneShortKey(p))
	}
	afterSet := make(map[string]bool, len(after))
	for _, p := range after {
		afterSet[zoneKey(p)] = true
	}
	var dropped []string
	for _, p := range before {
		if !afterSet[zoneKey(p)] {
			dropped = append(dropped, zoneShortKey(p))
		}
	}
	log.Printf("[resource_zone] %s trim: total %d→%d chars; per-zone %v→%v; selected=%v; dropped=%v",
		phase, bTotal, aTotal, bStats, aStats, selected, dropped)
}
