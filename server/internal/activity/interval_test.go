package activity

import (
	"strconv"
	"testing"
	"time"
)

// t0 是测试基准时刻；所有事件相对它偏移，保证排序稳定可预期。
var t0 = time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)

func at(offset time.Duration) time.Time { return t0.Add(offset) }

func ptr(s string) *string { return &s }

func ev(id, typ string, off time.Duration, pairID *string) sourceEvent {
	return sourceEvent{EventID: id, Type: typ, OccurredAt: at(off), PairID: pairID}
}

// closeOneOn 用一组事件跑闭合，返回以 startID 为首键的行。
func closeOneOn(t *testing.T, events []sourceEvent, startID string) intervalRow {
	t.Helper()
	rows := computeIntervals(events)
	for _, r := range rows {
		if r.StartEventID == startID {
			return r
		}
	}
	t.Fatalf("no interval_closed row for start %s (got %d rows)", startID, len(rows))
	return intervalRow{}
}

func TestClosePairedServerStyle(t *testing.T) {
	// 服务端：两端预分配 event_id 互指，pair_id == 对端 event_id。
	events := []sourceEvent{
		ev("s1", TypeToolCallStarted, 0, ptr("e1")),
		ev("e1", TypeToolCallEnded, 2*time.Second, ptr("s1")),
	}
	r := closeOneOn(t, events, "s1")
	if r.ClosedBy != ClosedPaired || r.EndEventID == nil || *r.EndEventID != "e1" {
		t.Errorf("paired row = %+v", r)
	}
	if r.DurationMs != 2000 {
		t.Errorf("duration = %d, want 2000", r.DurationMs)
	}
}

func TestClosePairedClientStyleSharedUUID(t *testing.T) {
	// 客户端：两端 pair_id 是同一条共享 UUID，不等于任一 event_id。
	shared := "pair-uuid"
	events := []sourceEvent{
		ev("s1", "browser.nav.started", 0, ptr(shared)),
		ev("e1", "browser.nav.ended", 1500*time.Millisecond, ptr(shared)),
	}
	r := closeOneOn(t, events, "s1")
	if r.ClosedBy != ClosedPaired || r.EndEventID == nil || *r.EndEventID != "e1" {
		t.Errorf("shared-uuid paired row = %+v", r)
	}
}

func TestClosePairIDWithoutMatchingEndFallsThrough(t *testing.T) {
	// 服务端预分配 pair_id 指向的对端永远没来（进程被杀）→ 不能算 paired。
	events := []sourceEvent{
		ev("s1", TypeToolCallStarted, 0, ptr("never-arrives")),
	}
	r := closeOneOn(t, events, "s1")
	if r.ClosedBy != ClosedFallback {
		t.Errorf("dangling pair_id should fallback, got %s", r.ClosedBy)
	}
	if r.DurationMs != intervalFallbackDuration.Milliseconds() {
		t.Errorf("fallback duration = %d", r.DurationMs)
	}
}

func TestCloseBoundaryTruncates(t *testing.T) {
	events := []sourceEvent{
		ev("s1", TypeToolCallStarted, 0, nil),
		ev("b1", boundaryType, 3*time.Second, nil),
	}
	r := closeOneOn(t, events, "s1")
	if r.ClosedBy != ClosedBoundary || r.EndEventID != nil {
		t.Errorf("boundary row = %+v", r)
	}
	if r.DurationMs != 3000 {
		t.Errorf("boundary duration = %d, want 3000", r.DurationMs)
	}
}

func TestCloseBoundaryOnlyAfterStart(t *testing.T) {
	// boundary 出现在 start 之前：不该拿它闭合。
	events := []sourceEvent{
		ev("b1", boundaryType, -5*time.Second, nil),
		ev("s1", TypeToolCallStarted, 0, nil),
	}
	r := closeOneOn(t, events, "s1")
	if r.ClosedBy != ClosedFallback {
		t.Errorf("boundary before start must not close: %s", r.ClosedBy)
	}
}

