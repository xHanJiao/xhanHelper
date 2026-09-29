package activity

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestIngestBatch_DedupAndWatermark(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "events.db"), true)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	batch := []BatchEvent{
		{EventID: "e1", Type: "client.app.active", OccurredAt: "2026-09-26T10:00:00.000Z", Actor: "user", Attrs: map[string]any{"x": 1}},
		{EventID: "e2", Type: "client.app.inactive", OccurredAt: "2026-09-26T10:07:00.000Z", Actor: "user"},
	}
	res, err := s.IngestBatch("seg1", batch)
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if res.Accepted != 2 || res.Duplicate != 0 || res.Watermark != "2026-09-26T10:07:00.000Z" || res.WatermarkKind != WatermarkTime {
		t.Fatalf("first = %+v", res)
	}

	// 同 event_id 重投 → 全 duplicate
	res, err = s.IngestBatch("seg1", batch)
	if err != nil || res.Accepted != 0 || res.Duplicate != 2 {
		t.Fatalf("replay = %+v, %v", res, err)
	}

	// attrs 落库 + actor
	var row struct {
		Attrs *string `gorm:"column:attrs"`
		Actor string  `gorm:"column:actor"`
	}
	if err := s.DB().Raw(`SELECT attrs, actor FROM events WHERE event_id='e1'`).Scan(&row).Error; err != nil {
		t.Fatalf("scan: %v", err)
	}
	if row.Attrs == nil || *row.Attrs != `{"x":1}` || row.Actor != "user" {
		t.Errorf("row = %+v", row)
	}
	// 缺 actor → 默认 system
	if _, err := s.IngestBatch("seg1", []BatchEvent{{EventID: "e3", Type: "client.idle"}}); err != nil {
		t.Fatalf("ingest e3: %v", err)
	}
	var actor string
	if err := s.DB().Raw(`SELECT actor FROM events WHERE event_id='e3'`).Scan(&actor).Error; err != nil {
		t.Fatalf("scan e3: %v", err)
	}
	if actor != ActorSystem {
		t.Errorf("缺省 actor = %q, want system", actor)
	}
}

// (type,biz_key) 命中唯一索引也算 duplicate（第二道幂等防线）。
func TestIngestBatch_BizKeyDedup(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "events.db"), true)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	if _, err := s.IngestBatch("seg1", []BatchEvent{{EventID: "a", Type: "browser.nav.started", BizKey: "u1"}}); err != nil {
		t.Fatalf("ingest a: %v", err)
	}
	res, err := s.IngestBatch("seg1", []BatchEvent{{EventID: "b", Type: "browser.nav.started", BizKey: "u1"}})
	if err != nil || res.Accepted != 0 || res.Duplicate != 1 {
		t.Fatalf("bizkey dup = %+v, %v", res, err)
	}
}

// segment 模式：水位回显 segment_id；缺 segment_id → ErrInvalidBatch。
func TestIngestBatch_SegmentMode(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "events.db"), true)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	s.SetWatermarkMode(WatermarkSegment)

	res, err := s.IngestBatch("seg-42", []BatchEvent{{EventID: "e1", Type: "client.app.active", OccurredAt: "2026-09-26T10:00:00.000Z"}})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if res.Watermark != "seg-42" || res.WatermarkKind != WatermarkSegment {
		t.Errorf("segment watermark = %+v", res)
	}

	if _, err := s.IngestBatch("", []BatchEvent{{EventID: "e2", Type: "x"}}); !errors.Is(err, ErrInvalidBatch) {
		t.Errorf("缺 segment_id 应报 ErrInvalidBatch, got %v", err)
	}

	// 未知非空值回落 time（记日志，不静默沿用非法模式）
	s.SetWatermarkMode("bogus")
	if s.WatermarkMode() != WatermarkTime {
		t.Errorf("bogus mode 应回落 time, got %q", s.WatermarkMode())
	}
}

func TestIngestBatch_Invalid(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "events.db"), true)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	for _, evs := range [][]BatchEvent{
		{},
		{{EventID: "", Type: "x.y"}},
		{{EventID: "x", Type: ""}},
		{{EventID: "x", Type: "nodot"}},     // 违「域.对象.动作」契约
		{{EventID: "x", Type: "Chat.Sent"}}, // 大写不合法
	} {
		if _, err := s.IngestBatch("seg1", evs); err == nil {
			t.Errorf("应报错: %+v", evs)
		}
	}
}
