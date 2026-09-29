package activity

import (
	"testing"
	"time"
)

func statKey(s dayStatRow) string {
	return s.Day + "|" + itoa(s.Hour) + "|" + s.Source + "|" + s.Type + "|" + s.Actor
}

func findByKey(rows []dayStatRow, day string, hour int, source, typ, actor string) *dayStatRow {
	for i := range rows {
		r := &rows[i]
		if r.Day == day && r.Hour == hour && r.Source == source && r.Type == typ && r.Actor == actor {
			return r
		}
	}
	return nil
}

func TestDayStatsCountsByOccurredAtHour(t *testing.T) {
	loc := time.UTC
	events := []presenceSource{
		{EventID: "a", Type: TypeToolCallStarted, OccurredAt: at(0), Actor: ActorAssistant},
		{EventID: "b", Type: TypeToolCallStarted, OccurredAt: at(30 * time.Minute), Actor: ActorAssistant},
	}
	rows := computeDayStats(nil, events, loc)
	day := at(0).In(loc).Format("2006-01-02")
	r := findByKey(rows, day, 10, "tool", TypeToolCallStarted, ActorAssistant)
	if r == nil || r.Cnt != 2 {
		t.Fatalf("want cnt=2 at hour 10, got %+v", r)
	}
}

func TestDayStatsSpreadsDurationAcrossHours(t *testing.T) {
	loc := time.UTC
	// 一段从 10:50 到 11:10（20 分钟），跨两小时 → 各摊 10 分钟。
	iv := []closedInterval{{
		StartAt: at(50 * time.Minute), EndAt: at(70 * time.Minute),
		Duration: 20 * time.Minute, Type: TypeToolCallStarted, Actor: ActorAssistant, ClosedBy: ClosedPaired,
	}}
	rows := computeDayStats(iv, nil, loc)
	day := at(0).In(loc).Format("2006-01-02")
	r10 := findByKey(rows, day, 10, "tool", TypeToolCallStarted, ActorAssistant)
	r11 := findByKey(rows, day, 11, "tool", TypeToolCallStarted, ActorAssistant)
	if r10 == nil || r10.DurationMs != (10*time.Minute).Milliseconds() {
		t.Errorf("hour10 dur = %+v", r10)
	}
	if r11 == nil || r11.DurationMs != (10*time.Minute).Milliseconds() {
		t.Errorf("hour11 dur = %+v", r11)
	}
}

func TestDayHealthInferredRatioAndClock(t *testing.T) {
	loc := time.UTC
	day := at(0).In(loc).Format("2006-01-02")
	iv := []closedInterval{
		{StartAt: at(0), EndAt: at(time.Second), Duration: time.Second, Type: TypeToolCallStarted, ClosedBy: ClosedInferred},
		{StartAt: at(time.Minute), EndAt: at(time.Minute + time.Second), Duration: time.Second, Type: TypeToolCallStarted, ClosedBy: ClosedPaired},
		{StartAt: at(2 * time.Minute), EndAt: at(2*time.Minute + time.Second), Duration: time.Second, Type: TypeToolCallStarted, ClosedBy: ClosedFallback},
		{StartAt: at(3 * time.Minute), EndAt: at(3*time.Minute + time.Second), Duration: time.Second, Type: TypeToolCallStarted, ClosedBy: ClosedPaired},
	}
	// 事件仅用于离机缺口；时钟异常由 scanClockAnomalies 单独算出后传入。
	events := []presenceSource{
		{EventID: "e1", Type: TypeToolCallStarted, OccurredAt: at(10 * time.Minute)},
	}
	rows := computeDayHealth(iv, events, map[string]int64{day: 1}, loc)
	if len(rows) != 1 {
		t.Fatalf("health rows = %d, want 1", len(rows))
	}
	h := rows[0]
	if h.Day != day {
		t.Errorf("day = %s, want %s", h.Day, day)
	}
	// inferred+fallback = 2 / total 4 = 0.5
	if h.InferredRatio != 0.5 {
		t.Errorf("inferred_ratio = %v, want 0.5", h.InferredRatio)
	}
	if h.FallbackCount != 1 {
		t.Errorf("fallback_count = %d, want 1", h.FallbackCount)
	}
	if h.ClockAnomalies != 1 {
		t.Errorf("clock_anomalies = %d, want 1", h.ClockAnomalies)
	}
}

