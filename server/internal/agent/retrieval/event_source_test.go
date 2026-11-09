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

func setEventLastMemoryAt(t *testing.T, db *gorm.DB, id string, at time.Time) {
	t.Helper()
	if err := db.Model(&model.Event{ID: id}).UpdateColumn("last_memory_at", at).Error; err != nil {
		t.Fatalf("set last_memory_at %s: %v", id, err)
	}
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

func eventByID(packets []ContextPacket, id string) (EventMeta, bool) {
	for _, p := range packets {
		if meta, ok := p.Meta.(EventMeta); ok && meta.EventID == id {
			return meta, true
		}
	}
	return EventMeta{}, false
}

func allEventOptions() SearchOptions {
	return SearchOptions{
		UserID: "u1", EventsAll: true,
		Event: SourceConfig{Threshold: 0.5, Limit: 20},
	}
}

// 全量列表：pinned/full、命中/hit、窗口内/summary、窗口外/index 四种形态同时出现。
func TestEventSource_AllEventsModes(t *testing.T) {
	f := setupEventSource(t, EventSourceConfig{})
	now := time.Now()
	seedEventRow(t, f.db, "p1", "钉住事件", "钉住现状")
	seedEventMemory(t, f.db, "pm1", "p1", "钉住记忆", now)
	seedEventRow(t, f.db, "h1", "命中事件", "命中现状")
	seedEventMemory(t, f.db, "hm1", "h1", "命中记忆", now)
	seedEventRow(t, f.db, "r1", "最近事件", "最近现状")
	seedEventMemory(t, f.db, "rm1", "r1", "最近记忆", now.Add(-time.Hour))
	seedEventRow(t, f.db, "o1", "老事件", "老现状")
	seedEventMemory(t, f.db, "om1", "o1", "老记忆", now.AddDate(0, 0, -40))
	setEventLastMemoryAt(t, f.db, "o1", now.AddDate(0, 0, -40))

	f.vs.results = []SearchResult{fullMemResult("hm1", 0.9)}

	opts := allEventOptions()
	opts.Embedding = []float64{1}
	opts.PinnedEventIDs = []string{"p1"}
	packets, _, err := f.src.Fetch(context.Background(), opts)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(packets) != 4 {
		t.Fatalf("expected all 4 events, got %d", len(packets))
	}
	want := map[string]string{"p1": EventModeFull, "h1": EventModeHit, "r1": EventModeSummary, "o1": EventModeIndex}
	for id, mode := range want {
		meta, ok := eventByID(packets, id)
		if !ok {
			t.Fatalf("event %s missing from list", id)
		}
		if meta.Mode != mode {
			t.Fatalf("event %s: expected mode %s, got %s", id, mode, meta.Mode)
		}
	}
	if meta, _ := eventByID(packets, "p1"); !meta.Pinned {
		t.Fatal("pinned event should carry pinned=1")
	}
	if meta, _ := eventByID(packets, "o1"); len(meta.Memories) != 0 {
		t.Fatalf("index event must not carry memories, got %d", len(meta.Memories))
	}
	if meta, _ := eventByID(packets, "p1"); len(meta.Memories) != 1 {
		t.Fatalf("pinned event should carry memories, got %d", len(meta.Memories))
	}
}

// 命中事件带的是「命中的记忆」按分数排序，不是最近 N 条。
func TestEventSource_HitMemoriesByScore(t *testing.T) {
	f := setupEventSource(t, EventSourceConfig{HitMemories: 2})
	seedEventRow(t, f.db, "e1", "事件", "现状")
	base := time.Now()
	seedEventMemory(t, f.db, "m1", "e1", "新但没命中", base)
	seedEventMemory(t, f.db, "m2", "e1", "命中低分", base.Add(-48*time.Hour))
	seedEventMemory(t, f.db, "m3", "e1", "命中高分", base.Add(-72*time.Hour))
	f.vs.results = []SearchResult{fullMemResult("m2", 0.7), fullMemResult("m3", 0.95)}

	opts := allEventOptions()
	opts.Embedding = []float64{1}
	packets, _, err := f.src.Fetch(context.Background(), opts)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	meta, ok := eventByID(packets, "e1")
	if !ok || meta.Mode != EventModeHit {
		t.Fatalf("event should be hit, got %+v", meta)
	}
	if len(meta.Memories) != 2 || meta.Memories[0].ID != "m3" || meta.Memories[1].ID != "m2" {
		t.Fatalf("expected hit memories by score (m3,m2), got %+v", meta.Memories)
	}
	if meta.MemoryCount != 3 {
		t.Fatalf("memory_count should be full count, got %d", meta.MemoryCount)
	}
}

// 命中事件数超过上限时，其余命中事件按普通规则走。
func TestEventSource_HitLimit(t *testing.T) {
	f := setupEventSource(t, EventSourceConfig{HitMax: 2})
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("e%d", i)
		seedEventRow(t, f.db, id, "事件"+id, "现状")
		seedEventMemory(t, f.db, "m"+id, id, "内容", time.Now())
		f.vs.results = append(f.vs.results, fullMemResult("m"+id, 0.9-float64(i)*0.05))
	}

	opts := allEventOptions()
	opts.Embedding = []float64{1}
	packets, _, err := f.src.Fetch(context.Background(), opts)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if got := countMode(packets, EventModeHit); got != 2 {
		t.Fatalf("expected 2 hit events, got %d", got)
	}
	if got := countMode(packets, EventModeSummary); got != 3 {
		t.Fatalf("expected remaining 3 as summary, got %d", got)
	}
}

