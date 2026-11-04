package activity

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"
)

// —— 只读查询层（骨架级）——
// 供只读 tool（tool/user_action_log.go）取「当天总览」与「区间列表」。
// 纪律（实施技术方案 §5.3/§5.4）：参数只固定这几个维度、不给 SQL、出口统一走本层；
// 骨架级只吐计数与时间，唯一的字符串是已归一的资源 canonical，不再二次脱敏（卡点留给明细级 P20）。

// DaySkeleton 是 day_skeleton 的返回值：一天的总览。
type DaySkeleton struct {
	Day       string            `json:"day"`
	Hours     []HourBucket      `json:"hours"`     // 小时 × 来源 × 类型 × actor 计数（读 day_stats）
	Sessions  []SessionSummary  `json:"sessions"`  // 当日起始的 session 列表（读 sessions）
	Resources []ResourceSummary `json:"resources"` // 当日被碰过的资源去重清单（读 event_resources）
	Health    *HealthSummary    `json:"health"`    // 体检摘要（读 day_health）；无体检行时 nil
}

// HourBucket 是 day_stats 聚合后的一行（小时 × 来源 × 类型 × actor）。
// 为控制输出体积，按「小时 × 来源」合并：类型与 actor 折叠进 Types/Actor 计数。
type HourBucket struct {
	Hour   int              `json:"hour"`
	Source string           `json:"source"`
	Cnt    int64            `json:"cnt"`
	Types  map[string]int64 `json:"types"`
	Actors map[string]int64 `json:"actors"`
}

// SessionSummary 是 sessions 表的一行（区间列表项）。
type SessionSummary struct {
	SessionID  string  `json:"session_id"`
	Level      int     `json:"level"`
	ParentID   *string `json:"parent_id,omitempty"`
	AnchorID   *string `json:"anchor_id,omitempty"`
	StartedAt  string  `json:"started_at"`
	EndedAt    *string `json:"ended_at,omitempty"`
	ClosedBy   *string `json:"closed_by,omitempty"`
	DurationMs int64   `json:"duration_ms"` // ended_at - started_at；未闭合时为 0
}

// ResourceSummary 是当日被碰过的资源去重项。
type ResourceSummary struct {
	Kind       string `json:"kind"`
	ResourceID string `json:"resource_id"`
	Canonical  string `json:"canonical"`
	EventCnt   int64  `json:"event_cnt"`
}

// HealthSummary 是 day_health 的摘要。offline_gaps / collection_gaps 为 JSON 文本，暂以阶段 4 视图为准，这里透传原文。
type HealthSummary struct {
	InferredRatio  float64 `json:"inferred_ratio"`
	FallbackCount  int64   `json:"fallback_count"`
	ClockAnomalies int64   `json:"clock_anomalies"`
}

// 读取层与派生层共用「一天」的边界时区 sessionDayLoc（见 session.go），不另立变量：
// 两者一旦不同值，读接口就会把本地凌晨算错一天，且不会有任何告警。

// DaySkeleton 取某天的骨架级总览。day 为本地日（YYYY-MM-DD）。
// 当天无派生数据时返回空集合（不报错）；day_health 无行时 Health 为 nil。
func (s *Store) DaySkeleton(day string) (*DaySkeleton, error) {
	if _, err := time.ParseInLocation("2006-01-02", day, sessionDayLoc); err != nil {
		return nil, fmt.Errorf("invalid day %q (want YYYY-MM-DD): %w", day, err)
	}
	out := &DaySkeleton{Day: day, Hours: []HourBucket{}, Sessions: []SessionSummary{}, Resources: []ResourceSummary{}}

	if err := s.daySkeletonHours(out); err != nil {
		return nil, err
	}
	if err := s.daySkeletonSessions(day, out); err != nil {
		return nil, err
	}
	if err := s.daySkeletonResources(day, out); err != nil {
		return nil, err
	}
	if err := s.daySkeletonHealth(day, out); err != nil {
		return nil, err
	}
	return out, nil
}

