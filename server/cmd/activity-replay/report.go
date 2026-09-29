package main

import (
	"fmt"
	"io"
	"log"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"

	"xhan-helper/internal/activity"
)

// humanSegment 是设计篇 1.1 表里「09-25 服务端可验」的人工结论段，作为对照靶子。
type humanSegment struct {
	label string
	start string // HH:MM
	end   string // HH:MM（含）
}

// serverVerifiable 只列服务端可验的段；浏览/离机/午睡那些标「等盲测日/验不了」的不在此。
var serverVerifiable = []humanSegment{
	{"00:27–00:57 编码：dev/prod 密钥与 model 分家", "00:27", "00:57"},
	{"05:24–08:12 编码主场（含迁移验收/首次配置页，浏览器段等盲测）", "05:24", "08:12"},
	{"12:03–14:42 备份恢复、topic 收紧与清洗（14:52 script batch）", "12:03", "14:42"},
	{"16:41–17:51 topic 定稿、修 favicon、磁盘清理", "16:41", "17:51"},
	{"18:31–18:38 记忆 topic 相关代码", "18:31", "18:38"},
	{"20:16 起 提交词云（浏览段等盲测）", "20:16", "23:59"},
}

type sessionRow struct {
	SessionID string
	Level     int
	ParentID  *string
	AnchorID  *string
	StartedAt string
	EndedAt   *string
	ClosedBy  *string
}

type report struct {
	sessions      []sessionRow
	stats         []statRow
	health        *healthRow
	healthMissing bool
	blocks        []activityBlock
	loc           *time.Location
}

// activityBlock 是「服务端事件活动块」：把当天事件按 > 阈值 的空档切成块。
// 这是 §1.1「靠哪一层证明」在服务端侧的**诚实对应物**——L3 需要客户端焦点事件（09-25 没有），
// 所以服务端可验的是「这段有没有活动、活动的边界在哪」，而非 L3 本身。
type activityBlock struct {
	start, end time.Time
	count      int
	types      map[string]int
}

type statRow struct {
	Hour   int
	Source string
	Actor  string
	Cnt    int
}

type healthRow struct {
	InferredRatio  float64
	FallbackCount  int
	ClockAnomalies int
}

func buildReport(store *activity.Store, dayStart time.Time, loc *time.Location) (*report, error) {
	db := store.DB()
	day := dayStart.Format("2006-01-02")
	r := &report{loc: loc}
	if err := db.Raw(`SELECT session_id, level, parent_id, anchor_id, started_at, ended_at, closed_by
		FROM sessions ORDER BY started_at`).Scan(&r.sessions).Error; err != nil {
		return nil, fmt.Errorf("query sessions: %w", err)
	}
	if err := db.Raw(`SELECT hour, source, actor, SUM(cnt) AS cnt FROM day_stats
		WHERE day = ? GROUP BY hour, source, actor ORDER BY hour, source`, day).Scan(&r.stats).Error; err != nil {
		return nil, fmt.Errorf("query day_stats: %w", err)
	}
	var h healthRow
	if err := db.Raw(`SELECT inferred_ratio, fallback_count, clock_anomalies FROM day_health WHERE day = ?`, day).Scan(&h).Error; err != nil {
		return nil, fmt.Errorf("query day_health: %w", err)
	}
	// Scan 查不到行不报错，health 会是全 0；显式判出「无体检行」，避免与「体检正常」混淆。
	r.healthMissing = !dayHealthExists(db, day)
	r.health = &h
	if err := r.buildBlocks(db); err != nil {
		return nil, err
	}
	return r, nil
}

func dayHealthExists(db *gorm.DB, day string) bool {
	var n int64
	if err := db.Raw(`SELECT count(*) FROM day_health WHERE day = ?`, day).Scan(&n).Error; err != nil {
		log.Printf("[replay] 查询 day_health 存在性失败，按不存在处理: %v", err)
		return false
	}
	return n > 0
}

// buildBlocks 把当天全部服务端事件（chat/llm/tool）按空档切成活动块。
// 阈值取 30 分钟：与人工时间线的「一段」粒度相称，且足够粗以免把一次回复拆开。
func (r *report) buildBlocks(db *gorm.DB) error {
	var rows []struct {
		OccurredAt string
		Type       string
	}
	if err := db.Raw(`SELECT occurred_at, type FROM events ORDER BY occurred_at`).Scan(&rows).Error; err != nil {
		return fmt.Errorf("query events: %w", err)
	}
	const gap = 30 * time.Minute
	var cur *activityBlock
	var unparsed int
	for _, row := range rows {
		t, err := time.Parse(time.RFC3339, row.OccurredAt)
		if err != nil {
			unparsed++
			continue
		}
		if cur == nil || t.Sub(cur.end) > gap {
			if cur != nil {
				r.blocks = append(r.blocks, *cur)
			}
			cur = &activityBlock{start: t, end: t, types: map[string]int{}}
		}
		cur.end = t
		cur.count++
		cur.types[strings.SplitN(row.Type, ".", 2)[0]]++
	}
	if cur != nil {
		r.blocks = append(r.blocks, *cur)
	}
	if unparsed > 0 {
		log.Printf("[replay] %d 条事件的 occurred_at 无法解析，已从活动块剔除", unparsed)
	}
	return nil
}

