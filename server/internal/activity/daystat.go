package activity

import (
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
)

// dayStatsVersion 是每日分布/体检的版本号（沿用在场阈值版本，两者随 session 同批重建）。
// 附表 B：day_stats / day_health 带 threshold_version，过期判定与重算触发看它（设计篇 6.5）。
const dayStatsVersion = SessionThresholdVersion

// dayStatRow 是 day_stats 表的一行。
type dayStatRow struct {
	Day              string `gorm:"column:day;primaryKey"`
	Hour             int    `gorm:"column:hour;primaryKey"`
	Source           string `gorm:"column:source;primaryKey"`
	Type             string `gorm:"column:type;primaryKey"`
	Actor            string `gorm:"column:actor;primaryKey"`
	Cnt              int64  `gorm:"column:cnt"`
	DurationMs       int64  `gorm:"column:duration_ms"`
	ThresholdVersion int    `gorm:"column:threshold_version"`
}

func (dayStatRow) TableName() string { return "day_stats" }

// dayHealthRow 是 day_health 表的一行。
type dayHealthRow struct {
	Day              string  `gorm:"column:day;primaryKey"`
	InferredRatio    float64 `gorm:"column:inferred_ratio"`
	FallbackCount    int64   `gorm:"column:fallback_count"`
	OfflineGaps      string  `gorm:"column:offline_gaps"`
	CollectionGaps   string  `gorm:"column:collection_gaps"`
	ClockAnomalies   int64   `gorm:"column:clock_anomalies"`
	ThresholdVersion int     `gorm:"column:threshold_version"`
	ComputedAt       string  `gorm:"column:computed_at"`
}

func (dayHealthRow) TableName() string { return "day_health" }

