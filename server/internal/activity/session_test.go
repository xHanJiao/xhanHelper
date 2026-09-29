package activity

import (
	"testing"
	"time"
)

// presence 造一个在场事件（测试用）。
func presence(id, typ string, off time.Duration, anchor string) presenceSource {
	e := presenceSource{EventID: id, Type: typ, OccurredAt: at(off)}
	switch typ {
	case TypeSystemWake, TypeSystemSleep:
		e.WakeEpochID = anchor
	case TypeClientAppLaunched, TypeClientAppQuit:
		e.LaunchID = anchor
	case TypeClientFocusGained, TypeClientFocusLost:
		e.FocusEpochID = anchor
	case TypeComponentOpened, TypeComponentClosed:
		e.ComponentInstanceID = anchor
	}
	return e
}

func TestSessionL2PairedByFocusEpoch(t *testing.T) {
	events := []presenceSource{
		presence("f1", TypeClientFocusGained, 0, "fp1"),
		presence("f2", TypeClientFocusLost, 30*time.Second, "fp1"),
	}
	raw := computeRawSessions(events, nil)
	got := findRaw(raw, 2, "fp1")
	if got == nil {
		t.Fatalf("no L2 session for fp1 in %+v", raw)
	}
	if !got.end.Equal(at(30*time.Second)) || got.closedBy != ClosedPaired {
		t.Errorf("L2 = %+v", got)
	}
}

func TestSessionL1LaunchToQuit(t *testing.T) {
	events := []presenceSource{
		presence("l1", TypeClientAppLaunched, 0, "launchA"),
		presence("l2", TypeClientAppQuit, 2*time.Hour, "launchA"),
	}
	raw := computeRawSessions(events, nil)
	got := findRaw(raw, 1, "launchA")
	if got == nil || !got.end.Equal(at(2*time.Hour)) {
		t.Fatalf("L1 = %+v", got)
	}
}

func TestSessionMissingEndFallsToBoundaryThenFallback(t *testing.T) {
	// 无 end，但有 boundary → boundary。
	events := []presenceSource{
		presence("f1", TypeClientFocusGained, 0, "fp1"),
		presence("b1", boundaryType, 10*time.Second, ""),
	}
	raw := computeRawSessions(events, boundaryTimes(toSourceEvents(events)))
	if got := findRaw(raw, 2, "fp1"); got == nil || got.closedBy != ClosedBoundary {
		t.Errorf("L2 should close by boundary: %+v", got)
	}
	// 无 end、无 boundary → fallback。
	raw2 := computeRawSessions([]presenceSource{presence("f2", TypeClientFocusGained, 0, "fp2")}, nil)
	if got := findRaw(raw2, 2, "fp2"); got == nil || got.closedBy != ClosedFallback {
		t.Errorf("L2 should fallback: %+v", got)
	}
}

func TestSessionL3DebounceZeroEqualsL2(t *testing.T) {
	// 去抖=0：两次紧邻的 L2（间隙 1s）也各自成段，L3 段数 == L2 段数。
	events := []presenceSource{
		presence("a1", TypeClientFocusGained, 0, "e1"),
		presence("a2", TypeClientFocusLost, 10*time.Second, "e1"),
		presence("b1", TypeClientFocusGained, 11*time.Second, "e2"),
		presence("b2", TypeClientFocusLost, 20*time.Second, "e2"),
	}
	raw := computeRawSessions(events, nil)
	l2, l3 := 0, 0
	for _, r := range raw {
		if r.level == 2 {
			l2++
		}
		if r.level == 3 {
			l3++
		}
	}
	if l2 != 2 || l3 != 2 {
		t.Errorf("debounce=0: L2=%d L3=%d, want 2/2", l2, l3)
	}
}

func TestSplitByDayAtMidnight(t *testing.T) {
	// 23:00 → 次日 01:00（本地日），应切成两段。
	save := sessionDayLoc
	defer func() { sessionDayLoc = save }()
	sessionDayLoc = time.UTC

	start := time.Date(2026, 9, 27, 23, 0, 0, 0, time.UTC)
	end := time.Date(2026, 9, 28, 1, 0, 0, 0, time.UTC)
	segs := splitByDay(rawSession{level: 2, anchor: "x", start: start, end: end, closedBy: ClosedPaired}, time.UTC)
	if len(segs) != 2 {
		t.Fatalf("split segs = %d, want 2: %+v", len(segs), segs)
	}
	if !segs[0].end.Equal(time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("first seg end = %v, want midnight", segs[0].end)
	}
	if segs[1].closedBy != ClosedPaired {
		t.Errorf("second seg should keep original closedBy, got %s", segs[1].closedBy)
	}
}

