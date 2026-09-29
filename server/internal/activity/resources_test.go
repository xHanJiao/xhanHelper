package activity

import (
	"strings"
	"testing"
)

func count(t *testing.T, s *Store, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := s.db.Raw(query, args...).Scan(&n).Error; err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	return n
}

func TestResourceIndexingOnEmit(t *testing.T) {
	s := openTest(t)
	s.Emit(Event{
		Type:  "browser.nav.started",
		Actor: "user",
		Attrs: map[string]any{"url_raw": "https://X.com/a/?utm_source=t&b=2"},
	})
	s.Flush()

	if n := count(t, s, `SELECT count(*) FROM resources WHERE resource_kind='url'`); n != 1 {
		t.Errorf("resources = %d, want 1", n)
	}
	var canonical string
	if err := s.db.Raw(`SELECT canonical FROM resources`).Scan(&canonical).Error; err != nil {
		t.Fatalf("canonical: %v", err)
	}
	if canonical != "https://x.com/a?b=2" {
		t.Errorf("canonical = %q", canonical)
	}
	if n := count(t, s, `SELECT count(*) FROM resource_aliases`); n != 1 {
		t.Errorf("aliases = %d, want 1", n)
	}
	if n := count(t, s, `SELECT count(*) FROM event_resources`); n != 1 {
		t.Errorf("event_resources = %d, want 1", n)
	}
}

func TestResourceAliasRepointKeepsBothWritings(t *testing.T) {
	s := openTest(t)
	s.Emit(Event{Type: "browser.bookmark.added", Attrs: map[string]any{"url": "https://x.com/p?utm_source=a"}})
	s.Emit(Event{Type: "browser.bookmark.added", Attrs: map[string]any{"url": "https://x.com/p"}})
	s.Flush()

	if n := count(t, s, `SELECT count(*) FROM resources`); n != 1 {
		t.Errorf("two writings of one canonical must merge to 1 resource, got %d", n)
	}
	if n := count(t, s, `SELECT count(*) FROM resource_aliases`); n != 2 {
		t.Errorf("aliases = %d, want 2 (both writings kept)", n)
	}
	if n := count(t, s, `SELECT count(*) FROM event_resources`); n != 2 {
		t.Errorf("event_resources = %d, want 2", n)
	}
}

func TestResourceExtractionDocAndMemory(t *testing.T) {
	s := openTest(t)
	s.Emit(Event{Type: "doc.created", Attrs: map[string]any{"doc_id": "d1"}})
	s.Emit(Event{Type: "memory.entity.changed", Attrs: map[string]any{"memory_id": "m1"}})
	s.Flush()

	if n := count(t, s, `SELECT count(*) FROM resources WHERE resource_kind='doc' AND resource_id='d1'`); n != 1 {
		t.Errorf("doc resource missing")
	}
	if n := count(t, s, `SELECT count(*) FROM resources WHERE resource_kind='memory' AND resource_id='m1'`); n != 1 {
		t.Errorf("memory resource missing")
	}
}

func TestResourceExtractionBillAndReminder(t *testing.T) {
	s := openTest(t)
	s.Emit(Event{Type: "bill.created", Attrs: map[string]any{"bill_id": "b1"}})
	s.Emit(Event{Type: "reminder.created", Attrs: map[string]any{"reminder_id": "r1"}})
	s.Flush()

	if n := count(t, s, `SELECT count(*) FROM resources WHERE resource_kind='bill' AND resource_id='b1'`); n != 1 {
		t.Errorf("bill resource missing")
	}
	if n := count(t, s, `SELECT count(*) FROM resources WHERE resource_kind='reminder' AND resource_id='r1'`); n != 1 {
		t.Errorf("reminder resource missing")
	}
}

func TestResourceRawIsScrubbedAndCapped(t *testing.T) {
	s := openTest(t)
	longURL := "https://x.com/p?q=" + strings.Repeat("a", 3000) + "&token=secret"
	s.Emit(Event{Type: "browser.nav.started", Attrs: map[string]any{"url_raw": longURL}})
	s.Flush()

	var raw string
	if err := s.db.Raw(`SELECT raw_value FROM event_resources LIMIT 1`).Scan(&raw).Error; err != nil {
		t.Fatalf("scan raw: %v", err)
	}
	if len(raw) > maxResourceValueLen {
		t.Errorf("raw_value not capped: len=%d", len(raw))
	}
	if strings.Contains(raw, "secret") {
		t.Errorf("raw_value leaks sensitive token: %q", raw)
	}
	// resource_id 也必须被 cap 且与 canonical 相等（口径不漂）
	var id, canonical string
	if err := s.db.Raw(`SELECT resource_id, canonical FROM resources LIMIT 1`).Scan(&struct {
		ResourceID *string `gorm:"column:resource_id"`
		Canonical  *string `gorm:"column:canonical"`
	}{&id, &canonical}).Error; err != nil {
		t.Fatalf("scan resource: %v", err)
	}
	if len(id) > maxResourceValueLen || id != canonical {
		t.Errorf("resource_id len=%d canonical len=%d equal=%v", len(id), len(canonical), id == canonical)
	}
}

