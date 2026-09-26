package activity

import (
	"path/filepath"
	"testing"
)

func TestOpenCreatesSchemaAndMeta(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.db")
	s, err := Open(path, true)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	tables := []string{
		"events", "event_refs", "event_resources", "resources", "resource_aliases",
		"interval_closed", "sessions", "event_session", "day_stats", "day_health",
		"event_edges", "meta",
	}
	for _, tbl := range tables {
		var name string
		if err := s.db.Raw(
			`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, tbl,
		).Scan(&name).Error; err != nil {
			t.Fatalf("query %s: %v", tbl, err)
		}
		if name != tbl {
			t.Errorf("table %s missing", tbl)
		}
	}

	if v, ok, err := s.GetMeta("schema_version"); err != nil || !ok || v != "1" {
		t.Errorf("schema_version = %q ok=%v err=%v", v, ok, err)
	}
	if v, ok, err := s.GetMeta("activity.enabled"); err != nil || !ok || v != "true" {
		t.Errorf("activity.enabled = %q ok=%v err=%v", v, ok, err)
	}
}

func TestOpenIdempotentKeepsMeta(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.db")
	s1, err := Open(path, true)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	s1.Close()

	// 二次 Open 用 enabled=false，INSERT OR IGNORE 不应覆盖已有值
	s2, err := Open(path, false)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	defer s2.Close()
	if v, _, _ := s2.GetMeta("activity.enabled"); v != "true" {
		t.Errorf("re-open must not overwrite meta, got %q", v)
	}
}

func TestSetMetaAndBizKeyIdempotency(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "events.db"), true)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	if err := s.SetMeta("dirty_days", `["2026-09-25"]`); err != nil {
		t.Fatalf("set meta: %v", err)
	}
	if v, ok, _ := s.GetMeta("dirty_days"); !ok || v != `["2026-09-25"]` {
		t.Errorf("dirty_days = %q ok=%v", v, ok)
	}

	ins := `INSERT OR IGNORE INTO events
		(event_id,type,occurred_at,received_at,actor,"trigger",biz_key,schema_version)
		VALUES(?,?,?,?,?,?,?,?)`
	args := func(id, biz string) []any {
		return []any{id, "tool.call.started", "2026-09-25T10:00:00+08:00",
			"2026-09-25T10:00:00+08:00", "assistant", "manual", biz, 1}
	}
	if err := s.db.Exec(ins, args("e1", "k1")...).Error; err != nil {
		t.Fatalf("insert e1: %v", err)
	}
	if err := s.db.Exec(ins, args("e2", "k1")...).Error; err != nil {
		t.Fatalf("insert e2: %v", err)
	}
	var n int64
	if err := s.db.Raw(`SELECT count(*) FROM events`).Scan(&n).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("biz_key unique index should reject duplicate, count=%d", n)
	}
}

func TestDBPath(t *testing.T) {
	got := DBPath("/data/xhan_helper.db")
	if want := "/data/events.db"; got != want {
		t.Errorf("DBPath = %q, want %q", got, want)
	}
}