func TestCloseInferredUsesP95(t *testing.T) {
	// 造 5 条已配对样本（时长 1..5s），再一条无配对的 start → inferred = start + p95。
	var events []sourceEvent
	durations := []int{1, 2, 3, 4, 5}
	for i, d := range durations {
		sid, eid := "ps"+string(rune('a'+i)), "pe"+string(rune('a'+i))
		events = append(events,
			ev(sid, TypeToolCallStarted, time.Duration(i)*time.Minute, ptr(eid)),
			ev(eid, TypeToolCallEnded, time.Duration(i)*time.Minute+time.Duration(d)*time.Second, ptr(sid)),
		)
	}
	events = append(events, ev("orphan", TypeToolCallStarted, 10*time.Minute, nil))
	r := closeOneOn(t, events, "orphan")
	if r.ClosedBy != ClosedInferred {
		t.Fatalf("expected inferred, got %s", r.ClosedBy)
	}
	// p95（最近秩法，n=5，idx=ceil(4.75)-1=4 → 5s）
	if r.DurationMs != 5000 {
		t.Errorf("inferred duration = %d, want 5000 (p95)", r.DurationMs)
	}
}

func TestCloseInferredBelowSampleThresholdFallsBack(t *testing.T) {
	// 只有 4 条样本（< intervalMinSamples=5）→ fallback。
	var events []sourceEvent
	for i := 0; i < 4; i++ {
		sid, eid := "ps"+string(rune('a'+i)), "pe"+string(rune('a'+i))
		events = append(events,
			ev(sid, TypeToolCallStarted, time.Duration(i)*time.Minute, ptr(eid)),
			ev(eid, TypeToolCallEnded, time.Duration(i)*time.Minute+time.Second, ptr(sid)),
		)
	}
	events = append(events, ev("orphan", TypeToolCallStarted, 10*time.Minute, nil))
	r := closeOneOn(t, events, "orphan")
	if r.ClosedBy != ClosedFallback {
		t.Errorf("below threshold should fallback, got %s", r.ClosedBy)
	}
}

func TestCloseSupersededOverridesHanging(t *testing.T) {
	// 前一条工具调用悬挂（fallback 会闭合到 +5min），后面同类型新 start 在 +10s →
	// 前一条改判 superseded，end = 新 start - 1ms.
	events := []sourceEvent{
		ev("s1", TypeToolCallStarted, 0, nil),
		ev("s2", TypeToolCallStarted, 10*time.Second, nil),
	}
	r := closeOneOn(t, events, "s1")
	if r.ClosedBy != ClosedSuperseded {
		t.Fatalf("expected superseded, got %s", r.ClosedBy)
	}
	if r.DurationMs != (10*time.Second - intervalSupersededEpsilon).Milliseconds() {
		t.Errorf("superseded duration = %d", r.DurationMs)
	}
}

func TestCloseSupersededDoesNotStealRealPair(t *testing.T) {
	// 并发场景：A 与 B 同类型重叠，A 有真实 paired end（晚于 B.start）。
	// superseded 不该抢走 A 的 paired——否则 end_event_id 永久丢失。
	events := []sourceEvent{
		ev("sa", TypeToolCallStarted, 0, ptr("ea")),
		ev("sb", TypeToolCallStarted, 1*time.Second, ptr("eb")),
		ev("ea", TypeToolCallEnded, 5*time.Second, ptr("sa")),
		ev("eb", TypeToolCallEnded, 6*time.Second, ptr("sb")),
	}
	a := closeOneOn(t, events, "sa")
	if a.ClosedBy != ClosedPaired || a.EndEventID == nil || *a.EndEventID != "ea" {
		t.Errorf("concurrent overlap must keep paired: %+v", a)
	}
	if a.DurationMs != 5000 {
		t.Errorf("A duration = %d, want 5000", a.DurationMs)
	}
	b := closeOneOn(t, events, "sb")
	if b.ClosedBy != ClosedPaired || b.EndEventID == nil || *b.EndEventID != "eb" {
		t.Errorf("B paired = %+v", b)
	}
}

func TestCloseSupersededDoesNotStealBoundary(t *testing.T) {
	// A 有 boundary 闭合（+2s），同类型 B 在 +1s（早于 A 的 end）。boundary 不该被 superseded 抢。
	events := []sourceEvent{
		ev("sa", TypeToolCallStarted, 0, nil),
		ev("ba", boundaryType, 2*time.Second, nil),
		ev("sb", TypeToolCallStarted, 1*time.Second, nil),
	}
	a := closeOneOn(t, events, "sa")
	if a.ClosedBy != ClosedBoundary {
		t.Errorf("boundary must not be superseded, got %s", a.ClosedBy)
	}
	if a.DurationMs != 2000 {
		t.Errorf("A boundary duration = %d, want 2000", a.DurationMs)
	}
}

