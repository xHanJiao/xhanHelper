package activity

import (
	"testing"
	"time"
)

// TestCleanupBeforeRemovesExpired 到期事件与其派生边一并删，新事件保留。
func TestCleanupBeforeRemovesExpired(t *testing.T) {
	s := openTest(t)
	defer s.Close()

	old := at(-100 * 24 * time.Hour)
	fresh := at(-time.Hour)
	s.Emit(Event{EventID: "old1", Type: TypeBrowserNavStarted, OccurredAt: old, Actor: ActorUser,
		Attrs: map[string]any{"url": "https://old.com/a"}})
	s.Emit(Event{EventID: "new1", Type: TypeBrowserNavStarted, OccurredAt: fresh, Actor: ActorUser,
		Attrs: map[string]any{"url": "https://new.com/b"}})
	s.Flush()
	if err := s.RebuildResources(); err != nil {
		t.Fatalf("RebuildResources: %v", err)
	}

	n, err := s.CleanupBefore(at(-90 * 24 * time.Hour))
	if err != nil {
		t.Fatalf("CleanupBefore: %v", err)
	}
	if n != 1 {
		t.Errorf("deleted = %d, want 1", n)
	}
	if got := countEvents(t, s); got != 1 {
		t.Errorf("剩余 events = %d, want 1", got)
	}
	var orphan int64
	if err := s.DB().Raw(`SELECT count(*) FROM event_resources WHERE event_id = 'old1'`).Scan(&orphan).Error; err != nil {
		t.Fatalf("query: %v", err)
	}
	if orphan != 0 {
		t.Errorf("old1 的 event_resources 应被清，剩 %d", orphan)
	}
}

// TestCleanupExpiredDisabled retentionDays<=0 不清理。
func TestCleanupExpiredDisabled(t *testing.T) {
	s := openTest(t)
	defer s.Close()

	s.Emit(Event{EventID: "old1", Type: TypeBrowserNavStarted, OccurredAt: at(-200 * 24 * time.Hour), Actor: ActorUser})
	s.Flush()
	if n, err := s.CleanupExpired(0); err != nil || n != 0 {
		t.Errorf("CleanupExpired(0) = (%d,%v), want (0,nil)", n, err)
	}
	if got := countEvents(t, s); got != 1 {
		t.Errorf("events = %d, want 1（未清理）", got)
	}
}

// TestCleanupResource 按资源清映射，不动 events。
func TestCleanupResource(t *testing.T) {
	s := openTest(t)
	defer s.Close()

	s.Emit(Event{EventID: "e1", Type: TypeBrowserNavStarted, OccurredAt: at(0), Actor: ActorUser,
		Attrs: map[string]any{"url": "https://gone.com/a"}})
	s.Flush()
	if err := s.RebuildResources(); err != nil {
		t.Fatalf("RebuildResources: %v", err)
	}
	if _, err := s.CleanupResource(ResourceKindURL, "https://gone.com/a"); err != nil {
		t.Fatalf("CleanupResource: %v", err)
	}
	var edges, res int64
	if err := s.DB().Raw(`SELECT count(*) FROM event_resources`).Scan(&edges).Error; err != nil {
		t.Fatalf("query edges: %v", err)
	}
	if err := s.DB().Raw(`SELECT count(*) FROM resources`).Scan(&res).Error; err != nil {
		t.Fatalf("query resources: %v", err)
	}
	if edges != 0 || res != 0 {
		t.Errorf("清后 edges=%d res=%d, want 0/0", edges, res)
	}
	if got := countEvents(t, s); got != 1 {
		t.Errorf("events = %d, want 1（资源清理不动事实层）", got)
	}
}

// TestCleanupBeforeRemovesDerivedTables 派生表（sessions/day_stats/day_health/interval_closed/event_session）随到期一并清。
func TestCleanupBeforeRemovesDerivedTables(t *testing.T) {
	s := openTest(t)
	defer s.Close()
	save := sessionDayLoc
	sessionDayLoc = time.UTC
	defer func() { sessionDayLoc = save }()

	old := at(-100 * 24 * time.Hour)
	// 一对工具区间 + 一条会进 day 统计的事件。
	s.Emit(Event{EventID: "ts", Type: TypeToolCallStarted, PairID: "te", OccurredAt: old, Actor: ActorAssistant})
	s.Emit(Event{EventID: "te", Type: TypeToolCallEnded, PairID: "ts", OccurredAt: old.Add(time.Minute), Actor: ActorAssistant})
	s.Flush()
	if err := s.Recompute(); err != nil {
		t.Fatalf("Recompute: %v", err)
	}
	// 前置确认派生有东西。
	count := func(table string) int64 {
		var n int64
		if err := s.DB().Raw(`SELECT count(*) FROM ` + table).Scan(&n).Error; err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		return n
	}
	if count("day_stats") == 0 || count("interval_closed") == 0 {
		t.Fatal("前置：派生表应有数据")
	}

	if _, err := s.CleanupBefore(at(-90 * 24 * time.Hour)); err != nil {
		t.Fatalf("CleanupBefore: %v", err)
	}
	for _, table := range []string{"day_stats", "day_health", "interval_closed", "event_session", "sessions"} {
		if n := count(table); n != 0 {
			t.Errorf("清理后 %s = %d, want 0", table, n)
		}
	}
}
