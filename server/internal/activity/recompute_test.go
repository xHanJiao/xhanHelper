package activity

import (
	"testing"
	"time"
)

func TestRecomputeWritesObservabilityMeta(t *testing.T) {
	s := openTest(t)
	if err := s.Recompute(); err != nil {
		t.Fatalf("recompute: %v", err)
	}
	if v, ok, err := s.GetMeta(recomputeMetaKey); err != nil || !ok || v != recomputeStamp() {
		t.Errorf("recompute_stamp = %q ok=%v err=%v want %q", v, ok, err, recomputeStamp())
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

// TestRecomputeDue 启动补算的门槛：没算过 / 时间戳坏掉 / 超过 maxAge 都算过期，
// 刚算完的算新鲜（回归：每次进程启动都全量重建，部署后必有一次读 IO 尖峰）。
func TestRecomputeDue(t *testing.T) {
	s := openTest(t)

	// 从没算过 → 过期。
	if due, err := s.RecomputeDue(24 * time.Hour); err != nil || !due {
		t.Fatalf("首次 RecomputeDue = %v/%v，want true/nil", due, err)
	}

	// 刚算完 → 新鲜。
	if err := s.Recompute(); err != nil {
		t.Fatalf("recompute: %v", err)
	}
	if due, err := s.RecomputeDue(24 * time.Hour); err != nil || due {
		t.Fatalf("刚算完 RecomputeDue = %v/%v，want false/nil", due, err)
	}

	// 规则版本变了 → 即便刚算完也算过期（派生层不能带着旧规则的产物继续服务）。
	if err := s.SetMeta(recomputeMetaKey, "r0/t0/n0/f0"); err != nil {
		t.Fatalf("SetMeta stamp: %v", err)
	}
	if due, err := s.RecomputeDue(24 * time.Hour); err != nil || !due {
		t.Fatalf("规则版本变化 RecomputeDue = %v/%v，want true/nil", due, err)
	}
	if err := s.Recompute(); err != nil {
		t.Fatalf("recompute: %v", err)
	}

	// 时间戳退到 25 小时前 → 过期。
	old := time.Now().Add(-25 * time.Hour).UTC().Format(time.RFC3339)
	if err := s.SetMeta("last_recompute_at", old); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}
	if due, err := s.RecomputeDue(24 * time.Hour); err != nil || !due {
		t.Fatalf("25h 前 RecomputeDue = %v/%v，want true/nil", due, err)
	}

	// 时间戳坏掉 → 按过期处理（重算会覆盖成合法值）。
	if err := s.SetMeta("last_recompute_at", "not-a-time"); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}
	if due, err := s.RecomputeDue(24 * time.Hour); err != nil || !due {
		t.Fatalf("坏时间戳 RecomputeDue = %v/%v，want true/nil", due, err)
	}
}
