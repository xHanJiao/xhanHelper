package retrieval

import (
	"context"
	"fmt"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

type eventFixture struct {
	src       *EventSource
	db        *gorm.DB
	memRepo   *service.UserMemoryRepo
	eventRepo *service.EventRepo
	vs        *mockVectorStore
}

func setupEventSource(t *testing.T, cfg EventSourceConfig) *eventFixture {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Event{}, &model.UserMemory{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	memRepo := service.NewUserMemoryRepo(db)
	eventRepo := service.NewEventRepo(db)
	vs := &mockVectorStore{}
	return &eventFixture{
		src:       NewEventSource(vs, memRepo, eventRepo, cfg, NewTimeDecay(0)),
		db:        db,
		memRepo:   memRepo,
		eventRepo: eventRepo,
		vs:        vs,
	}
}

func seedEventRow(t *testing.T, db *gorm.DB, id, name, summary string) *model.Event {
	t.Helper()
	e := &model.Event{ID: id, UserID: "u1", Name: name, Summary: summary}
	if err := db.Create(e).Error; err != nil {
		t.Fatalf("seed event %s: %v", id, err)
	}
	return e
}

func seedEventMemory(t *testing.T, db *gorm.DB, id, eventID, content string, updatedAt time.Time) {
	t.Helper()
	m := &model.UserMemory{
		ID: id, UserID: "u1", MemoryKey: "k", ContentHash: "h-" + id,
		MemoryValue: content, Topic: "测试",
	}
	if eventID != "" {
		eid := eventID
		m.EventID = &eid
	}
	if err := db.Create(m).Error; err != nil {
		t.Fatalf("seed memory %s: %v", id, err)
	}
	if err := db.Model(&model.UserMemory{ID: id}).UpdateColumn("updated_at", updatedAt).Error; err != nil {
		t.Fatalf("set updated_at %s: %v", id, err)
	}
}

func eventOf(packets []ContextPacket) (EventMeta, bool) {
	for _, p := range packets {
		if p.Type == "event" {
			if meta, ok := p.Meta.(EventMeta); ok {
				return meta, true
			}
		}
	}
	return EventMeta{}, false
}

func TestEventSource_GroupsMemoriesByEvent(t *testing.T) {
	f := setupEventSource(t, EventSourceConfig{})
	seedEventRow(t, f.db, "e1", "回西安准备", "打包中")
	now := time.Now()
	seedEventMemory(t, f.db, "m1", "e1", "打包行李", now)
	seedEventMemory(t, f.db, "m2", "e1", "订机票", now.Add(-time.Hour))
	seedEventMemory(t, f.db, "m3", "", "普通记忆", now)
	f.vs.results = []SearchResult{
		fullMemResult("m1", 0.9),
		fullMemResult("m2", 0.7),
		fullMemResult("m3", 0.95),
	}

	packets, _, err := f.src.Fetch(context.Background(), SearchOptions{
		Embedding: []float64{1}, UserID: "u1", Query: "去西安",
		Event: SourceConfig{Threshold: 0.5, Limit: 20},
	})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(packets) != 1 {
		t.Fatalf("expected one event packet, got %d", len(packets))
	}
	meta, _ := eventOf(packets)
	if meta.EventID != "e1" || meta.Mode != EventModeFull {
		t.Fatalf("unexpected meta: %+v", meta)
	}
	if meta.Score < 0.89 {
		t.Fatalf("event score should be the max memory score, got %.3f", meta.Score)
	}
	if len(meta.Memories) != 2 || meta.Memories[0].ID != "m1" {
		t.Fatalf("memories should be updated_at DESC, got %+v", meta.Memories)
	}
}

func TestEventSource_SkipsDeletedEvent(t *testing.T) {
	f := setupEventSource(t, EventSourceConfig{})
	e := seedEventRow(t, f.db, "e1", "已删事件", "")
	if err := f.db.Model(&model.Event{ID: e.ID}).Update("is_deleted", true).Error; err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	seedEventMemory(t, f.db, "m1", "e1", "记忆", time.Now())
	f.vs.results = []SearchResult{fullMemResult("m1", 0.9)}

	packets, _, err := f.src.Fetch(context.Background(), SearchOptions{
		Embedding: []float64{1}, UserID: "u1",
		Event: SourceConfig{Threshold: 0.5, Limit: 20},
	})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(packets) != 0 {
		t.Fatalf("deleted event must not be injected, got %d", len(packets))
	}
}

func TestEventSource_FullMemoriesCapped(t *testing.T) {
	f := setupEventSource(t, EventSourceConfig{MemoriesPerEvent: 3})
	seedEventRow(t, f.db, "e1", "事件", "")
	base := time.Now()
	for i, id := range []string{"m1", "m2", "m3", "m4", "m5"} {
		seedEventMemory(t, f.db, id, "e1", id, base.Add(-time.Duration(i)*time.Hour))
		f.vs.results = append(f.vs.results, fullMemResult(id, 0.9))
	}

	packets, _, err := f.src.Fetch(context.Background(), SearchOptions{
		Embedding: []float64{1}, UserID: "u1",
		Event: SourceConfig{Threshold: 0.5, Limit: 20},
	})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	meta, _ := eventOf(packets)
	if len(meta.Memories) != 3 {
		t.Fatalf("expected 3 memories, got %d", len(meta.Memories))
	}
	if meta.Memories[0].ID != "m1" || meta.Memories[2].ID != "m3" {
		t.Fatalf("expected newest 3, got %+v", meta.Memories)
	}
	if meta.MemoryCount != 5 {
		t.Fatalf("memory_count should be the full count, got %d", meta.MemoryCount)
	}
}

func TestEventSource_QuotaDegradesLowestScore(t *testing.T) {
	f := setupEventSource(t, EventSourceConfig{
		QuotaFull: 2, QuotaCompact: 1, QuotaSummary: 1, QuotaIndex: 1,
	})
	scores := []float64{0.9, 0.8, 0.7, 0.6, 0.55, 0.51}
	for i := range scores {
		id := string(rune('a' + i))
		seedEventRow(t, f.db, "e"+id, "事件"+id, "")
		seedEventMemory(t, f.db, "m"+id, "e"+id, "内容", time.Now())
		f.vs.results = append(f.vs.results, fullMemResult("m"+id, scores[i]))
	}

	packets, _, err := f.src.Fetch(context.Background(), SearchOptions{
		Embedding: []float64{1}, UserID: "u1",
		Event: SourceConfig{Threshold: 0.5, Limit: 20},
	})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	modes := map[string]int{}
	for _, p := range packets {
		modes[p.Meta.(EventMeta).Mode]++
	}
	if modes[EventModeFull] != 2 || modes[EventModeCompact] != 1 ||
		modes[EventModeSummary] != 1 || modes[EventModeIndex] != 1 {
		t.Fatalf("unexpected mode distribution: %v", modes)
	}
	if len(packets) != 5 {
		t.Fatalf("one event should be dropped by index quota, got %d", len(packets))
	}
}

func TestEventSource_StickyWithinSession(t *testing.T) {
	f := setupEventSource(t, EventSourceConfig{})
	seedEventRow(t, f.db, "e1", "事件", "")
	seedEventMemory(t, f.db, "m1", "e1", "内容", time.Now())

	cases := []struct {
		ref  EventRef
		mode string
		miss int
	}{
		{EventRef{ID: "e1", Mode: EventModeFull, Miss: 0}, EventModeFull, 1},
		{EventRef{ID: "e1", Mode: EventModeFull, Miss: 1}, EventModeCompact, 2},
		{EventRef{ID: "e1", Mode: EventModeCompact}, EventModeCompact, 0},
	}
	for _, c := range cases {
		packets, _, err := f.src.Fetch(context.Background(), SearchOptions{
			UserID: "u1", PrevEventRefs: []EventRef{c.ref},
			Event: SourceConfig{Threshold: 0.5, Limit: 20},
		})
		if err != nil {
			t.Fatalf("fetch: %v", err)
		}
		meta, ok := eventOf(packets)
		if !ok {
			t.Fatalf("sticky event %+v should be injected", c.ref)
		}
		if meta.Mode != c.mode || meta.Miss != c.miss {
			t.Fatalf("ref %+v: expected %s/miss=%d, got %s/miss=%d", c.ref, c.mode, c.miss, meta.Mode, meta.Miss)
		}
	}
}

func TestEventSource_CrossSessionCarriedSummary(t *testing.T) {
	f := setupEventSource(t, EventSourceConfig{})
	seedEventRow(t, f.db, "e1", "事件", "现状")
	seedEventMemory(t, f.db, "m1", "e1", "内容", time.Now())

	packets, _, err := f.src.Fetch(context.Background(), SearchOptions{
		UserID: "u1", IsNewSession: true,
		PrevEventRefs: []EventRef{{ID: "e1", Mode: EventModeFull}},
		Event:         SourceConfig{Threshold: 0.5, Limit: 20},
	})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	meta, ok := eventOf(packets)
	if !ok {
		t.Fatal("carried event should be injected")
	}
	if meta.Mode != EventModeSummary || !meta.Carried {
		t.Fatalf("expected summary carried, got mode=%s carried=%v", meta.Mode, meta.Carried)
	}
	if len(meta.Memories) != 0 {
		t.Fatalf("carried summary must not carry memories, got %d", len(meta.Memories))
	}
}

func TestEventSource_PinnedForcesFull(t *testing.T) {
	f := setupEventSource(t, EventSourceConfig{})
	seedEventRow(t, f.db, "e1", "事件", "现状")
	seedEventMemory(t, f.db, "m1", "e1", "内容", time.Now())

	packets, _, err := f.src.Fetch(context.Background(), SearchOptions{
		UserID: "u1", PinnedEventIDs: []string{"e1"},
		Event: SourceConfig{Threshold: 0.5, Limit: 20},
	})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	meta, ok := eventOf(packets)
	if !ok || meta.Mode != EventModeFull || !meta.Pinned {
		t.Fatalf("pinned event should be full, got %+v", meta)
	}
}

func TestEventSource_LiteralNameHit(t *testing.T) {
	f := setupEventSource(t, EventSourceConfig{})
	seedEventRow(t, f.db, "e1", "回西安准备", "打包中")
	seedEventMemory(t, f.db, "m1", "e1", "内容", time.Now())

	packets, _, err := f.src.Fetch(context.Background(), SearchOptions{
		UserID: "u1", Query: "回西安准备得怎么样了",
		Event: SourceConfig{Threshold: 0.5, Limit: 20},
	})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if _, ok := eventOf(packets); !ok {
		t.Fatal("literal event name hit should inject the event")
	}
}

func TestEventSource_BelowThresholdIsNearMiss(t *testing.T) {
	f := setupEventSource(t, EventSourceConfig{})
	seedEventRow(t, f.db, "e1", "事件", "")
	seedEventMemory(t, f.db, "m1", "e1", "内容", time.Now())
	f.vs.results = []SearchResult{fullMemResult("m1", 0.4)}

	packets, nearMiss, err := f.src.Fetch(context.Background(), SearchOptions{
		Embedding: []float64{1}, UserID: "u1",
		Event: SourceConfig{Threshold: 0.5, Limit: 20},
	})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(packets) != 0 {
		t.Fatalf("below threshold should not inject, got %d", len(packets))
	}
	if len(nearMiss) != 1 || nearMiss[0].Reason != "below_threshold" {
		t.Fatalf("expected one below_threshold near miss, got %+v", nearMiss)
	}
}

func TestEventSource_ChunkVectorHit(t *testing.T) {
	f := setupEventSource(t, EventSourceConfig{})
	seedEventRow(t, f.db, "e1", "回西安准备", "打包中")
	seedEventMemory(t, f.db, "m1", "e1", "打包行李", time.Now())
	f.vs.results = []SearchResult{
		memChunkResult("chunk:memory:m1/0", "m1", 0.88),
	}

	packets, _, err := f.src.Fetch(context.Background(), SearchOptions{
		Embedding: []float64{1}, UserID: "u1",
		Event: SourceConfig{Threshold: 0.5, Limit: 20},
	})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if meta, ok := eventOf(packets); !ok || meta.EventID != "e1" {
		t.Fatalf("chunk hit should resolve to its event, got %+v", packets)
	}
}

func TestEventSource_LoadActiveEventsPagination(t *testing.T) {
	f := setupEventSource(t, EventSourceConfig{})
	for i := 0; i < 205; i++ {
		seedEventRow(t, f.db, fmt.Sprintf("e%03d", i), fmt.Sprintf("事件%d", i), "")
	}
	active, err := f.src.loadActiveEvents(context.Background(), "u1")
	if err != nil {
		t.Fatalf("load active events: %v", err)
	}
	if len(active) != 205 {
		t.Fatalf("expected all 205 events (paged), got %d", len(active))
	}
}

func TestEventSource_CrossUserIsolation(t *testing.T) {
	f := setupEventSource(t, EventSourceConfig{})
	seedEventRow(t, f.db, "e1", "u1 的事件", "")
	// u2 的记忆挂到了 u1 的事件 ID 上（脏数据）：不能注入给 u1
	m := &model.UserMemory{
		ID: "m1", UserID: "u2", MemoryKey: "k", ContentHash: "h1",
		MemoryValue: "别人的记忆", EventID: stringPtr("e1"),
	}
	if err := f.db.Create(m).Error; err != nil {
		t.Fatalf("seed memory: %v", err)
	}
	f.vs.results = []SearchResult{fullMemResult("m1", 0.9)}

	packets, _, err := f.src.Fetch(context.Background(), SearchOptions{
		Embedding: []float64{1}, UserID: "u1",
		Event: SourceConfig{Threshold: 0.5, Limit: 20},
	})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(packets) != 0 {
		t.Fatalf("cross-user memory must not inject, got %+v", packets)
	}
}

func TestEventSource_DanglingEventID(t *testing.T) {
	f := setupEventSource(t, EventSourceConfig{})
	seedEventRow(t, f.db, "e1", "活跃事件", "")
	m := &model.UserMemory{
		ID: "m1", UserID: "u1", MemoryKey: "k", ContentHash: "h1",
		MemoryValue: "悬空记忆", EventID: stringPtr("missing"),
	}
	if err := f.db.Create(m).Error; err != nil {
		t.Fatalf("seed memory: %v", err)
	}
	f.vs.results = []SearchResult{fullMemResult("m1", 0.9)}

	packets, _, err := f.src.Fetch(context.Background(), SearchOptions{
		Embedding: []float64{1}, UserID: "u1",
		Event: SourceConfig{Threshold: 0.5, Limit: 20},
	})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(packets) != 0 {
		t.Fatalf("dangling event_id must be skipped, got %+v", packets)
	}
}

func stringPtr(s string) *string { return &s }