func TestAssignParentsContainment(t *testing.T) {
	// L0 包 L1 包 L2，parent 各自指向外层。
	mk := func(level int, sid string, st, en time.Duration) sessionRow {
		e := at(en).Format(isoMillis)
		return sessionRow{SessionID: sid, Level: level, StartedAt: at(st).Format(isoMillis), EndedAt: &e, ComputedAt: at(0).Format(isoMillis)}
	}
	rows := []sessionRow{
		mk(0, "L0", 0, 2*time.Hour),
		mk(1, "L1", 10*time.Minute, 90*time.Minute),
		mk(2, "L2", 20*time.Minute, 40*time.Minute),
	}
	assignParents(rows)
	if rows[0].ParentID != nil {
		t.Errorf("L0 should have no parent")
	}
	if rows[1].ParentID == nil || *rows[1].ParentID != "L0" {
		t.Errorf("L1 parent = %v, want L0", rows[1].ParentID)
	}
	if rows[2].ParentID == nil || *rows[2].ParentID != "L1" {
		t.Errorf("L2 parent = %v, want L1", rows[2].ParentID)
	}
}

func TestRebuildSessionsWritesRowsAndMapping(t *testing.T) {
	save := sessionDayLoc
	defer func() { sessionDayLoc = save }()
	sessionDayLoc = time.UTC

	s := openTest(t)
	// L2 一段 + 一条落在里面的普通事件（tool.call）→ 应有 event_session 映射。
	s.Emit(Event{EventID: "f1", Type: TypeClientFocusGained, FocusEpochID: "fp1", OccurredAt: at(0)})
	s.Emit(Event{EventID: "tk", Type: TypeToolCallStarted, OccurredAt: at(5 * time.Second)})
	s.Emit(Event{EventID: "f2", Type: TypeClientFocusLost, FocusEpochID: "fp1", OccurredAt: at(30 * time.Second)})
	s.Flush()

	if err := s.RebuildSessions(); err != nil {
		t.Fatalf("rebuild sessions: %v", err)
	}
	if n := count(t, s, `SELECT count(*) FROM sessions WHERE level=2`); n != 1 {
		t.Errorf("L2 sessions = %d, want 1", n)
	}
	// 去抖=0 → L3 与 L2 同段。
	if n := count(t, s, `SELECT count(*) FROM sessions WHERE level=3`); n != 1 {
		t.Errorf("L3 sessions = %d, want 1", n)
	}
	if n := count(t, s, `SELECT count(*) FROM event_session WHERE event_id='tk' AND level=2`); n != 1 {
		t.Errorf("tool event should map to L2 session")
	}
}

func TestLevelSessionsDedupesRepeatedStart(t *testing.T) {
	// 同锚重复 start（重投/epoch 复用）→ 只产一段，不产生重复 session_id（否则主键冲突）。
	events := []presenceSource{
		presence("f1", TypeClientFocusGained, 0, "fp1"),
		presence("f1b", TypeClientFocusGained, 10*time.Second, "fp1"), // 同锚再起
		presence("f2", TypeClientFocusLost, 30*time.Second, "fp1"),
	}
	raw := computeRawSessions(events, nil)
	n := 0
	for _, r := range raw {
		if r.level == 2 {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("repeated start produced %d L2 sessions, want 1", n)
	}
	// 覆盖为最后一段（start = +10s）。
	if got := findRaw(raw, 2, "fp1"); got == nil || !got.start.Equal(at(10*time.Second)) {
		t.Errorf("dedup should keep last segment, got %+v", got)
	}
}

func TestRebuildSessionsRepeatedStartNoCrash(t *testing.T) {
	s := openTest(t)
	s.Emit(Event{EventID: "f1", Type: TypeClientFocusGained, FocusEpochID: "fp1", OccurredAt: at(0)})
	s.Emit(Event{EventID: "f1b", Type: TypeClientFocusGained, FocusEpochID: "fp1", OccurredAt: at(10 * time.Second)})
	s.Emit(Event{EventID: "f2", Type: TypeClientFocusLost, FocusEpochID: "fp1", OccurredAt: at(30 * time.Second)})
	s.Flush()
	if err := s.RebuildSessions(); err != nil {
		t.Fatalf("rebuild with repeated start: %v", err)
	}
	if n := count(t, s, `SELECT count(*) FROM sessions WHERE level=2`); n != 1 {
		t.Errorf("L2 sessions = %d, want 1", n)
	}
}

// findRaw 在 raw 里找指定 level + anchor 的一段。
func findRaw(raw []rawSession, level int, anchor string) *rawSession {
	for i := range raw {
		if raw[i].level == level && raw[i].anchor == anchor {
			return &raw[i]
		}
	}
	return nil
}