// daySkeletonHours 读 day_stats，按小时×来源合并（类型/actor 折叠成计数）。
func (s *Store) daySkeletonHours(out *DaySkeleton) error {
	rows := []dayStatRow{}
	if err := s.db.Raw(`SELECT hour, source, type, actor, cnt, duration_ms FROM day_stats
		WHERE day = ? ORDER BY hour, source`, out.Day).Scan(&rows).Error; err != nil {
		return fmt.Errorf("query day_stats: %w", err)
	}
	idx := map[[2]any]int{} // {hour, source} → out.Hours 下标
	for _, r := range rows {
		k := [2]any{r.Hour, r.Source}
		i, ok := idx[k]
		if !ok {
			out.Hours = append(out.Hours, HourBucket{Hour: r.Hour, Source: r.Source, Types: map[string]int64{}, Actors: map[string]int64{}})
			i = len(out.Hours) - 1
			idx[k] = i
		}
		b := &out.Hours[i]
		b.Cnt += r.Cnt
		b.Types[r.Type] += r.Cnt
		b.Actors[r.Actor] += r.Cnt
	}
	return nil
}

// daySkeletonSessions 读该本地日起始的 session（跨午夜在 00:00 已切开，故按起始日即可）。
func (s *Store) daySkeletonSessions(day string, out *DaySkeleton) error {
	start, err := dayBounds(day)
	if err != nil {
		return err
	}
	lo, hi := start.UTC().Format(isoMillis), start.Add(24*time.Hour).UTC().Format(isoMillis)
	if err := s.loadSessionsInto(lo, hi, nil, maxSessionRows, out); err != nil {
		return err
	}
	return nil
}

// daySkeletonResources 读当日事件碰过的资源去重清单（join resources 取 canonical）。
func (s *Store) daySkeletonResources(day string, out *DaySkeleton) error {
	start, err := dayBounds(day)
	if err != nil {
		return err
	}
	lo, hi := start.UTC().Format(isoMillis), start.Add(24*time.Hour).UTC().Format(isoMillis)
	var rows []struct {
		Kind       string `gorm:"column:resource_kind"`
		ResourceID string `gorm:"column:resource_id"`
		Canonical  string `gorm:"column:canonical"`
		EventCnt   int64  `gorm:"column:event_cnt"`
	}
	if err := s.db.Raw(`SELECT er.resource_kind, er.resource_id, COALESCE(r.canonical, er.resource_id) AS canonical, COUNT(DISTINCT er.event_id) AS event_cnt
		FROM event_resources er
		JOIN events e ON e.event_id = er.event_id
		LEFT JOIN resources r ON r.resource_id = er.resource_id
		WHERE e.occurred_at >= ? AND e.occurred_at < ?
		GROUP BY er.resource_kind, er.resource_id
		ORDER BY event_cnt DESC, er.resource_kind, er.resource_id`, lo, hi).Scan(&rows).Error; err != nil {
		return fmt.Errorf("query event_resources: %w", err)
	}
	for _, r := range rows {
		out.Resources = append(out.Resources, ResourceSummary{Kind: r.Kind, ResourceID: r.ResourceID, Canonical: r.Canonical, EventCnt: r.EventCnt})
	}
	return nil
}

// daySkeletonHealth 读 day_health；无行则 Health 保持 nil（读接口不伪造 0）。
func (s *Store) daySkeletonHealth(day string, out *DaySkeleton) error {
	var h dayHealthRow
	if err := s.db.Raw(`SELECT day, inferred_ratio, fallback_count, clock_anomalies FROM day_health WHERE day = ?`, day).Scan(&h).Error; err != nil {
		return fmt.Errorf("query day_health: %w", err)
	}
	// Scan 查不到行不报错，用回读的 day 判存在（省一次 count 往返，且不把「无行」误当「全 0」）。
	if h.Day != "" {
		out.Health = &HealthSummary{InferredRatio: h.InferredRatio, FallbackCount: h.FallbackCount, ClockAnomalies: h.ClockAnomalies}
	}
	return nil
}

// maxSessionRows 是 list_sessions 单次返回的硬上限：from/to 由调用方给定，跨度可达数年，
// 不设限会一次吐数千行灌进 LLM 上下文（day_skeleton 天然按天有界，无此问题）。
const maxSessionRows = 500