func TestDayHealthOfflineGapOnlyWhenL0Present(t *testing.T) {
	loc := time.UTC
	day := at(0).In(loc).Format("2006-01-02")
	// 有 L0：10:00 醒，10:30 睡；事件在 10:00 与 10:45 → 10:30~10:45 为离机缺口。
	events := []presenceSource{
		{EventID: "w", Type: TypeSystemWake, OccurredAt: at(0), WakeEpochID: "w1"},
		{EventID: "s", Type: TypeSystemSleep, OccurredAt: at(30 * time.Minute), WakeEpochID: "w1"},
		{EventID: "e1", Type: TypeToolCallStarted, OccurredAt: at(0)},
		{EventID: "e2", Type: TypeToolCallStarted, OccurredAt: at(45 * time.Minute)},
	}
	gaps := computeOfflineGaps(events, loc)
	if len(gaps[day]) != 1 {
		t.Errorf("offline gaps = %v, want 1", gaps[day])
	}
	// 无 L0：不报缺口（N/A）。
	gaps2 := computeOfflineGaps([]presenceSource{{EventID: "e", Type: TypeToolCallStarted, OccurredAt: at(0)}}, loc)
	if len(gaps2[day]) != 0 {
		t.Errorf("no L0 should report no gaps, got %v", gaps2[day])
	}
}

// 只有点事件、没有闭合区间的日子也要有 day_health 行（否则 DaySkeleton.Health 为 nil、离机缺口被丢）。
func TestDayHealthIncludesPointEventOnlyDays(t *testing.T) {
	loc := time.UTC
	day := at(0).In(loc).Format("2006-01-02")
	events := []presenceSource{
		{EventID: "e1", Type: TypeDocCreated, OccurredAt: at(5 * time.Minute)},
	}
	rows := computeDayHealth(nil, events, nil, loc)
	if len(rows) != 1 || rows[0].Day != day {
		t.Fatalf("health rows = %+v, want 1 row for %s", rows, day)
	}
	// 纯 read.* 元事件不产生体检行。
	meta := []presenceSource{{EventID: "m", Type: "read.tool", OccurredAt: at(5 * time.Minute)}}
	if rows := computeDayHealth(nil, meta, nil, loc); len(rows) != 0 {
		t.Errorf("meta-only day should have no health row, got %+v", rows)
	}
}

func TestRebuildDayStatsWritesRows(t *testing.T) {
	save := sessionDayLoc
	defer func() { sessionDayLoc = save }()
	sessionDayLoc = time.UTC

	s := openTest(t)
	s.Emit(Event{EventID: "s1", Type: TypeToolCallStarted, PairID: "e1", OccurredAt: at(0)})
	s.Emit(Event{EventID: "e1", Type: TypeToolCallEnded, PairID: "s1", OccurredAt: at(2 * time.Second)})
	s.Flush()
	if err := s.RebuildIntervals(); err != nil {
		t.Fatalf("rebuild intervals: %v", err)
	}
	if err := s.RebuildDayStats(); err != nil {
		t.Fatalf("rebuild day stats: %v", err)
	}
	if n := count(t, s, `SELECT count(*) FROM day_stats WHERE type='tool.call.started'`); n != 1 {
		t.Errorf("day_stats rows = %d, want 1", n)
	}
	if n := count(t, s, `SELECT count(*) FROM day_health`); n != 1 {
		t.Errorf("day_health rows = %d, want 1", n)
	}
}

// 端到端验 scanClockAnomalies：同一批（received_at 相同）内 occurred_at 回拨记 1 次。
// 修的是「事件表按 occurred_at 升序读入 → 逐条比大小恒为 false，clock_anomalies 永远是 0」。
func TestScanClockAnomaliesDetectsBatchRollback(t *testing.T) {
	save := sessionDayLoc
	defer func() { sessionDayLoc = save }()
	sessionDayLoc = time.UTC

	s := openTest(t)
	_, err := s.IngestBatch("seg1", []BatchEvent{
		{EventID: "ca1", Type: TypeToolCallStarted, OccurredAt: at(10 * time.Minute).UTC().Format(isoMillis)},
		{EventID: "ca2", Type: TypeToolCallStarted, OccurredAt: at(5 * time.Minute).UTC().Format(isoMillis)},
	})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	clock, err := scanClockAnomalies(s.db, time.UTC)
	if err != nil {
		t.Fatalf("scanClockAnomalies: %v", err)
	}
	day := at(0).In(time.UTC).Format("2006-01-02")
	if clock[day] != 1 {
		t.Errorf("clock anomalies = %d, want 1 (%v)", clock[day], clock)
	}
}
