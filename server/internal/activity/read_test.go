package activity

import (
	"testing"
	"time"
)

// TestDaySkeletonReadsDerived 端到端：灌事件 → Recompute → DaySkeleton 出计数/资源/体检。
func TestDaySkeletonReadsDerived(t *testing.T) {
	s := openTest(t)
	defer s.Close()

	save := sessionDayLoc
	sessionDayLoc = time.UTC
	defer func() { sessionDayLoc = save }()

	// 一条工具区间（配对着闭合）+ 一条带 doc 资源的事件。
	s.Emit(Event{EventID: "s1", Type: TypeToolCallStarted, PairID: "e1", OccurredAt: at(0),
		Attrs: map[string]any{"tool_name": "bookkeeping__create"}})
	s.Emit(Event{EventID: "e1", Type: TypeToolCallEnded, PairID: "s1", OccurredAt: at(2 * time.Minute)})
	s.Emit(Event{EventID: "d1", Type: "doc.created", OccurredAt: at(3 * time.Minute),
		Attrs: map[string]any{"doc_id": "doc-123"}})
	s.Flush()
	if err := s.Recompute(); err != nil {
		t.Fatalf("recompute: %v", err)
	}

	day := at(0).In(time.UTC).Format("2006-01-02")
	sk, err := s.DaySkeleton(day)
	if err != nil {
		t.Fatalf("DaySkeleton: %v", err)
	}
	if len(sk.Hours) == 0 {
		t.Fatal("Hours 为空，期望有 tool/doc 计数")
	}
	var toolCnt int64
	for _, h := range sk.Hours {
		if h.Source == "tool" {
			toolCnt += h.Cnt
		}
	}
	if toolCnt == 0 {
		t.Errorf("tool 小时计数为 0，Hours=%+v", sk.Hours)
	}
	foundDoc := false
	for _, r := range sk.Resources {
		if r.Kind == ResourceKindDoc && r.ResourceID == "doc-123" {
			foundDoc = true
		}
	}
	if !foundDoc {
		t.Errorf("资源清单缺 doc-123，Resources=%+v", sk.Resources)
	}
	if sk.Health == nil {
		t.Error("Health 为 nil，Recompute 后应有体检行")
	}
}

// TestDaySkeletonNoData 无数据的日期：空集合、不报错、Health 为 nil。
func TestDaySkeletonNoData(t *testing.T) {
	s := openTest(t)
	defer s.Close()

	sk, err := s.DaySkeleton("2026-01-01")
	if err != nil {
		t.Fatalf("DaySkeleton: %v", err)
	}
	if len(sk.Hours) != 0 || len(sk.Sessions) != 0 || len(sk.Resources) != 0 {
		t.Errorf("期望空集合，得到 %+v", sk)
	}
	if sk.Health != nil {
		t.Errorf("无体检行时 Health 应为 nil，得到 %+v", sk.Health)
	}
}

// TestDaySkeletonRejectsBadDate 非法日期必须报错（不静默用零值继续）。
func TestDaySkeletonRejectsBadDate(t *testing.T) {
	s := openTest(t)
	defer s.Close()
	if _, err := s.DaySkeleton("2026/01/01"); err == nil {
		t.Error("非法日期应报错")
	}
}

// TestListSessionsFiltersLevel 按层级过滤，且只取范围内的 session。
func TestListSessionsFiltersLevel(t *testing.T) {
	s := openTest(t)
	defer s.Close()

	save := sessionDayLoc
	sessionDayLoc = time.UTC
	defer func() { sessionDayLoc = save }()

	// L1（应用）在场：launch_id 锚。
	s.Emit(Event{EventID: "l1s", Type: TypeClientAppLaunched, OccurredAt: at(0),
		LaunchID: "launch-1"})
	s.Emit(Event{EventID: "l1e", Type: TypeClientAppQuit, OccurredAt: at(10 * time.Minute),
		LaunchID: "launch-1"})
	s.Flush()
	if err := s.RebuildSessions(); err != nil {
		t.Fatalf("rebuild sessions: %v", err)
	}

	from, to := at(-time.Hour), at(2*time.Hour)
	all, err := s.ListSessions(from, to, -1, 0)
	if err != nil {
		t.Fatalf("ListSessions all: %v", err)
	}
	if len(all) == 0 {
		t.Fatal("期望至少一段 L1 session")
	}
	if all[0].DurationMs <= 0 {
		t.Errorf("已闭合 session 时长应 > 0，得到 %d", all[0].DurationMs)
	}

	l1, err := s.ListSessions(from, to, 1, 0)
	if err != nil {
		t.Fatalf("ListSessions level=1: %v", err)
	}
	for _, ss := range l1 {
		if ss.Level != 1 {
			t.Errorf("level=1 过滤失效，得到 L%d", ss.Level)
		}
	}

	l0, err := s.ListSessions(from, to, 0, 0)
	if err != nil {
		t.Fatalf("ListSessions level=0: %v", err)
	}
	if len(l0) != 0 {
		t.Errorf("level=0 应为空（无电源事件），得到 %d 段", len(l0))
	}
}