// ListSessions 取 [from, to) 内的 session 区间列表；level < 0 表示不限层级。
// limit <= 0 时取默认值，超过 maxSessionRows 一律钳制到上限（硬上限，调用方不可绕过）。
// from/to 为本地时间；只按 sessions.started_at 过滤（跨午夜已切开，起始日即归属日）。
func (s *Store) ListSessions(from, to time.Time, level, limit int) ([]SessionSummary, error) {
	if limit <= 0 || limit > maxSessionRows {
		limit = maxSessionRows
	}
	out := &DaySkeleton{}
	lvl := (*int)(nil)
	if level >= 0 {
		lvl = &level
	}
	lo, hi := from.UTC().Format(isoMillis), to.UTC().Format(isoMillis)
	if err := s.loadSessionsInto(lo, hi, lvl, limit, out); err != nil {
		return nil, err
	}
	return out.Sessions, nil
}

// loadSessionsInto 按 [lo, hi)（isoMillis UTC 串）取 sessions，可选按 level 过滤。limit 必为正。
// ponytail: 无 level 过滤时只按 started_at 范围查，走不到 idx_sessions_level_time(level, started_at)
// 前缀 → 全表扫描；当前 session 量小无碍，数据长大后需补 sessions(started_at) 索引。
func (s *Store) loadSessionsInto(lo, hi string, level *int, limit int, out *DaySkeleton) error {
	q := `SELECT session_id, level, parent_id, anchor_id, started_at, ended_at, closed_by FROM sessions
		WHERE started_at >= ? AND started_at < ?`
	args := []any{lo, hi}
	if level != nil {
		q += ` AND level = ?`
		args = append(args, *level)
	}
	q += ` ORDER BY started_at, level LIMIT ?`
	args = append(args, limit)
	var rows []struct {
		SessionID string  `gorm:"column:session_id"`
		Level     int     `gorm:"column:level"`
		ParentID  *string `gorm:"column:parent_id"`
		AnchorID  *string `gorm:"column:anchor_id"`
		StartedAt string  `gorm:"column:started_at"`
		EndedAt   *string `gorm:"column:ended_at"`
		ClosedBy  *string `gorm:"column:closed_by"`
	}
	if err := s.db.Raw(q, args...).Scan(&rows).Error; err != nil {
		return fmt.Errorf("query sessions: %w", err)
	}
	for _, r := range rows {
		sum := SessionSummary{
			SessionID: r.SessionID, Level: r.Level, ParentID: r.ParentID, AnchorID: r.AnchorID,
			StartedAt: r.StartedAt, EndedAt: r.EndedAt, ClosedBy: r.ClosedBy,
		}
		sum.DurationMs = sessionDurationMs(r.SessionID, r.StartedAt, r.EndedAt)
		out.Sessions = append(out.Sessions, sum)
	}
	return nil
}

// sessionDurationMs 算区间时长；解析失败记日志后返回 0（不因单行坏数据整段失败）。
// 畸形派生行会让该段时长显示为 0，打日志以便排查（对齐 daystat.go 的解析失败计数做法）。
func sessionDurationMs(sessionID, start string, end *string) int64 {
	if end == nil {
		return 0
	}
	st, err := time.Parse(isoMillis, start)
	if err != nil {
		log.Printf("[activity] session %s 起始时间解析失败（%q），duration 记 0: %v", sessionID, start, err)
		return 0
	}
	en, err := time.Parse(isoMillis, *end)
	if err != nil {
		log.Printf("[activity] session %s 结束时间解析失败（%q），duration 记 0: %v", sessionID, *end, err)
		return 0
	}
	d := en.Sub(st)
	if d < 0 {
		log.Printf("[activity] session %s 结束早于开始（%q < %q），duration 记 0", sessionID, *end, start)
		return 0
	}
	return d.Milliseconds()
}

// dayBounds 把本地日 YYYY-MM-DD 解成当天本地 00:00 时刻。
func dayBounds(day string) (time.Time, error) {
	t, err := time.ParseInLocation("2006-01-02", day, sessionDayLoc)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid day %q: %w", day, err)
	}
	return t, nil
}

// —— 只读查询层（范围级，P19）——
// 纪律同骨架级：参数固定维度、不给 SQL；本层只吐 events.db 内的数据（含 canonical、tool_name），
// doc 资源的中文标题由 tool 层批量 join 主库补（activity 包不依赖主库，保持内聚）。