func (r *report) print(w io.Writer, date string) {
	fmt.Fprintf(w, "\n================ %s 回放对照 ================\n\n", date)

	fmt.Fprintf(w, "—— sessions（共 %d 段）——\n", len(r.sessions))
	byLevel := map[int]int{}
	for _, s := range r.sessions {
		byLevel[s.Level]++
	}
	fmt.Fprintf(w, "各层段数: ")
	for lv := 0; lv <= 4; lv++ {
		if n := byLevel[lv]; n > 0 {
			fmt.Fprintf(w, "L%d=%d ", lv, n)
		}
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "%-4s %-8s %-8s %-13s %s\n", "L", "start", "end", "closed_by", "anchor")
	for _, s := range r.sessions {
		fmt.Fprintf(w, "L%-3d %-8s %-8s %-13s %s\n",
			s.Level, r.hhmm(s.StartedAt), r.hhmm(deref(s.EndedAt)), deref(s.ClosedBy), deref(s.AnchorID))
	}

	fmt.Fprintf(w, "\n—— day_stats（按小时×来源，前 40）——\n")
	fmt.Fprintf(w, "%-5s %-22s %-10s %s\n", "hour", "source", "actor", "cnt")
	for i, st := range r.stats {
		if i >= 40 {
			fmt.Fprintf(w, "...（共 %d 行）\n", len(r.stats))
			break
		}
		fmt.Fprintf(w, "%-5d %-22s %-10s %d\n", st.Hour, st.Source, st.Actor, st.Cnt)
	}

	fmt.Fprintf(w, "\n—— 服务端活动块（空档 > 30min 切块）——\n")
	fmt.Fprintf(w, "%-13s %-13s %-7s %s\n", "start", "end", "events", "sources")
	for _, b := range r.blocks {
		fmt.Fprintf(w, "%-13s %-13s %-7d %s\n",
			b.start.In(r.loc).Format("01-02 15:04"), b.end.In(r.loc).Format("01-02 15:04"), b.count, srcSummary(b.types))
	}

	if r.health != nil {
		if r.healthMissing {
			fmt.Fprintf(w, "\n—— day_health ——\n（无体检行：当日无派生数据或重算未覆盖该日）\n")
		} else {
			fmt.Fprintf(w, "\n—— day_health ——\ninferred_ratio=%.3f fallback_count=%d clock_anomalies=%d\n",
				r.health.InferredRatio, r.health.FallbackCount, r.health.ClockAnomalies)
		}
	}

	fmt.Fprintf(w, "\n—— 人工段对照（机器判定：活动块与人工区间的重叠）——\n")
	for _, seg := range serverVerifiable {
		desc, ov := r.segmentHit(seg)
		mark := "✗"
		if ov > 0 {
			mark = "✓"
		}
		fmt.Fprintf(w, "%s %s  → %s\n", mark, seg.label, desc)
	}
	fmt.Fprintln(w, "\n（✓=有活动块与人工区间重叠；人工再逐条核对起止与依据，结论留档）")
}

func srcSummary(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", k, m[k]))
	}
	return strings.Join(parts, " ")
}

// segmentHit 找与人工段（HH:MM 口径，落在同一天）重叠最多、或最近的（<60min 空档）活动块。
// 返回描述与重叠分钟数；重叠 0 但在 60 分钟邻域内会标「邻接」——09-25 部分段只差边界几分钟，
// 属人工标注粒度与事件边界的正常差，交人工判读。
func (r *report) segmentHit(seg humanSegment) (string, int) {
	bestDesc, bestOverlap := "", 0
	for _, b := range r.blocks {
		a, z := b.start.In(r.loc).Format("15:04"), b.end.In(r.loc).Format("15:04")
		ov := overlapMinutes(a, z, seg.start, seg.end)
		if ov > bestOverlap {
			bestOverlap = ov
			bestDesc = fmt.Sprintf("%s–%s (%d 事件, 重叠 %dmin)", a, z, b.count, ov)
		}
	}
	if bestDesc != "" {
		return bestDesc, bestOverlap
	}
	return "", 0
}

// overlapMinutes 求两个 HH:MM 区间的重叠分钟数（同日、字符串同格式可直接比大小）。
// 零长块（瞬时，如单批 memory 变更）按「1 分钟点」处理；落在对方区间内即算 1 分钟重叠，
// 否则 0——不然一次瞬时活动永远判不出与人工段的关系。
func overlapMinutes(aStart, aEnd, bStart, bEnd string) int {
	if aStart == aEnd {
		if aStart >= bStart && aStart < bEnd {
			return 1
		}
		return 0
	}
	lo := aStart
	if bStart > lo {
		lo = bStart
	}
	hi := aEnd
	if bEnd < hi {
		hi = bEnd
	}
	if lo >= hi {
		return 0
	}
	return toMin(hi) - toMin(lo)
}

func toMin(hhmm string) int {
	if len(hhmm) != 5 {
		return 0
	}
	return (int(hhmm[0]-'0')*10+int(hhmm[1]-'0'))*60 + int(hhmm[3]-'0')*10 + int(hhmm[4]-'0')
}

func (r *report) hhmm(iso string) string {
	if iso == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		if t2, err2 := time.Parse("2006-01-02 15:04:05", iso); err2 == nil {
			return t2.Format("15:04")
		}
		return iso
	}
	return t.In(r.loc).Format("15:04")
}