// closeDayStats 全量重跑每日分布与体检：清空两张表后从事件/闭合重算（P16 定时前须先停 ingest）。
func (s *Store) closeDayStats(tx *gorm.DB) error {
	if err := tx.Exec(`DELETE FROM day_stats`).Error; err != nil {
		return fmt.Errorf("清空 day_stats: %w", err)
	}
	if err := tx.Exec(`DELETE FROM day_health`).Error; err != nil {
		return fmt.Errorf("清空 day_health: %w", err)
	}
	events, err := scanPresenceEvents(tx)
	if err != nil {
		return err
	}
	intervals, err := scanClosedIntervalsWithEvent(tx)
	if err != nil {
		return err
	}
	clock, err := scanClockAnomalies(tx, sessionDayLoc)
	if err != nil {
		return err
	}
	stats := computeDayStats(intervals, events, sessionDayLoc)
	health := computeDayHealth(intervals, events, clock, sessionDayLoc)
	if len(stats) > 0 {
		if err := tx.CreateInBatches(&stats, 500).Error; err != nil {
			return fmt.Errorf("insert %d day_stats: %w", len(stats), err)
		}
	}
	if len(health) > 0 {
		if err := tx.CreateInBatches(&health, 500).Error; err != nil {
			return fmt.Errorf("insert %d day_health: %w", len(health), err)
		}
	}
	// 自身也写版本：单独调用 RebuildDayStats（测试/手动）时不必依赖 RebuildSessions 先跑过。
	return tx.Exec(
		`INSERT INTO meta("key", value, updated_at) VALUES(?, ?, ?)
		 ON CONFLICT("key") DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		"threshold_version", strconv.Itoa(dayStatsVersion), time.Now().UTC().Format(time.RFC3339),
	).Error
}

// RebuildDayStats 全量重跑每日分布与体检。
func (s *Store) RebuildDayStats() error {
	if s == nil {
		return fmt.Errorf("events.db 未启用")
	}
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		return s.closeDayStats(tx)
	}); err != nil {
		return fmt.Errorf("rebuild day stats: %w", err)
	}
	return nil
}

// closedInterval 是「闭合区间 + 其 start 事件的 type/actor」的投影。
type closedInterval struct {
	StartAt  time.Time
	EndAt    time.Time
	Duration time.Duration
	Type     string
	Actor    string
	ClosedBy string
}

// scanClosedIntervalsWithEvent 读 interval_closed 并联出 start 事件的 type/actor（时长摊分需要）。
func scanClosedIntervalsWithEvent(tx *gorm.DB) ([]closedInterval, error) {
	var raw []struct {
		StartEventID string `gorm:"column:start_event_id"`
		Type         string `gorm:"column:type"`
		Actor        string `gorm:"column:actor"`
		OccurredAt   string `gorm:"column:occurred_at"`
		EndAt        string `gorm:"column:end_at"`
		DurationMs   int64  `gorm:"column:duration_ms"`
		ClosedBy     string `gorm:"column:closed_by"`
	}
	err := tx.Raw(
		`SELECT i.start_event_id, e.type, e.actor, e.occurred_at, i.end_at, i.duration_ms, i.closed_by
		 FROM interval_closed i JOIN events e ON e.event_id = i.start_event_id`,
	).Scan(&raw).Error
	if err != nil {
		return nil, fmt.Errorf("扫描 interval_closed+events: %w", err)
	}
	out := make([]closedInterval, 0, len(raw))
	for _, r := range raw {
		st, err1 := time.Parse(isoMillis, r.OccurredAt)
		en, err2 := time.Parse(isoMillis, r.EndAt)
		if err1 != nil || err2 != nil {
			continue
		}
		out = append(out, closedInterval{
			StartAt: st, EndAt: en, Duration: time.Duration(r.DurationMs) * time.Millisecond,
			Type: r.Type, Actor: r.Actor, ClosedBy: r.ClosedBy,
		})
	}
	return out, nil
}

// computeDayStats 双口径：cnt 按事件 occurred_at 归起始小时（不摊）；duration 按区间与小时的
// 重叠摊到各小时（附表 B）。source = type 第一段（如 chat.turn.started → chat）。
func computeDayStats(intervals []closedInterval, events []presenceSource, loc *time.Location) []dayStatRow {
	type key struct {
		day, source, typ, actor string
		hour                    int
	}
	agg := make(map[key]*dayStatRow)
	bump := func(day string, hour int, source, typ, actor string, cnt, dur int64) {
		k := key{day, source, typ, actor, hour}
		row, ok := agg[k]
		if !ok {
			row = &dayStatRow{Day: day, Hour: hour, Source: source, Type: typ, Actor: actor, ThresholdVersion: dayStatsVersion}
			agg[k] = row
		}
		row.Cnt += cnt
		row.DurationMs += dur
	}
	for _, e := range events {
		if isMetaEvent(e.Type) {
			// read.* 是读取自记（actor=assistant），默认不参与「你干了什么」的统计（附表 A），
			// 否则「问助手我今天干了啥」会抬高当天活动计数（自反指标）。
			continue
		}
		day, hour := dayHour(e.OccurredAt, loc)
		bump(day, hour, sourceOf(e.Type), e.Type, e.Actor, 1, 0)
	}
	for _, iv := range intervals {
		spreadDuration(iv, loc, func(day string, hour int, ms int64) {
			bump(day, hour, sourceOf(iv.Type), iv.Type, iv.Actor, 0, ms)
		})
	}
	out := make([]dayStatRow, 0, len(agg))
	for _, row := range agg {
		out = append(out, *row)
	}
	return out
}

// scanClockAnomalies 按「到达顺序」检测客户端 occurred_at 回拨：同一条 received_at（= 客户端同一
// 批上报）内，若 occurred_at 比批内前一条小，即客户端时钟在这批内回拨。
//
// 为什么不能用事件表既有的 occurred_at 排序判：scanPresenceEvents 已按 occurred_at 升序读入，
// 再逐条比大小恒为 false（历史 bug：clock_anomalies 永远是 0）。
// 局限（有意）：跨批乱序（离线补投、网络重排）不算——那更像到达延迟而非时钟回拨，宁缺毋滥。
// received_at 是服务端落库时刻，批内共享；rowid 是插入序，批内保持客户端上报顺序。
func scanClockAnomalies(tx *gorm.DB, loc *time.Location) (map[string]int64, error) {
	var raw []struct {
		OccurredAt string `gorm:"column:occurred_at"`
		ReceivedAt string `gorm:"column:received_at"`
	}
	if err := tx.Raw(`SELECT occurred_at, received_at FROM events ORDER BY received_at, rowid`).Scan(&raw).Error; err != nil {
		return nil, fmt.Errorf("扫描时钟异常: %w", err)
	}
	out := make(map[string]int64)
	var prevReceived string
	var prevOccurred time.Time
	hasPrev := false
	for _, r := range raw {
		occ, err := time.Parse(isoMillis, r.OccurredAt)
		if err != nil {
			log.Printf("[activity] 时钟异常：解析 occurred_at 失败，跳过: %v", err)
			continue
		}
		if r.ReceivedAt != prevReceived {
			prevReceived = r.ReceivedAt
			hasPrev = false
		}
		if hasPrev && occ.Before(prevOccurred) {
			day, _ := dayHour(occ, loc)
			out[day]++
		}
		prevOccurred = occ
		hasPrev = true
	}
	return out, nil
}

// computeDayHealth 逐日体检：推断闭合占比、fallback 计数、离机/采集断流缺口、时钟异常。
func computeDayHealth(intervals []closedInterval, events []presenceSource, clock map[string]int64, loc *time.Location) []dayHealthRow {
	type acc struct {
		total, inferred, fallback int64
	}
	days := make(map[string]*acc)
	get := func(day string) *acc {
		a, ok := days[day]
		if !ok {
			a = &acc{}
			days[day] = a
		}
		return a
	}
	for _, iv := range intervals {
		day, _ := dayHour(iv.StartAt, loc)
		a := get(day)
		a.total++
		switch iv.ClosedBy {
		case ClosedInferred:
			a.inferred++
		case ClosedFallback, ClosedSuperseded:
			// superseded 也是「无实据、被估计闭合」的一类，计入不可信占比。
			a.fallback++
		}
	}
	// 只有点事件、没有闭合区间的日子也要有体检行：否则该天 day_stats 有、day_health 无，
	// DaySkeleton.Health 为 nil，且当天算出的离机缺口会被丢掉。
	for _, e := range events {
		if isMetaEvent(e.Type) {
			continue
		}
		day, _ := dayHour(e.OccurredAt, loc)
		get(day)
	}
	// L0 缺口（离机）：机器醒着的区间之外、当天有事件覆盖的时间轴。
	offline := computeOfflineGaps(events, loc)
	now := time.Now().UTC().Format(isoMillis)
	out := make([]dayHealthRow, 0, len(days))
	for day, a := range days {
		ratio := 0.0
		if a.total > 0 {
			ratio = float64(a.inferred+a.fallback) / float64(a.total)
		}
		gaps := offline[day]
		if gaps == nil {
			gaps = []string{}
		}
		gj, err := json.Marshal(gaps)
		if err != nil {
			gj = []byte("[]")
		}
		out = append(out, dayHealthRow{
			Day: day, InferredRatio: ratio, FallbackCount: a.fallback,
			OfflineGaps: string(gj), CollectionGaps: "[]", ClockAnomalies: clock[day],
			ThresholdVersion: dayStatsVersion, ComputedAt: now,
		})
	}
	return out
}

// computeOfflineGaps 返回每天「有事件覆盖但 L0 未醒」的缺口（离机）。L0 区间来自 wake↔sleep。
func computeOfflineGaps(events []presenceSource, loc *time.Location) map[string][]string {
	// 收集 L0 醒着的区间。
	type span struct{ start, end time.Time }
	var awake []span
	open := map[string]time.Time{}
	for _, e := range events {
		switch e.Type {
		case TypeSystemWake:
			if e.WakeEpochID != "" {
				open[e.WakeEpochID] = e.OccurredAt
			}
		case TypeSystemSleep:
			if st, ok := open[e.WakeEpochID]; ok && !e.OccurredAt.Before(st) {
				awake = append(awake, span{st, e.OccurredAt})
				delete(open, e.WakeEpochID)
			}
		}
	}
	// 事件覆盖的时间范围（按天）。
	type bounds struct {
		min, max time.Time
		ok       bool
	}
	evDays := make(map[string]*bounds)
	for _, e := range events {
		day, _ := dayHour(e.OccurredAt, loc)
		b, ok := evDays[day]
		if !ok {
			b = &bounds{min: e.OccurredAt, max: e.OccurredAt, ok: true}
			evDays[day] = b
			continue
		}
		if e.OccurredAt.Before(b.min) {
			b.min = e.OccurredAt
		}
		if e.OccurredAt.After(b.max) {
			b.max = e.OccurredAt
		}
	}
	out := make(map[string][]string)
	// L0 无源时不臆造缺口（N/A）；有 L0 区间时，缺口 = 事件跨度内不被任何 L0 覆盖的时段。
	// 只报「当天已有 L0 数据仍算得出缺口」的情形，避免把"根本没采集 L0"误报成离机。
	if len(awake) == 0 {
		for day := range evDays {
			out[day] = nil
		}
		return out
	}
	for day, b := range evDays {
		if !b.ok {
			continue
		}
		var gaps []string
		cursor := b.min
		for _, a := range awake {
			if !a.end.After(cursor) {
				continue
			}
			if a.start.After(cursor) && a.start.Before(b.max) {
				gapEnd := a.start
				if gapEnd.After(b.max) {
					gapEnd = b.max
				}
				gaps = append(gaps, cursor.UTC().Format(isoMillis)+"~"+gapEnd.UTC().Format(isoMillis))
			}
			if a.end.After(cursor) {
				cursor = a.end
			}
		}
		// 末段：最后一个 L0 区间结束后仍到事件跨度末尾 → 也是离机缺口。
		if cursor.Before(b.max) {
			gaps = append(gaps, cursor.UTC().Format(isoMillis)+"~"+b.max.UTC().Format(isoMillis))
		}
		out[day] = gaps
	}
	return out
}

// spreadDuration 把一段区间按与各小时的重叠摊到对应小时（day/hour 由 loc 决定）。
func spreadDuration(iv closedInterval, loc *time.Location, add func(day string, hour int, ms int64)) {
	if iv.Duration <= 0 || iv.EndAt.Before(iv.StartAt) {
		return
	}
	cursor := iv.StartAt
	for cursor.Before(iv.EndAt) {
		// 用 loc 构造本地整点边界（Truncate 按绝对时间截断，半小时偏移时区会错切到 :30）。
		lt := cursor.In(loc)
		hourStart := time.Date(lt.Year(), lt.Month(), lt.Day(), lt.Hour(), 0, 0, 0, loc)
		next := hourStart.Add(time.Hour)
		if next.After(iv.EndAt) {
			next = iv.EndAt
		}
		day, hour := dayHour(cursor, loc)
		add(day, hour, next.Sub(cursor).Milliseconds())
		cursor = next
	}
}

// dayHour 返回时刻在 loc 下的本地日与小时。
func dayHour(t time.Time, loc *time.Location) (string, int) {
	lt := t.In(loc)
	return lt.Format("2006-01-02"), lt.Hour()
}

// sourceOf 取 type 第一段（附表 B：不新造映射表）。
func sourceOf(typ string) string {
	if i := strings.IndexByte(typ, '.'); i > 0 {
		return typ[:i]
	}
	return typ
}

// isMetaEvent 判定「元事件」：读取自记（read.*）这类记录助手取数行为的事件，
// 默认不参与「你干了什么」的统计（附表 A）。用 type 前缀判定，与 sourceOf 同源。
func isMetaEvent(typ string) bool {
	return sourceOf(typ) == "read"
}