// EventFilter 是 list_events 的过滤维度；零值表示该维度不限。
// ResourceKind 与 ResourceID 必须同给才生效（只给 kind 会退化成按大类全量拉，设计上禁止）。
type EventFilter struct {
	Type         string
	Source       string // 由 type 前缀推导，见 sourceOf
	Actor        string
	SessionID    string
	ResourceKind string
	ResourceID   string
}

// EventResource 是事件碰过的一个资源（范围级只给可读标识，正文/全文标题属明细级）。
type EventResource struct {
	Kind       string `json:"kind"`
	ResourceID string `json:"resource_id"`
	Canonical  string `json:"canonical"`
}

// EventSummary 是 list_events / facts_by_memory 的一条事件摘要。
type EventSummary struct {
	EventID    string          `json:"event_id"`
	Type       string          `json:"type"`
	Source     string          `json:"source"`
	OccurredAt string          `json:"occurred_at"`
	Actor      string          `json:"actor"`
	Outcome    string          `json:"outcome,omitempty"`
	ToolName   string          `json:"tool_name,omitempty"`
	Resources  []EventResource `json:"resources"`
}

// TimeRange 是某资源被碰过的一段时间（由相邻事件并段得到，不落库）。
type TimeRange struct {
	Start      string `json:"start"`
	End        string `json:"end"`
	DurationMs int64  `json:"duration_ms"`
	EventCnt   int64  `json:"event_cnt"`
}

const (
	maxEventRows = 500
	maxFactRows  = 100
	// maxRangeEventRows 是 TimeRangesByResource 读取时刻的行数上限（段数上限另由 limit 管）。
	// 一个资源一天通常几十条；上万条说明是热点大窗口，先卡住内存、记日志提示可能截断。
	maxRangeEventRows = 10000
)

// 并段间隔：相邻事件超过它就不算"同一段"（对齐 intervalFallbackDuration）。
var timeRangeGapMs = intervalFallbackDuration.Milliseconds()

