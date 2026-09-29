package activity

import (
	"testing"
	"time"
)

// countRefs 数 event_refs 里某 source 的行数。
func countRefs(t *testing.T, s *Store, source string) int {
	t.Helper()
	var n int64
	if err := s.DB().Raw(`SELECT count(*) FROM event_refs WHERE source = ?`, source).Scan(&n).Error; err != nil {
		t.Fatalf("count refs: %v", err)
	}
	return int(n)
}

// TestRebuildRefsSharedResource 共享资源 → 给无 conversation_id 的事件挂 conversation ref。
func TestRebuildRefsSharedResource(t *testing.T) {
	s := openTest(t)
	defer s.Close()

	// 带 conversation 的事件碰了 URL；聊天外的事件碰了同一 URL。
	s.Emit(Event{EventID: "chat1", Type: TypeChatTurnStarted, OccurredAt: at(0),
		Actor: ActorAssistant, ConversationID: "conv-1",
		Attrs: map[string]any{"url": "https://x.com/a"}})
	s.Emit(Event{EventID: "nav1", Type: TypeBrowserNavStarted, OccurredAt: at(time.Minute),
		Actor: ActorUser, Attrs: map[string]any{"url": "https://x.com/a"}})
	s.Flush()
	if err := s.RebuildResources(); err != nil {
		t.Fatalf("RebuildResources: %v", err)
	}
	if err := s.RebuildRefs(); err != nil {
		t.Fatalf("RebuildRefs: %v", err)
	}

	var rows []struct {
		EventID string `gorm:"column:event_id"`
		RefID   string `gorm:"column:ref_id"`
	}
	if err := s.DB().Raw(`SELECT event_id, ref_id FROM event_refs WHERE source='resource' AND ref_type='conversation'`).Scan(&rows).Error; err != nil {
		t.Fatalf("query refs: %v", err)
	}
	if len(rows) != 1 || rows[0].EventID != "nav1" || rows[0].RefID != "conv-1" {
		t.Errorf("refs = %+v, want nav1→conv-1", rows)
	}
}

// TestRebuildRefsMemoryDirect memory 资源 → ref(memory)（供 facts_by_memory 取）。
func TestRebuildRefsMemoryDirect(t *testing.T) {
	s := openTest(t)
	defer s.Close()

	s.Emit(Event{EventID: "m1", Type: TypeMemoryEntityChanged, OccurredAt: at(0),
		Actor: ActorAssistant, Attrs: map[string]any{"memory_id": "mem-9"}})
	s.Flush()
	if err := s.RebuildResources(); err != nil {
		t.Fatalf("RebuildResources: %v", err)
	}
	if err := s.RebuildRefs(); err != nil {
		t.Fatalf("RebuildRefs: %v", err)
	}
	facts, err := s.FactsByMemory("mem-9", 0)
	if err != nil {
		t.Fatalf("FactsByMemory: %v", err)
	}
	if len(facts) != 1 || facts[0].EventID != "m1" {
		t.Errorf("facts = %+v, want [m1]", facts)
	}
}

// TestRebuildRefsKeepsExplicit explicit 不落 event_refs（直接关系由 events 列承载），
// 但重算的 DELETE 是防御性的：即便有 explicit 行也不得删（附表 B 混合表契约）。
func TestRebuildRefsKeepsExplicit(t *testing.T) {
	s := openTest(t)
	defer s.Close()

	if err := s.DB().Exec(`INSERT INTO event_refs(event_id,ref_type,ref_id,source,confidence)
		VALUES('e1','conversation','c1','explicit',1.0)`).Error; err != nil {
		t.Fatalf("seed explicit: %v", err)
	}
	if err := s.RebuildRefs(); err != nil {
		t.Fatalf("RebuildRefs: %v", err)
	}
	if n := countRefs(t, s, RefSourceExplicit); n != 1 {
		t.Errorf("explicit refs = %d, want 1（重算不得删 explicit）", n)
	}
}

