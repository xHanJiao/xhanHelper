package activity

import (
	"fmt"
	"log"
	"sort"
	"strconv"
	"time"

	"gorm.io/gorm"
)

// SessionThresholdVersion 是五层在场派生的阈值版本号；任一在场阈值变化时 +1。
// 过期判定看 sessions.threshold_version（附表 B），变更触发重算（设计篇 6.5）。
const SessionThresholdVersion = 1

// 在场阈值（设计篇 6.2 的三个尺度）。L3 去抖上线初值 0，等于不防抖（L3==L2），
// 收集真实切出分布后再校准。
const (
	sessionFocusDebounce = 0 // 焦点防抖（L3），当前唯一被消费的阈值
	// 预留（登记进 threshold_version，暂无消费方）：将来"注意力断没断/坐下来的段结束没"若要影响
	// closePresence 的闭合，接上这两个即可——接上前请勿当死代码删除。
	sessionAttentionGap = 5 * time.Minute  // 预留：注意力断没断
	sessionPresenceGap  = 15 * time.Minute // 预留：在场 session 结束
)

// sessionDayLoc 是切段所用的「一天」边界时区。人话版说「跨午夜 00:00 切开」，指的是本地日；
// 用 Local（服务器/本机所在时区）而非 UTC，避免把人自己的凌晨算错一天。测试改为 UTC。
var sessionDayLoc = time.Local

// 在场与组件事件 type（附表 A）。
const (
	TypeSystemSleep        = "system.sleep"
	TypeSystemWake         = "system.wake"
	TypeSystemScreensSleep = "system.screens.sleep"
	TypeSystemScreensWake  = "system.screens.wake"
	TypeClientAppLaunched  = "client.app.launched"
	TypeClientAppQuit      = "client.app.quit"
	TypeClientFocusGained  = "client.focus.gained"
	TypeClientFocusLost    = "client.focus.lost"
	TypeComponentOpened    = "component.opened"
	TypeComponentClosed    = "component.closed"
)

// sessionLevel 定义一层的 start/end 事件 type 与锚字段（launch_id/focus_epoch_id/…）。
// L1/L2/L4 靠锚直接归属（进程连续重启也不串）；L0 靠 wake_epoch_id；L3 由 L2 去抖而来。
type sessionLevel struct {
	level     int
	startType string
	endType   string
	anchorFn  func(presenceSource) string
}

var sessionLevels = []sessionLevel{
	{0, TypeSystemWake, TypeSystemSleep, func(p presenceSource) string { return p.WakeEpochID }},
	{1, TypeClientAppLaunched, TypeClientAppQuit, func(p presenceSource) string { return p.LaunchID }},
	{2, TypeClientFocusGained, TypeClientFocusLost, func(p presenceSource) string { return p.FocusEpochID }},
	{4, TypeComponentOpened, TypeComponentClosed, func(p presenceSource) string { return p.ComponentInstanceID }},
}

// presenceSource 是五层派生需要的事件列。
type presenceSource struct {
	EventID             string
	Type                string
	OccurredAt          time.Time
	Actor               string
	LaunchID            string
	FocusEpochID        string
	ComponentInstanceID string
	WakeEpochID         string
}

// sessionRow 是 sessions 表的一行。
type sessionRow struct {
	SessionID        string  `gorm:"column:session_id;primaryKey"`
	Level            int     `gorm:"column:level"`
	ParentID         *string `gorm:"column:parent_id"`
	AnchorID         *string `gorm:"column:anchor_id"`
	StartedAt        string  `gorm:"column:started_at"`
	EndedAt          *string `gorm:"column:ended_at"`
	ClosedBy         *string `gorm:"column:closed_by"`
	ThresholdVersion int     `gorm:"column:threshold_version"`
	ComputedAt       string  `gorm:"column:computed_at"`
}

func (sessionRow) TableName() string { return "sessions" }

// eventSessionRow 是 event_session 表的一行。
type eventSessionRow struct {
	EventID   string `gorm:"column:event_id;primaryKey"`
	Level     int    `gorm:"column:level;primaryKey"`
	SessionID string `gorm:"column:session_id"`
}

func (eventSessionRow) TableName() string { return "event_session" }