// 最近窗口事件带最近 N 条记忆（updated_at DESC）。
func TestEventSource_RecentMemoriesCapped(t *testing.T) {
	f := setupEventSource(t, EventSourceConfig{RecentMemories: 3})
	seedEventRow(t, f.db, "e1", "事件", "现状")
	base := time.Now()
	for i, id := range []string{"m1", "m2", "m3", "m4", "m5"} {
		seedEventMemory(t, f.db, id, "e1", id, base.Add(-time.Duration(i)*time.Hour))
	}

	packets, _, err := f.src.Fetch(context.Background(), allEventOptions())
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	meta, ok := eventByID(packets, "e1")
	if !ok || meta.Mode != EventModeSummary {
		t.Fatalf("event should be summary, got %+v", meta)
	}
	if len(meta.Memories) != 3 || meta.Memories[0].ID != "m1" || meta.Memories[2].ID != "m3" {
		t.Fatalf("expected newest 3, got %+v", meta.Memories)
	}
}

// 检索工具路径（EventsAll=false）只返回命中事件，不返回 pinned / recent / index。
func TestEventSource_SearchModeHitsOnly(t *testing.T) {
	f := setupEventSource(t, EventSourceConfig{})
	seedEventRow(t, f.db, "p1", "钉住事件", "现状")
	seedEventMemory(t, f.db, "pm1", "p1", "内容", time.Now())
	seedEventRow(t, f.db, "r1", "最近事件", "现状")
	seedEventMemory(t, f.db, "rm1", "r1", "内容", time.Now())
	seedEventRow(t, f.db, "h1", "命中事件", "现状")
	seedEventMemory(t, f.db, "hm1", "h1", "内容", time.Now())
	f.vs.results = []SearchResult{fullMemResult("hm1", 0.9)}

	packets, _, err := f.src.Fetch(context.Background(), SearchOptions{
		Embedding: []float64{1}, UserID: "u1",
		PinnedEventIDs: []string{"p1"},
		Event:          SourceConfig{Threshold: 0.5, Limit: 20},
	})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(packets) != 1 {
		t.Fatalf("search mode should return only hits, got %d", len(packets))
	}
	if meta, _ := eventOf(packets); meta.EventID != "h1" || meta.Mode != EventModeHit {
		t.Fatalf("unexpected search hit: %+v", meta)
	}
}

// 短输入兜底：不跑向量检索，仍然出全量列表。
func TestEventSource_ListForInjection(t *testing.T) {
	f := setupEventSource(t, EventSourceConfig{})
	seedEventRow(t, f.db, "e1", "事件一", "现状")
	seedEventRow(t, f.db, "e2", "事件二", "现状")
	f.vs.results = []SearchResult{fullMemResult("m1", 0.9)} // 不应被使用

	packets, err := f.src.ListForInjection(context.Background(), "u1", "", nil)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(packets) != 2 {
		t.Fatalf("expected all 2 events, got %d", len(packets))
	}
	for _, p := range packets {
		if meta := p.Meta.(EventMeta); meta.Mode != EventModeSummary {
			t.Fatalf("recent events should be summary, got %s", meta.Mode)
		}
	}
}