func TestClosePairedEndBeforeStartNotPaired(t *testing.T) {
	// 时钟回拨：配对的 end 时刻早于 start → 不算 paired，落到后续档位（这里 boundary）。
	shared := "p1"
	events := []sourceEvent{
		ev("s1", "browser.nav.started", 10*time.Second, ptr(shared)),
		ev("e1", "browser.nav.ended", 5*time.Second, ptr(shared)),
		ev("b1", boundaryType, 12*time.Second, nil),
	}
	r := closeOneOn(t, events, "s1")
	if r.ClosedBy == ClosedPaired {
		t.Errorf("end before start must not be paired: %+v", r)
	}
	if r.ClosedBy != ClosedBoundary {
		t.Errorf("should fall to boundary, got %s", r.ClosedBy)
	}
}

func TestRebuildIntervalsChunksLargeInsert(t *testing.T) {
	// 造 >5000 条 interval（远超单条 INSERT 的 ~4680 行变量上限），验证分块插入不炸。
	// 直接落库（绕开 emit 的有界队列，那会丢事件），顺带覆盖 eventRow 的分块插入。
	s := openTest(t)
	const n = 6000
	rows := make([]eventRow, 0, 2*n)
	for i := 0; i < n; i++ {
		off := time.Duration(i) * time.Second
		rows = append(rows,
			eventRow{EventID: "ts" + itoa(i), Type: TypeToolCallStarted,
				OccurredAt: at(off).Format(isoMillis), ReceivedAt: at(off).Format(isoMillis),
				Actor: ActorSystem, Trigger: TriggerManual, SchemaVersion: schemaVersion},
			eventRow{EventID: "te" + itoa(i), Type: TypeToolCallEnded,
				OccurredAt: at(off + 500*time.Millisecond).Format(isoMillis), ReceivedAt: at(off).Format(isoMillis),
				Actor: ActorSystem, Trigger: TriggerManual, SchemaVersion: schemaVersion},
		)
	}
	if err := s.insertEvents(rows); err != nil {
		t.Fatalf("insert events: %v", err)
	}
	if err := s.RebuildIntervals(); err != nil {
		t.Fatalf("rebuild large: %v", err)
	}
	if got := count(t, s, `SELECT count(*) FROM interval_closed`); got != n {
		t.Errorf("interval_closed = %d, want %d", got, n)
	}
}

func itoa(i int) string {
	return strconv.Itoa(i)
}

func TestCloseCapsAt24Hours(t *testing.T) {
	// 悬挂 start + 同类型后面没有新 start + 有极远 boundary：仍受 24h 上限。
	events := []sourceEvent{
		ev("s1", TypeToolCallStarted, 0, nil),
		ev("b1", boundaryType, 48*time.Hour, nil),
	}
	r := closeOneOn(t, events, "s1")
	if r.DurationMs != intervalMaxDuration.Milliseconds() {
		t.Errorf("duration = %d, want 24h cap %d", r.DurationMs, intervalMaxDuration.Milliseconds())
	}
	if r.ClosedBy != ClosedFallback {
		t.Errorf("capped-by-24h should be marked fallback, got %s", r.ClosedBy)
	}
}

func TestRebuildIntervalsWritesRowsAndVersion(t *testing.T) {
	s := openTest(t)
	s.Emit(Event{EventID: "s1", Type: TypeToolCallStarted, PairID: "e1", OccurredAt: at(0),
		Attrs: map[string]any{"tool_name": "bookkeeping__create"}})
	s.Emit(Event{EventID: "e1", Type: TypeToolCallEnded, PairID: "s1", OccurredAt: at(2 * time.Second)})
	s.Flush()

	if err := s.RebuildIntervals(); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if n := count(t, s, `SELECT count(*) FROM interval_closed`); n != 1 {
		t.Errorf("interval_closed rows = %d, want 1", n)
	}
	var cb string
	if err := s.db.Raw(`SELECT closed_by FROM interval_closed WHERE start_event_id='s1'`).Scan(&cb).Error; err != nil {
		t.Fatalf("scan closed_by: %v", err)
	}
	if cb != ClosedPaired {
		t.Errorf("closed_by = %q, want paired", cb)
	}
	if v, ok, err := s.GetMeta("rule_version"); err != nil || !ok || v != "1" {
		t.Errorf("meta rule_version = %q ok=%v err=%v", v, ok, err)
	}
}
