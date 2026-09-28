package activity

import (
	"fmt"
	"log"
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