func TestFileRefRequiresRepoPath(t *testing.T) {
	s := openTest(t)
	// file_path 无 repo_path：不产 file 资源（附表 A：file_path 配 repo_path）
	s.Emit(Event{Type: "tool.call.started", Attrs: map[string]any{"file_path": "src/a.go"}})
	s.Flush()
	if n := count(t, s, `SELECT count(*) FROM resources WHERE resource_kind='file'`); n != 0 {
		t.Errorf("file ref without repo_path must be skipped, got %d", n)
	}
	// 带 repo_path 才产
	s.Emit(Event{Type: "tool.call.started", Attrs: map[string]any{"file_path": "/Users/me/x/r/src/a.go", "repo_path": "/Users/me/x/r"}})
	s.Flush()
	if n := count(t, s, `SELECT count(*) FROM resources WHERE resource_kind='file'`); n != 1 {
		t.Errorf("file ref with repo_path missing, got %d", n)
	}
}

func TestReplayWithDifferentAttrsDoesNotTouchAliases(t *testing.T) {
	s := openTest(t)
	s.Emit(Event{EventID: "e1", Type: "browser.nav.started", Attrs: map[string]any{"url_raw": "https://x.com/a"}})
	s.Flush()
	before := count(t, s, `SELECT count(*) FROM resource_aliases`)
	// 同 event_id 重放、attrs 不同：不该按新 attrs 建边/别名
	s.Emit(Event{EventID: "e1", Type: "browser.nav.started", Attrs: map[string]any{"url_raw": "https://y.com/b"}})
	s.Flush()
	if after := count(t, s, `SELECT count(*) FROM resource_aliases`); after != before {
		t.Errorf("replay polluted aliases: before=%d after=%d", before, after)
	}
	if n := count(t, s, `SELECT count(*) FROM resources WHERE resource_id='https://y.com/b'`); n != 0 {
		t.Errorf("replay created new resource from stale attrs")
	}
}

func TestNoOrphanEdgesWhenEventSkippedByBizKey(t *testing.T) {
	s := openTest(t)
	// 同一 biz_key（doc_id 为业务键）：第二条 event_id 新、但被唯一索引跳过 → 不该建边
	s.Emit(Event{EventID: "e1", Type: "doc.created", BizKey: "d1", Attrs: map[string]any{"doc_id": "d1"}})
	s.Flush()
	s.Emit(Event{EventID: "e2", Type: "doc.created", BizKey: "d1", Attrs: map[string]any{"doc_id": "d1"}})
	s.Flush()

	if n := count(t, s, `SELECT count(*) FROM events`); n != 1 {
		t.Errorf("events = %d, want 1", n)
	}
	if n := count(t, s, `SELECT count(*) FROM event_resources`); n != 1 {
		t.Errorf("orphan edge: event_resources = %d, want 1", n)
	}
	if n := count(t, s, `SELECT count(*) FROM event_resources WHERE event_id NOT IN (SELECT event_id FROM events)`); n != 0 {
		t.Errorf("found %d orphan edges", n)
	}
}

func TestResourceIndexingIdempotentOnReplay(t *testing.T) {
	s := openTest(t)
	ev := Event{
		EventID: "e-dup",
		Type:    "browser.nav.started",
		Attrs:   map[string]any{"url_raw": "https://x.com/a"},
	}
	s.Emit(ev)
	s.Flush()
	s.Emit(ev)
	s.Flush()

	if n := count(t, s, `SELECT count(*) FROM events`); n != 1 {
		t.Errorf("events = %d, want 1", n)
	}
	if n := count(t, s, `SELECT count(*) FROM event_resources`); n != 1 {
		t.Errorf("event_resources = %d, want 1", n)
	}
	if n := count(t, s, `SELECT count(*) FROM resources`); n != 1 {
		t.Errorf("resources = %d, want 1", n)
	}
}

func TestRebuildResourcesReproducesAndBumpsVersion(t *testing.T) {
	s := openTest(t)
	s.Emit(Event{Type: "browser.nav.started", Attrs: map[string]any{"url_raw": "https://x.com/a?utm_source=t"}})
	s.Emit(Event{Type: "doc.created", Attrs: map[string]any{"doc_id": "d1"}})
	s.Flush()
	before := count(t, s, `SELECT count(*) FROM event_resources`)

	// 模拟规则变更后派生被清空
	if err := s.db.Exec(`DELETE FROM event_resources`).Error; err != nil {
		t.Fatalf("clear: %v", err)
	}
	if err := s.RebuildResources(); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if got := count(t, s, `SELECT count(*) FROM event_resources`); got != before {
		t.Errorf("rebuild event_resources = %d, want %d", got, before)
	}
	if v, ok, _ := s.GetMeta("normalize_rule_version"); !ok || v == "" {
		t.Errorf("normalize_rule_version not written")
	}
}