// ListEvents 取 [from, to) 内满足 filter 的事件摘要，按时间升序，最多 limit 条（硬上限 maxEventRows）。
func (s *Store) ListEvents(from, to time.Time, f EventFilter, limit int) ([]EventSummary, error) {
	if limit <= 0 || limit > maxEventRows {
		limit = maxEventRows
	}
	// source 会被拼进 GLOB 模式，先卡成纯标识符，防止 '*'/'?'/'[' 等元字符放宽匹配。
	if f.Source != "" && !validSource(f.Source) {
		return nil, fmt.Errorf("invalid source %q (want ^[a-z_]+$)", f.Source)
	}
	lo, hi := from.UTC().Format(isoMillis), to.UTC().Format(isoMillis)
	q := `SELECT e.event_id, e.type, e.occurred_at, e.actor, e.outcome, e.attrs FROM events e`
	args := []any{}
	if f.SessionID != "" {
		q += ` JOIN event_session es ON es.event_id = e.event_id AND es.level = 1`
	}
	if f.ResourceKind != "" && f.ResourceID != "" {
		q += ` JOIN event_resources er ON er.event_id = e.event_id`
	}
	q += ` WHERE e.occurred_at >= ? AND e.occurred_at < ?`
	args = append(args, lo, hi)
	if f.Type != "" {
		q += ` AND e.type = ?`
		args = append(args, f.Type)
	}
	if f.Source != "" {
		// type 前缀 = source。契约保证事件类型恒为「域.对象.动作」带点（附表 A），
		// 故 source+「.*」必能命中该域全部类型；source 已在上方校验为纯标识符，无元字符歧义。
		q += ` AND e.type GLOB ?`
		args = append(args, f.Source+".*")
	}
	if f.Actor != "" {
		q += ` AND e.actor = ?`
		args = append(args, f.Actor)
	}
	if f.SessionID != "" {
		q += ` AND es.session_id = ?`
		args = append(args, f.SessionID)
	}
	if f.ResourceKind != "" && f.ResourceID != "" {
		q += ` AND er.resource_kind = ? AND er.resource_id = ?`
		args = append(args, f.ResourceKind, f.ResourceID)
	}
	q += ` ORDER BY e.occurred_at, e.event_id LIMIT ?`
	args = append(args, limit)

	var rows []struct {
		EventID    string  `gorm:"column:event_id"`
		Type       string  `gorm:"column:type"`
		OccurredAt string  `gorm:"column:occurred_at"`
		Actor      string  `gorm:"column:actor"`
		Outcome    *string `gorm:"column:outcome"`
		Attrs      *string `gorm:"column:attrs"`
	}
	if err := s.db.Raw(q, args...).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("query events: %w", err)
	}
	out := make([]EventSummary, 0, len(rows))
	ids := make([]string, 0, len(rows))
	byID := make(map[string]int, len(rows))
	for _, r := range rows {
		sum := EventSummary{EventID: r.EventID, Type: r.Type, Source: sourceOf(r.Type),
			OccurredAt: r.OccurredAt, Actor: r.Actor, Resources: []EventResource{}}
		if r.Outcome != nil {
			sum.Outcome = *r.Outcome
		}
		sum.ToolName = attrString(r.Attrs, "tool_name")
		byID[r.EventID] = len(out)
		out = append(out, sum)
		ids = append(ids, r.EventID)
	}
	if len(ids) > 0 {
		if err := s.attachResources(ids, out, byID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// attachResources 给已取出的事件批量贴资源（一次 IN 查询，避免 N+1）。
func (s *Store) attachResources(ids []string, out []EventSummary, byID map[string]int) error {
	var rows []struct {
		EventID    string `gorm:"column:event_id"`
		Kind       string `gorm:"column:resource_kind"`
		ResourceID string `gorm:"column:resource_id"`
		Canonical  string `gorm:"column:canonical"`
	}
	if err := s.db.Raw(`SELECT er.event_id, er.resource_kind, er.resource_id,
			COALESCE(r.canonical, er.resource_id) AS canonical
		FROM event_resources er
		LEFT JOIN resources r ON r.resource_id = er.resource_id
		WHERE er.event_id IN ?
		ORDER BY er.resource_kind, er.resource_id`, ids).Scan(&rows).Error; err != nil {
		return fmt.Errorf("query event_resources: %w", err)
	}
	for _, r := range rows {
		i, ok := byID[r.EventID]
		if !ok {
			continue
		}
		out[i].Resources = append(out[i].Resources, EventResource{Kind: r.Kind, ResourceID: r.ResourceID, Canonical: r.Canonical})
	}
	return nil
}

// TimeRangesByResource 取某资源在 [from, to) 内被碰过的时段：按 occurred_at 排序后，
// 相邻间隔 ≤ timeRangeGapMs 的并成一段。limit 为段数上限（硬上限 maxEventRows）。
// 输入行数另设上限 maxRangeEventRows：limit 只截"段数"，不截读取的行数，热点资源 + 大窗口
// 可能把上百万条时刻载进内存，故 SQL 端先卡一道。
// 返回值 readCapped 表示读取行数触顶（后面的时刻根本没读，段数可能还在 limit 以内）——
// 调用方必须把它和「段数触顶」一起当成截断暴露出去，否则读取截断会变成静默丢数据。
func (s *Store) TimeRangesByResource(kind, id string, from, to time.Time, limit int) (ranges []TimeRange, readCapped bool, err error) {
	if limit <= 0 || limit > maxEventRows {
		limit = maxEventRows
	}
	lo, hi := from.UTC().Format(isoMillis), to.UTC().Format(isoMillis)
	var times []string
	if err := s.db.Raw(`SELECT e.occurred_at
		FROM event_resources er JOIN events e ON e.event_id = er.event_id
		WHERE er.resource_kind = ? AND er.resource_id = ? AND e.occurred_at >= ? AND e.occurred_at < ?
		ORDER BY e.occurred_at LIMIT ?`, kind, id, lo, hi, maxRangeEventRows+1).Scan(&times).Error; err != nil {
		return nil, false, fmt.Errorf("query resource events: %w", err)
	}
	if len(times) > maxRangeEventRows {
		log.Printf("[activity] resource %s/%s 在 [%s,%s) 内事件数超过 %d，只取前 %d 条，结果可能截断",
			kind, id, lo, hi, maxRangeEventRows, maxRangeEventRows)
		times = times[:maxRangeEventRows]
		readCapped = true
	}
	return mergeTimeRanges(id, times, limit), readCapped, nil
}

// mergeTimeRanges 把排好序的 ISO 时刻并成时段；坏时刻跳过并记日志（不因单行坏数据整段失败）。
// 时长在闭合每一段时就地算好（appendRange），避免"命中 limit 提前返回"时漏算末尾循环。
func mergeTimeRanges(resourceID string, times []string, limit int) []TimeRange {
	ranges := []TimeRange{}
	appendRange := func(r TimeRange) {
		r.DurationMs = rangeDurationMs(resourceID, r)
		ranges = append(ranges, r)
	}
	var cur *TimeRange
	var curEnd time.Time
	for _, ts := range times {
		t, err := time.Parse(isoMillis, ts)
		if err != nil {
			log.Printf("[activity] resource %s 事件时刻解析失败（%q），跳过: %v", resourceID, ts, err)
			continue
		}
		if cur == nil {
			cur = &TimeRange{Start: ts, End: ts}
			curEnd = t
		} else if t.Sub(curEnd).Milliseconds() <= timeRangeGapMs {
			cur.End = ts
			curEnd = t
		} else {
			appendRange(*cur)
			if len(ranges) >= limit {
				return ranges
			}
			cur = &TimeRange{Start: ts, End: ts}
			curEnd = t
		}
		cur.EventCnt++
	}
	if cur != nil && len(ranges) < limit {
		appendRange(*cur)
	}
	return ranges
}

// rangeDurationMs 算时段时长；解析失败记日志后返 0。
func rangeDurationMs(resourceID string, r TimeRange) int64 {
	st, err := time.Parse(isoMillis, r.Start)
	if err != nil {
		log.Printf("[activity] resource %s 时段起点解析失败（%q），duration 记 0: %v", resourceID, r.Start, err)
		return 0
	}
	en, err := time.Parse(isoMillis, r.End)
	if err != nil {
		log.Printf("[activity] resource %s 时段终点解析失败（%q），duration 记 0: %v", resourceID, r.End, err)
		return 0
	}
	if d := en.Sub(st); d > 0 {
		return d.Milliseconds()
	}
	return 0
}

// FactsByMemory 取某记忆关联的最近事件（沿 event_refs 的 memory 引用，P21 落 resource 来源），
// 按时间倒序，最多 limit 条。无引用时返回空集合（不是错误）。
func (s *Store) FactsByMemory(memoryID string, limit int) ([]EventSummary, error) {
	if limit <= 0 || limit > maxFactRows {
		limit = maxFactRows
	}
	var rows []struct {
		EventID    string  `gorm:"column:event_id"`
		Type       string  `gorm:"column:type"`
		OccurredAt string  `gorm:"column:occurred_at"`
		Actor      string  `gorm:"column:actor"`
		Outcome    *string `gorm:"column:outcome"`
		Attrs      *string `gorm:"column:attrs"`
	}
	// DISTINCT：event_refs 主键含 source，同一 (event, memory) 可有多条不同 source 的引用，
	// 不去重会让同一事件重复出现（且 byID 只记最后一次下标）。
	if err := s.db.Raw(`SELECT DISTINCT e.event_id, e.type, e.occurred_at, e.actor, e.outcome, e.attrs
		FROM event_refs rf JOIN events e ON e.event_id = rf.event_id
		WHERE rf.ref_type = 'memory' AND rf.ref_id = ?
		ORDER BY e.occurred_at DESC LIMIT ?`, memoryID, limit).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("query memory facts: %w", err)
	}
	out := make([]EventSummary, 0, len(rows))
	ids := make([]string, 0, len(rows))
	byID := make(map[string]int, len(rows))
	for _, r := range rows {
		sum := EventSummary{EventID: r.EventID, Type: r.Type, Source: sourceOf(r.Type),
			OccurredAt: r.OccurredAt, Actor: r.Actor, Resources: []EventResource{}}
		if r.Outcome != nil {
			sum.Outcome = *r.Outcome
		}
		sum.ToolName = attrString(r.Attrs, "tool_name")
		byID[r.EventID] = len(out)
		out = append(out, sum)
		ids = append(ids, r.EventID)
	}
	if len(ids) > 0 {
		if err := s.attachResources(ids, out, byID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// validSource 校验 source 是纯标识符（^[a-z_]+$）：它要拼进 GLOB 模式，必须无元字符。
func validSource(s string) bool {
	for _, c := range s {
		if (c < 'a' || c > 'z') && c != '_' {
			return false
		}
	}
	return s != ""
}

// attrString 从 attrs JSON 文本里取一个字符串键；空/坏 JSON/缺键都返回 ""。
func attrString(attrs *string, key string) string {
	if attrs == nil || *attrs == "" {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(*attrs), &m); err != nil {
		log.Printf("[activity] attrs 解析失败，忽略 %s: %v", key, err)
		return ""
	}
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// —— 只读查询层（明细级）——

const (
	// MaxDetailIDs 是事件 id 列表上限（导出，供 tool 层在调用前显式校验并回报，而非静默截断）。
	MaxDetailIDs = 50
	// maxDetailTextLen 是单条明细文本（页面标题）截断后的**字节**上限（复用 rune 安全的 truncateRunes）。
	maxDetailTextLen = 500
)

// EventDetailItem 是明细级的一条：比 EventSummary 多带内容文本摘要。
// 工具入参**只有摘要与长度**（原文从未进 events.db，取不回）；页面标题为全文（出口已过 sanitize）。
type EventDetailItem struct {
	EventID    string `json:"event_id"`
	Type       string `json:"type"`
	OccurredAt string `json:"occurred_at"`
	Actor      string `json:"actor"`
	ToolName   string `json:"tool_name,omitempty"`
	ArgsDigest string `json:"args_digest,omitempty"`
	ArgsLen    int    `json:"args_len,omitempty"`
	PageTitle  string `json:"page_title,omitempty"`
}

// EventDetail 取指定事件 id 的内容文本摘要。是唯一会吐内容文本的读取入口（第三级），
// 只在范围级结果指向明确时调用，且由调用方（tool 层）记一条 read.query 留痕。
// 只读 events 表；doc 标题仍由 tool 层 join 主库。ids 超上限截断，空 ids 返回空。
func (s *Store) EventDetail(ids []string) ([]EventDetailItem, error) {
	if len(ids) == 0 {
		return []EventDetailItem{}, nil
	}
	if len(ids) > MaxDetailIDs {
		ids = ids[:MaxDetailIDs]
	}
	var rows []struct {
		EventID    string  `gorm:"column:event_id"`
		Type       string  `gorm:"column:type"`
		OccurredAt string  `gorm:"column:occurred_at"`
		Actor      string  `gorm:"column:actor"`
		Attrs      *string `gorm:"column:attrs"`
	}
	if err := s.db.Raw(`SELECT event_id, type, occurred_at, actor, attrs FROM events
		WHERE event_id IN ? ORDER BY occurred_at`, ids).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("query event detail: %w", err)
	}
	out := make([]EventDetailItem, 0, len(rows))
	for _, r := range rows {
		out = append(out, EventDetailItem{
			EventID:    r.EventID,
			Type:       r.Type,
			OccurredAt: r.OccurredAt,
			Actor:      r.Actor,
			ToolName:   attrString(r.Attrs, "tool_name"),
			ArgsDigest: attrString(r.Attrs, "args_digest"),
			ArgsLen:    attrInt(r.Attrs, "args_len"),
			PageTitle:  SanitizeDetailText(attrString(r.Attrs, "page_title")),
		})
	}
	return out, nil
}

// attrInt 从 attrs JSON 文本里取一个整数键；空/坏 JSON/缺键/非数字都返回 0。
func attrInt(attrs *string, key string) int {
	if attrs == nil || *attrs == "" {
		return 0
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(*attrs), &m); err != nil {
		log.Printf("[activity] attrs 解析失败，忽略 %s: %v", key, err)
		return 0
	}
	if v, ok := m[key].(float64); ok {
		return int(v)
	}
	return 0
}

// SanitizeDetailText 是明细级出口的脱敏：剥控制字符、按 rune 截断。
// URL token 等敏感值在采集/归一时已剥（客户端最小脱敏 + ScrubbedURL），这里再做一道兜底，
// 保证任何明细文本出口前都过统一检查（设计篇「脱敏在出口统一做」）。
func SanitizeDetailText(s string) string {
	if s == "" {
		return ""
	}
	s = strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' {
			return ' '
		}
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	return truncateRunes(s, maxDetailTextLen)
}
