package activity

import (
	"testing"
	"time"
)

func TestRecomputeStampAndNeeds(t *testing.T) {
	s := openTest(t)
	// 首启：从未算过 → 需要重算。
	if need, err := s.NeedsRecompute(); err != nil || !need {
		t.Fatalf("fresh store NeedsRecompute = %v,%v want true", need, err)
	}
	if err := s.Recompute(); err != nil {
		t.Fatalf("recompute: %v", err)
	}
	if need, err := s.NeedsRecompute(); err != nil || need {
		t.Fatalf("after recompute NeedsRecompute = %v,%v want false", need, err)
	}
	if v, ok, err := s.GetMeta("last_recompute_at"); err != nil || !ok || v == "" {
		t.Errorf("last_recompute_at = %q ok=%v err=%v", v, ok, err)
	}
}

func TestRecomputeDerivesAllTables(t *testing.T) {
	save := sessionDayLoc
	defer func() { sessionDayLoc = save }()
	sessionDayLoc = time.UTC

	s := openTest(t)
	// 一对工具区间 + 一段 L2 在场，应同时产出 interval/session/day_stats。
	s.Emit(Event{EventID: "s1", Type: TypeToolCallStarted, PairID: "e1", OccurredAt: at(0)})
	s.Emit(Event{EventID: "e1", Type: TypeToolCallEnded, PairID: "s1", OccurredAt: at(2 * time.Second)})
	s.Emit(Event{EventID: "f1", Type: TypeClientFocusGained, FocusEpochID: "fp1", OccurredAt: at(0)})
	s.Emit(Event{EventID: "f2", Type: TypeClientFocusLost, FocusEpochID: "fp1", OccurredAt: at(time.Minute)})
	s.Flush()

	if err := s.Recompute(); err != nil {
		t.Fatalf("recompute: %v", err)
	}
	for _, tbl := range []string{"interval_closed", "sessions", "day_stats", "day_health"} {
		if n := count(t, s, `SELECT count(*) FROM `+tbl); n == 0 {
			t.Errorf("%s empty after recompute", tbl)
		}
	}
}

func TestRecomputeHoldsWriteGate(t *testing.T) {
	s := openTest(t)
	// 模拟在途写（IngestBatch/emit 持读锁）：Recompute 必须等它放锁才开始。
	s.writeMu.RLock()
	done := make(chan error, 1)
	go func() { done <- s.Recompute() }()
	select {
	case <-done:
		t.Fatalf("Recompute finished while a write lock was held")
	case <-time.After(50 * time.Millisecond):
	}
	s.writeMu.RUnlock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Recompute after release: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("Recompute did not finish after lock released")
	}
}

func TestRecomputePausesIngestThenFlushes(t *testing.T) {
	s := openTest(t)
	if err := s.Recompute(); err != nil {
		t.Fatalf("recompute: %v", err)
	}
	// 重算期间 emitter 处于暂停；结束后 emit 仍能正常落库（resume 生效）。
	s.Emit(Event{EventID: "x1", Type: TypeToolCallStarted, OccurredAt: at(0)})
	s.Flush()
	if n := countEvents(t, s); n != 1 {
		t.Errorf("events after recompute = %d, want 1", n)
	}
}