func TestEventSource_PinnedNotDegradedByHitQuota(t *testing.T) {
	f := setupEventSource(t, EventSourceConfig{HitMax: 1})
	for _, id := range []string{"p1", "p2", "p3"} {
		seedEventRow(t, f.db, id, "pinned-"+id, "")
		seedEventMemory(t, f.db, "pm-"+id, id, "内容", time.Now())
	}
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("f%d", i)
		seedEventRow(t, f.db, id, "fresh-"+id, "")
		seedEventMemory(t, f.db, "fm-"+id, id, "内容", time.Now())
		f.vs.results = append(f.vs.results, fullMemResult("fm-"+id, 0.9))
	}

	opts := allEventOptions()
	opts.Embedding = []float64{1}
	opts.PinnedEventIDs = []string{"p1", "p2", "p3"}
	packets, _, err := f.src.Fetch(context.Background(), opts)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	pinnedCount := 0
	for _, p := range packets {
		meta := p.Meta.(EventMeta)
		if meta.Pinned {
			pinnedCount++
			if meta.Mode != EventModeFull {
				t.Fatalf("pinned event degraded: %+v", meta)
			}
		}
	}
	if pinnedCount != 3 {
		t.Fatalf("expected 3 pinned full events, got %d", pinnedCount)
	}
	if got := countMode(packets, EventModeHit); got != 1 {
		t.Fatalf("expected hit capped at 1, got %d", got)
	}
}

// pinned 带最近 20 条（event_memories 默认值）。
func TestEventSource_PinnedMemoriesCapped(t *testing.T) {
	f := setupEventSource(t, EventSourceConfig{})
	seedEventRow(t, f.db, "p1", "钉住事件", "现状")
	base := time.Now()
	for i := 0; i < 25; i++ {
		seedEventMemory(t, f.db, fmt.Sprintf("m%02d", i), "p1", "内容", base.Add(-time.Duration(i)*time.Hour))
	}

	opts := allEventOptions()
	opts.PinnedEventIDs = []string{"p1"}
	packets, _, err := f.src.Fetch(context.Background(), opts)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	meta, ok := eventByID(packets, "p1")
	if !ok || meta.Mode != EventModeFull {
		t.Fatalf("pinned event should be full, got %+v", meta)
	}
	if len(meta.Memories) != 20 {
		t.Fatalf("pinned event should carry 20 memories, got %d", len(meta.Memories))
	}
	if meta.Memories[0].ID != "m00" {
		t.Fatalf("pinned memories should be updated_at DESC, got first=%s", meta.Memories[0].ID)
	}
}

// 命中事件带命中记忆 top 10（event_hit_memories 默认值）。
func TestEventSource_HitMemoriesCapped(t *testing.T) {
	f := setupEventSource(t, EventSourceConfig{})
	seedEventRow(t, f.db, "e1", "事件", "现状")
	base := time.Now()
	for i := 0; i < 12; i++ {
		id := fmt.Sprintf("m%02d", i)
		seedEventMemory(t, f.db, id, "e1", "内容", base.Add(-time.Duration(i)*time.Hour))
		f.vs.results = append(f.vs.results, fullMemResult(id, 0.9-float64(i)*0.01))
	}

	opts := allEventOptions()
	opts.Embedding = []float64{1}
	packets, _, err := f.src.Fetch(context.Background(), opts)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	meta, ok := eventByID(packets, "e1")
	if !ok || meta.Mode != EventModeHit {
		t.Fatalf("event should be hit, got %+v", meta)
	}
	if len(meta.Memories) != 10 {
		t.Fatalf("hit event should carry 10 memories, got %d", len(meta.Memories))
	}
	if meta.Memories[0].ID != "m00" {
		t.Fatalf("hit memories should be score DESC, got first=%s", meta.Memories[0].ID)
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

	opts := allEventOptions()
	opts.Embedding = []float64{1}
	packets, _, err := f.src.Fetch(context.Background(), opts)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(packets) != 0 {
		t.Fatalf("deleted event must not be injected, got %d", len(packets))
	}
}

func TestEventSource_LiteralNameHit(t *testing.T) {
	f := setupEventSource(t, EventSourceConfig{})
	seedEventRow(t, f.db, "e1", "回西安准备", "打包中")
	seedEventMemory(t, f.db, "m1", "e1", "内容", time.Now())

	packets, _, err := f.src.Fetch(context.Background(), SearchOptions{
		UserID: "u1", Query: "回西安准备得怎么样了", EventsAll: true,
		Event: SourceConfig{Threshold: 0.5, Limit: 20},
	})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	meta, ok := eventByID(packets, "e1")
	if !ok || meta.Mode != EventModeHit {
		t.Fatalf("literal event name hit should mark event as hit, got %+v", meta)
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

	opts := allEventOptions()
	opts.Embedding = []float64{1}
	packets, _, err := f.src.Fetch(context.Background(), opts)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if meta, ok := eventByID(packets, "e1"); !ok || meta.Mode != EventModeHit {
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