// TestRebuildRefsNoSharedResource 不共享资源 → 不挂。
func TestRebuildRefsNoSharedResource(t *testing.T) {
	s := openTest(t)
	defer s.Close()

	s.Emit(Event{EventID: "chat1", Type: TypeChatTurnStarted, OccurredAt: at(0),
		Actor: ActorAssistant, ConversationID: "conv-1", Attrs: map[string]any{"url": "https://x.com/a"}})
	s.Emit(Event{EventID: "nav1", Type: TypeBrowserNavStarted, OccurredAt: at(time.Minute),
		Actor: ActorUser, Attrs: map[string]any{"url": "https://y.com/b"}})
	s.Flush()
	if err := s.RebuildResources(); err != nil {
		t.Fatalf("RebuildResources: %v", err)
	}
	if err := s.RebuildRefs(); err != nil {
		t.Fatalf("RebuildRefs: %v", err)
	}
	if n := countRefs(t, s, RefSourceResource); n != 0 {
		t.Errorf("resource refs = %d, want 0", n)
	}
}

// TestRecomputeRunsRefs Recompute 应带上 RebuildRefs（派生链完整）。
func TestRecomputeRunsRefs(t *testing.T) {
	s := openTest(t)
	defer s.Close()

	s.Emit(Event{EventID: "chat1", Type: TypeChatTurnStarted, OccurredAt: at(0),
		Actor: ActorAssistant, ConversationID: "conv-1", Attrs: map[string]any{"url": "https://x.com/a"}})
	s.Emit(Event{EventID: "nav1", Type: TypeBrowserNavStarted, OccurredAt: at(time.Minute),
		Actor: ActorUser, Attrs: map[string]any{"url": "https://x.com/a"}})
	s.Flush()
	if err := s.Recompute(); err != nil {
		t.Fatalf("Recompute: %v", err)
	}
	if n := countRefs(t, s, RefSourceResource); n < 1 {
		t.Errorf("Recompute 后 resource refs = %d, want >=1", n)
	}
}

// TestRebuildRefsMaxConfidence 同一事件命中同 conversation 多资源（不同具体度）→ 取最高 confidence。
func TestRebuildRefsMaxConfidence(t *testing.T) {
	s := openTest(t)
	defer s.Close()

	// 聊天事件同时碰了低具体度 repo 与高具体度 url。
	s.Emit(Event{EventID: "chat1", Type: TypeChatTurnStarted, OccurredAt: at(0), Actor: ActorAssistant,
		ConversationID: "conv-1", Attrs: map[string]any{"url": "https://x.com/a", "repo_path": "/Users/z/proj"}})
	// 外部事件也同时碰这两个 → 应只挂一条 conversation ref，confidence 取 url 的 0.9。
	s.Emit(Event{EventID: "nav1", Type: TypeBrowserNavStarted, OccurredAt: at(time.Minute), Actor: ActorUser,
		Attrs: map[string]any{"url": "https://x.com/a", "repo_path": "/Users/z/proj"}})
	s.Flush()
	if err := s.RebuildResources(); err != nil {
		t.Fatalf("RebuildResources: %v", err)
	}
	if err := s.RebuildRefs(); err != nil {
		t.Fatalf("RebuildRefs: %v", err)
	}
	var rows []struct {
		EventID    string  `gorm:"column:event_id"`
		Confidence float64 `gorm:"column:confidence"`
	}
	if err := s.DB().Raw(`SELECT event_id, confidence FROM event_refs
		WHERE source='resource' AND ref_type='conversation'`).Scan(&rows).Error; err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 1 || rows[0].EventID != "nav1" {
		t.Fatalf("rows = %+v, want 单条 nav1", rows)
	}
	if rows[0].Confidence != resourceSpecificity[ResourceKindURL] {
		t.Errorf("confidence = %v, want %v（取最高具体度）", rows[0].Confidence, resourceSpecificity[ResourceKindURL])
	}
}

// TestRebuildRefsReminderDirect reminder 资源 → ref(reminder)。
func TestRebuildRefsReminderDirect(t *testing.T) {
	s := openTest(t)
	defer s.Close()

	s.Emit(Event{EventID: "r1", Type: TypeReminderCreated, OccurredAt: at(0), Actor: ActorAssistant,
		Attrs: map[string]any{"reminder_id": "rem-7"}})
	s.Flush()
	if err := s.RebuildResources(); err != nil {
		t.Fatalf("RebuildResources: %v", err)
	}
	if err := s.RebuildRefs(); err != nil {
		t.Fatalf("RebuildRefs: %v", err)
	}
	var n int64
	if err := s.DB().Raw(`SELECT count(*) FROM event_refs
		WHERE ref_type='reminder' AND ref_id='rem-7' AND source='resource'`).Scan(&n).Error; err != nil {
		t.Fatalf("query: %v", err)
	}
	if n != 1 {
		t.Errorf("reminder refs = %d, want 1", n)
	}
}