// rawSession 是切段前的一段区间（尚未按天切开）。
type rawSession struct {
	level    int
	anchor   string
	start    time.Time
	end      time.Time
	closedBy string
}

// closeSessions 全量重跑五层在场：清空 sessions/event_session 后从在场事件重算。
// 与 closeIntervals 同为「清空 + 重扫」，整段一个写事务（P16 定时前须先停 ingest）。
func (s *Store) closeSessions(tx *gorm.DB) error {
	if err := tx.Exec(`DELETE FROM sessions`).Error; err != nil {
		return fmt.Errorf("清空 sessions: %w", err)
	}
	if err := tx.Exec(`DELETE FROM event_session`).Error; err != nil {
		return fmt.Errorf("清空 event_session: %w", err)
	}
	events, err := scanPresenceEvents(tx)
	if err != nil {
		return err
	}
	boundaries := boundaryTimes(toSourceEvents(events))
	raw := computeRawSessions(events, boundaries)
	sessions := buildSessions(raw, sessionDayLoc)
	mapping := mapEventsToSessions(sessions, events)
	if len(sessions) > 0 {
		if err := tx.CreateInBatches(&sessions, 500).Error; err != nil {
			return fmt.Errorf("insert %d sessions: %w", len(sessions), err)
		}
	}
	if len(mapping) > 0 {
		if err := tx.CreateInBatches(&mapping, 500).Error; err != nil {
			return fmt.Errorf("insert %d event_session: %w", len(mapping), err)
		}
	}
	return tx.Exec(
		`INSERT INTO meta("key", value, updated_at) VALUES(?, ?, ?)
		 ON CONFLICT("key") DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		"threshold_version", strconv.Itoa(SessionThresholdVersion), time.Now().UTC().Format(time.RFC3339),
	).Error
}

// RebuildSessions 全量重跑五层在场（可在阈值变更后调用）。
func (s *Store) RebuildSessions() error {
	if s == nil {
		return fmt.Errorf("events.db 未启用")
	}
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		return s.closeSessions(tx)
	}); err != nil {
		return fmt.Errorf("rebuild sessions: %w", err)
	}
	return nil
}

// scanPresenceEvents 读出全部事件（按 occurred_at 排序）。全量读入内存，理由同 scanSourceEvents。
func scanPresenceEvents(tx *gorm.DB) ([]presenceSource, error) {
	var raw []struct {
		EventID             string  `gorm:"column:event_id"`
		Type                string  `gorm:"column:type"`
		OccurredAt          string  `gorm:"column:occurred_at"`
		Actor               string  `gorm:"column:actor"`
		LaunchID            *string `gorm:"column:launch_id"`
		FocusEpochID        *string `gorm:"column:focus_epoch_id"`
		ComponentInstanceID *string `gorm:"column:component_instance_id"`
		WakeEpochID         *string `gorm:"column:wake_epoch_id"`
	}
	err := tx.Raw(
		`SELECT event_id, type, occurred_at, actor, launch_id, focus_epoch_id, component_instance_id, wake_epoch_id
		 FROM events ORDER BY occurred_at, received_at, event_id`,
	).Scan(&raw).Error
	if err != nil {
		return nil, fmt.Errorf("扫描 events(在场): %w", err)
	}
	out := make([]presenceSource, 0, len(raw))
	for _, r := range raw {
		at, err := time.Parse(isoMillis, r.OccurredAt)
		if err != nil {
			log.Printf("[activity] 五层派生：解析 occurred_at 失败，跳过 %s: %v", r.EventID, err)
			continue
		}
		out = append(out, presenceSource{
			EventID: r.EventID, Type: r.Type, OccurredAt: at, Actor: r.Actor,
			LaunchID: ptrStr(r.LaunchID), FocusEpochID: ptrStr(r.FocusEpochID),
			ComponentInstanceID: ptrStr(r.ComponentInstanceID), WakeEpochID: ptrStr(r.WakeEpochID),
		})
	}
	return out, nil
}

// toSourceEvents 把在场事件投影成 sourceEvent，供 P13 的 boundaryTimes 复用（只看 type/occurred_at）。
func toSourceEvents(es []presenceSource) []sourceEvent {
	out := make([]sourceEvent, len(es))
	for i, e := range es {
		out[i] = sourceEvent{EventID: e.EventID, Type: e.Type, OccurredAt: e.OccurredAt}
	}
	return out
}

// computeRawSessions 从在场事件算出 L0/L1/L2/L4 原始区间，再派生 L3（L2 去抖）。
// 一端缺失（崩溃/强杀）时：先看后续第一个 boundary，否则 fallback 保守闭合。
func computeRawSessions(events []presenceSource, boundaries []time.Time) []rawSession {
	out := make([]rawSession, 0)
	for _, lv := range sessionLevels {
		out = append(out, levelSessions(events, lv, boundaries)...)
	}
	out = append(out, debounceL3(level2Sessions(out))...)
	return out
}

// levelSessions 派生单层：每个锚开一段，配对到同锚的 end；缺 end 走 boundary/fallback。
func levelSessions(events []presenceSource, lv sessionLevel, boundaries []time.Time) []rawSession {
	// starts 按出现顺序；ends 按锚记第一个（忽略重复端）。
	type seg struct {
		start presenceSource
		end   *presenceSource
	}
	segByAnchor := make(map[string]*seg)
	order := make([]string, 0)
	for i := range events {
		e := events[i]
		anchor := lv.anchorFn(e)
		if anchor == "" {
			continue
		}
		switch e.Type {
		case lv.startType:
			// 同锚重复 start（重投/epoch 复用）：覆盖为该锚最后一段，但 order 只记一次，
			// 否则会产出多行同 session_id → CreateInBatches 主键冲突，整轮重建失败。
			if _, seen := segByAnchor[anchor]; !seen {
				order = append(order, anchor)
			}
			segByAnchor[anchor] = &seg{start: e}
		case lv.endType:
			if g, ok := segByAnchor[anchor]; ok && g.end == nil {
				cp := e
				g.end = &cp
			}
		}
	}
	out := make([]rawSession, 0, len(order))
	for _, anchor := range order {
		g := segByAnchor[anchor]
		end, closedBy := closePresence(g.start.OccurredAt, g.end, boundaries)
		out = append(out, rawSession{level: lv.level, anchor: anchor, start: g.start.OccurredAt, end: end, closedBy: closedBy})
	}
	return out
}

// closePresence 定区间终点：有配对端用配对；否则首个 boundary；否则 fallback。
func closePresence(start time.Time, end *presenceSource, boundaries []time.Time) (time.Time, string) {
	if end != nil && !end.OccurredAt.Before(start) {
		return end.OccurredAt, ClosedPaired
	}
	if b, ok := firstBoundaryAfter(boundaries, start); ok {
		return b, ClosedBoundary
	}
	return start.Add(intervalFallbackDuration), ClosedFallback
}

// level2Sessions 挑出已算出的 L2 区间（供 L3 去抖）。
func level2Sessions(all []rawSession) []rawSession {
	out := make([]rawSession, 0)
	for _, r := range all {
		if r.level == 2 {
			out = append(out, r)
		}
	}
	return out
}

// debounceL3 = L2 去抖：相邻 L2 区间若间隙 ≤ sessionFocusDebounce 则合并为一段 L3。
// 初值 0 时无间隙可合并（L3==L2）；阈值调大后跨过短切出，把碎片拼成一段注意力期。
func debounceL3(l2 []rawSession) []rawSession {
	if len(l2) == 0 {
		return nil
	}
	sorted := append([]rawSession(nil), l2...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].start.Before(sorted[j].start) })
	debounce := time.Duration(sessionFocusDebounce)
	out := make([]rawSession, 0, len(sorted))
	cur := sorted[0]
	cur.level = 3
	for _, r := range sorted[1:] {
		if r.start.Sub(cur.end) <= debounce {
			if r.end.After(cur.end) {
				cur.end = r.end
				cur.closedBy = r.closedBy
			}
			continue
		}
		out = append(out, cur)
		cur = r
		cur.level = 3
	}
	out = append(out, cur)
	return out
}

// buildSessions 按天切段、算 parent_id。session_id 取「level + 锚 + 起刻毫秒」，
// 同输入同版本下稳定（幂等要求见设计篇 5.2）。
func buildSessions(raw []rawSession, loc *time.Location) []sessionRow {
	now := time.Now().UTC().Format(isoMillis)
	rows := make([]sessionRow, 0, len(raw))
	for _, r := range raw {
		for _, seg := range splitByDay(r, loc) {
			rows = append(rows, sessionRow{
				SessionID:        sessionID(seg.level, seg.anchor, seg.start),
				Level:            seg.level,
				AnchorID:         nullable(seg.anchor),
				StartedAt:        seg.start.UTC().Format(isoMillis),
				EndedAt:          nullable(seg.end.UTC().Format(isoMillis)),
				ClosedBy:         nullable(seg.closedBy),
				ThresholdVersion: SessionThresholdVersion,
				ComputedAt:       now,
			})
		}
	}
	assignParents(rows)
	return rows
}

// splitByDay 把一段按本地午夜切开（跨午夜视为强结束，设计篇 6.2）。
func splitByDay(r rawSession, loc *time.Location) []rawSession {
	out := make([]rawSession, 0, 1)
	start := r.start
	for {
		nextMidnight := time.Date(start.In(loc).Year(), start.In(loc).Month(), start.In(loc).Day()+1, 0, 0, 0, 0, loc)
		if !r.end.After(nextMidnight) {
			out = append(out, rawSession{level: r.level, anchor: r.anchor, start: start, end: r.end, closedBy: r.closedBy})
			break
		}
		out = append(out, rawSession{level: r.level, anchor: r.anchor, start: start, end: nextMidnight, closedBy: ClosedDaySplit})
		start = nextMidnight
	}
	return out
}

// assignParents 写 parent_id：每段在 level-1 里找包含它起点的区间（最近一个外层），没有则 NULL。
// 同级允许重叠，故用「包含 start」而非树；L0 无父。
//
// 只在 level-1 的区间里查（不是全表），并按 start 排序二分逼近，避免 O(N²)。
func assignParents(rows []sessionRow) {
	type parsedSpan struct {
		idx   int
		start time.Time
		end   time.Time
	}
	byLevel := make(map[int][]parsedSpan)
	skipped := 0
	for i := range rows {
		if rows[i].EndedAt == nil {
			continue
		}
		st, err1 := time.Parse(isoMillis, rows[i].StartedAt)
		en, err2 := time.Parse(isoMillis, *rows[i].EndedAt)
		if err1 != nil || err2 != nil {
			skipped++
			continue
		}
		byLevel[rows[i].Level] = append(byLevel[rows[i].Level], parsedSpan{i, st, en})
	}
	if skipped > 0 {
		log.Printf("[activity] assignParents: %d 条 session 时间解析失败，未参与父层匹配（parent_id 可能为空）", skipped)
	}
	for lv := range byLevel {
		sort.Slice(byLevel[lv], func(a, b int) bool { return byLevel[lv][a].start.Before(byLevel[lv][b].start) })
	}
	for i := range rows {
		if rows[i].Level == 0 {
			continue
		}
		st, err := time.Parse(isoMillis, rows[i].StartedAt)
		if err != nil {
			log.Printf("[activity] assignParents: session %s 起始时间解析失败（%q），跳过父层匹配: %v", rows[i].SessionID, rows[i].StartedAt, err)
			continue
		}
		// 最近外层 = level-1 里 start ≤ 本段 start、end ≥ 本段 start、且 start 最大者。
		best := ""
		parents := byLevel[rows[i].Level-1]
		// 从「最右一个 start ≤ st」往回走，第一个包含 st 的即最近外层。
		j := sort.Search(len(parents), func(k int) bool { return parents[k].start.After(st) }) - 1
		for ; j >= 0; j-- {
			if !parents[j].end.Before(st) {
				best = rows[parents[j].idx].SessionID
				break
			}
		}
		if best != "" {
			pid := best
			rows[i].ParentID = &pid
		}
	}
}

// sessionSpan 是一个已落库 session 的时间范围与锚，供事件映射的包含匹配。
type sessionSpan struct {
	id     string
	start  time.Time
	end    time.Time
	anchor string
}

// sessionIndex 是事件映射用的索引：按层存（按 start 排序）+ 有锚层再建 anchor→spans。
type sessionIndex struct {
	byLevel  map[int][]sessionSpan
	byAnchor map[int]map[string][]sessionSpan
}

// mapEventsToSessions 为每条事件、每层找所属 session 写进 event_session。
// L1/L2/L4 优先按 epoch 锚精确归属（事件带对应 launch_id/focus_epoch_id/component_instance_id 时）；
// L0/L3 无逐事件锚，退回时间包含匹配（设计篇 6.2 的「时间包含加 epoch 算出」）。
func mapEventsToSessions(rows []sessionRow, events []presenceSource) []eventSessionRow {
	idx := buildSessionIndex(rows)
	anchorFor := map[int]func(presenceSource) string{
		0: func(p presenceSource) string { return p.WakeEpochID },
		1: func(p presenceSource) string { return p.LaunchID },
		2: func(p presenceSource) string { return p.FocusEpochID },
		4: func(p presenceSource) string { return p.ComponentInstanceID },
	}
	out := make([]eventSessionRow, 0)
	for _, e := range events {
		for _, lv := range []int{0, 1, 2, 3, 4} {
			var want string
			if fn, ok := anchorFor[lv]; ok {
				want = fn(e)
			}
			if id := idx.find(lv, e.OccurredAt, want); id != "" {
				out = append(out, eventSessionRow{EventID: e.EventID, Level: lv, SessionID: id})
			}
		}
	}
	return out
}

func buildSessionIndex(rows []sessionRow) *sessionIndex {
	idx := &sessionIndex{byLevel: make(map[int][]sessionSpan), byAnchor: make(map[int]map[string][]sessionSpan)}
	for _, r := range rows {
		if r.EndedAt == nil {
			continue
		}
		st, err1 := time.Parse(isoMillis, r.StartedAt)
		en, err2 := time.Parse(isoMillis, *r.EndedAt)
		if err1 != nil || err2 != nil {
			continue
		}
		sp := sessionSpan{id: r.SessionID, start: st, end: en, anchor: ptrStr(r.AnchorID)}
		idx.byLevel[r.Level] = append(idx.byLevel[r.Level], sp)
		if sp.anchor != "" {
			if idx.byAnchor[r.Level] == nil {
				idx.byAnchor[r.Level] = make(map[string][]sessionSpan)
			}
			idx.byAnchor[r.Level][sp.anchor] = append(idx.byAnchor[r.Level][sp.anchor], sp)
		}
	}
	for lv := range idx.byLevel {
		sortSpans(idx.byLevel[lv])
	}
	for lv := range idx.byAnchor {
		for a := range idx.byAnchor[lv] {
			sortSpans(idx.byAnchor[lv][a])
		}
	}
	return idx
}

func sortSpans(spans []sessionSpan) {
	sort.Slice(spans, func(a, b int) bool { return spans[a].start.Before(spans[b].start) })
}

// find 找 level 上包含 t 的 session：有锚先用锚索引（O(1)），否则按 start 二分逼近，
// 取「start ≤ t 且 end ≥ t 的最近起点」。找不到返回空串。
func (idx *sessionIndex) find(level int, t time.Time, want string) string {
	if want != "" {
		// 锚索引：同锚可能多段（跨午夜切段），取包含 t 的最近起点。
		return nearestContaining(idx.byAnchor[level][want], t)
	}
	return nearestContaining(idx.byLevel[level], t)
}

// nearestContaining 在按 start 排序的 spans 里找包含 t 的最近起点（start 最大者）。
// 从「最右一个 start ≤ t」往回扫，首个包含 t 的即最近（更早的 start 都更远）。
func nearestContaining(spans []sessionSpan, t time.Time) string {
	j := sort.Search(len(spans), func(k int) bool { return spans[k].start.After(t) }) - 1
	for ; j >= 0; j-- {
		if !t.After(spans[j].end) {
			return spans[j].id
		}
	}
	return ""
}

// sessionID 生成稳定 id：level + 锚（空锚用 '-'）+ 起刻毫秒。
func sessionID(level int, anchor string, start time.Time) string {
	if anchor == "" {
		anchor = "-"
	}
	return fmt.Sprintf("L%d:%s:%d", level, anchor, start.UnixMilli())
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func ptrStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
