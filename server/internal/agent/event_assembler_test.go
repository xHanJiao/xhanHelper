package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/idgen"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// fakeEmbedder — 按文本返回固定向量的 embedding 桩（拼装单测不调真实 API）。
type fakeEmbedder struct {
	fn  func(text string) []float64
	err error
}

func (f *fakeEmbedder) Embed(_ context.Context, text string) ([]float64, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.fn != nil {
		return f.fn(text), nil
	}
	return []float64{1, 0, 0}, nil
}

func (f *fakeEmbedder) ModelName() string { return "fake" }

func setupEventAssembler(t *testing.T, emb *fakeEmbedder, dedupeSim float64) (*EventAssembler, *gorm.DB) {
	t.Helper()
	if err := idgen.Init(1); err != nil {
		t.Fatalf("idgen init: %v", err)
	}
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Event{}, &model.UserMemory{}, &model.Embedding{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if dedupeSim == 0 {
		dedupeSim = 0.88
	}
	a := NewEventAssembler(&EventAssemblerConfig{
		FlashClient:   &syncMockLLM{},
		Registry:      noopRegistry(),
		EventRepo:     service.NewEventRepo(db),
		MemRepo:       service.NewUserMemoryRepo(db),
		EmbeddingRepo: service.NewEmbeddingRepo(db),
		Embedder:      emb,
		DedupeSim:     dedupeSim,
		BatchSize:     40,
	})
	return a, db
}

func seedEvent(t *testing.T, db *gorm.DB, userID, name, summary string) *model.Event {
	t.Helper()
	e := &model.Event{UserID: userID, Name: name, Summary: summary}
	if err := service.NewEventRepo(db).Create(context.Background(), e); err != nil {
		t.Fatalf("seed event %s: %v", name, err)
	}
	return e
}

func seedMemory(t *testing.T, db *gorm.DB, userID, id, value string) *model.UserMemory {
	t.Helper()
	m := &model.UserMemory{
		ID:          id,
		UserID:      userID,
		MemoryKey:   "k",
		ContentHash: "h-" + id,
		MemoryValue: value,
	}
	if err := db.Create(m).Error; err != nil {
		t.Fatalf("seed memory %s: %v", id, err)
	}
	return m
}

func loadMemoryByID(t *testing.T, db *gorm.DB, id string) *model.UserMemory {
	t.Helper()
	var m model.UserMemory
	if err := db.Where("id = ?", id).First(&m).Error; err != nil {
		t.Fatalf("load memory %s: %v", id, err)
	}
	return &m
}

func loadEventByID(t *testing.T, db *gorm.DB, id string) *model.Event {
	t.Helper()
	var e model.Event
	if err := db.Where("id = ?", id).First(&e).Error; err != nil {
		t.Fatalf("load event %s: %v", id, err)
	}
	return &e
}

func batchOf(mems ...*model.UserMemory) map[string]model.UserMemory {
	out := make(map[string]model.UserMemory, len(mems))
	for _, m := range mems {
		out[m.ID] = *m
	}
	return out
}

func activeOf(events ...*model.Event) map[string]*model.Event {
	out := make(map[string]*model.Event, len(events))
	for _, e := range events {
		out[e.ID] = e
	}
	return out
}

func TestNormalizeEventName(t *testing.T) {
	cases := map[string]string{
		"驾照考取":            "驾照考取",
		" 驾照 考取 ":         "驾照考取",
		"驾照-考取！":          "驾照考取",
		"Driving License": "drivinglicense",
	}
	for in, want := range cases {
		if got := normalizeEventName(in); got != want {
			t.Errorf("normalizeEventName(%q)=%q, want %q", in, got, want)
		}
	}
}

func TestParseAssemblyPlan_Fenced(t *testing.T) {
	raw := "```json\n{\"assignments\":[{\"memory_id\":\"m1\",\"event_id\":\"e1\"}],\"new_events\":[],\"event_updates\":[{\"event_id\":\"e1\",\"summary\":\"新状态\"}],\"merge\":[]}\n```"
	var plan assemblyPlan
	if _, err := ParseFlashJSON(raw, &plan); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(plan.Assignments) != 1 || plan.Assignments[0].MemoryID != "m1" {
		t.Fatalf("unexpected assignments: %+v", plan.Assignments)
	}
	if len(plan.EventUpdates) != 1 || plan.EventUpdates[0].Summary != "新状态" {
		t.Fatalf("unexpected updates: %+v", plan.EventUpdates)
	}
}

func TestApplyPlan_AssignAndSkipUnknown(t *testing.T) {
	a, db := setupEventAssembler(t, &fakeEmbedder{}, 0)
	ctx := context.Background()
	e := seedEvent(t, db, "u1", "回西安准备", "打包中")
	m1 := seedMemory(t, db, "u1", "m1", "开始打包行李")
	m2 := seedMemory(t, db, "u1", "m2", "买了机票")

	stats := a.applyPlan(ctx, "u1", &assemblyPlan{
		Assignments: []assemblyAssignment{
			{MemoryID: "m1", EventID: e.ID},
			{MemoryID: "m2", EventID: "missing"},
			{MemoryID: "not-in-batch", EventID: e.ID},
		},
	}, batchOf(m1, m2), activeOf(e))

	if got := loadMemoryByID(t, db, "m1").EventID; got == nil || *got != e.ID {
		t.Fatalf("m1 should be assigned to %s, got %v", e.ID, got)
	}
	if got := loadMemoryByID(t, db, "m2").EventID; got != nil {
		t.Fatalf("m2 should stay in pool, got %v", got)
	}
	if stats.Assigned != 1 {
		t.Fatalf("expected 1 assignment, got %d", stats.Assigned)
	}
}

func TestApplyPlan_NewEvent(t *testing.T) {
	a, db := setupEventAssembler(t, &fakeEmbedder{}, 0)
	ctx := context.Background()
	m1 := seedMemory(t, db, "u1", "m1", "开始打包行李")
	m2 := seedMemory(t, db, "u1", "m2", "买了机票")

	stats := a.applyPlan(ctx, "u1", &assemblyPlan{
		NewEvents: []assemblyNewEvent{{
			Name: "回西安准备", Summary: "打包中，机票已买", MemoryIDs: []string{"m1", "m2"},
		}},
	}, batchOf(m1, m2), map[string]*model.Event{})

	if stats.Created != 1 {
		t.Fatalf("expected 1 created event, got %d", stats.Created)
	}
	var events []model.Event
	if err := db.Find(&events).Error; err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 1 || events[0].Name != "回西安准备" {
		t.Fatalf("unexpected events: %+v", events)
	}
	for _, id := range []string{"m1", "m2"} {
		got := loadMemoryByID(t, db, id).EventID
		if got == nil || *got != events[0].ID {
			t.Fatalf("%s should attach to new event, got %v", id, got)
		}
	}
	if events[0].LastMemoryAt == nil {
		t.Fatal("expected last_memory_at to advance")
	}
}

func TestApplyPlan_NewEventDedupeByName(t *testing.T) {
	a, db := setupEventAssembler(t, &fakeEmbedder{fn: func(string) []float64 { return []float64{1, 0, 0} }}, 0)
	ctx := context.Background()
	e := seedEvent(t, db, "u1", "驾照考取", "科三补考中")
	m1 := seedMemory(t, db, "u1", "m1", "科三补考预约 11-14")

	stats := a.applyPlan(ctx, "u1", &assemblyPlan{
		NewEvents: []assemblyNewEvent{{
			Name: "驾照 考取", Summary: "科三还没过", MemoryIDs: []string{"m1"},
		}},
	}, batchOf(m1), activeOf(e))

	if stats.Created != 0 || stats.Rejected != 1 {
		t.Fatalf("expected rejected, got %+v", stats)
	}
	if got := loadMemoryByID(t, db, "m1").EventID; got != nil {
		t.Fatalf("memory should stay in pool, got %v", got)
	}
}

func TestApplyPlan_NewEventDedupeBySummary(t *testing.T) {
	// 所有文本同一向量 → 余弦 1.0，必然触发语义查重。
	a, db := setupEventAssembler(t, &fakeEmbedder{fn: func(string) []float64 { return []float64{1, 0, 0} }}, 0)
	ctx := context.Background()
	e := seedEvent(t, db, "u1", "旧事件", "旧摘要")
	m1 := seedMemory(t, db, "u1", "m1", "完全不同的内容")

	stats := a.applyPlan(ctx, "u1", &assemblyPlan{
		NewEvents: []assemblyNewEvent{{
			Name: "新事件", Summary: "新摘要", MemoryIDs: []string{"m1"},
		}},
	}, batchOf(m1), activeOf(e))

	if stats.Created != 0 || stats.Rejected != 1 {
		t.Fatalf("expected summary dedupe rejection, got %+v", stats)
	}
}

func TestApplyPlan_RejectChainMerge(t *testing.T) {
	a, db := setupEventAssembler(t, &fakeEmbedder{}, 0)
	ctx := context.Background()
	eA := seedEvent(t, db, "u1", "A", "")
	eB := seedEvent(t, db, "u1", "B", "")
	eC := seedEvent(t, db, "u1", "C", "")
	m1 := seedMemory(t, db, "u1", "m1", "A 的记忆")
	if err := service.NewEventRepo(db).AssignMemories(ctx, "u1", eA.ID, []string{"m1"}); err != nil {
		t.Fatalf("assign: %v", err)
	}

	stats := a.applyPlan(ctx, "u1", &assemblyPlan{
		Merge: []assemblyMerge{
			{From: eA.ID, Into: eB.ID},
			{From: eB.ID, Into: eC.ID}, // 链式，必须拒绝
		},
	}, batchOf(m1), activeOf(eA, eB, eC))

	if stats.Merged != 0 {
		t.Fatalf("chain should reject all merges, got %d", stats.Merged)
	}
	if loadEventByID(t, db, eA.ID).IsDeleted {
		t.Fatal("A should stay active when the whole batch is rejected")
	}
	if loadEventByID(t, db, eB.ID).IsDeleted || loadEventByID(t, db, eC.ID).IsDeleted {
		t.Fatal("B and C should stay active")
	}
	if got := loadMemoryByID(t, db, "m1").EventID; got == nil || *got != eA.ID {
		t.Fatalf("m1 should stay on A, got %v", got)
	}
}

func TestApplyPlan_RemapsAliasAfterMerge(t *testing.T) {
	a, db := setupEventAssembler(t, &fakeEmbedder{}, 0)
	ctx := context.Background()
	eA := seedEvent(t, db, "u1", "A", "")
	eB := seedEvent(t, db, "u1", "B", "")
	m1 := seedMemory(t, db, "u1", "m1", "新记忆")

	stats := a.applyPlan(ctx, "u1", &assemblyPlan{
		Merge:       []assemblyMerge{{From: eA.ID, Into: eB.ID}},
		Assignments: []assemblyAssignment{{MemoryID: "m1", EventID: eA.ID}},
		EventUpdates: []assemblyEventUpdate{
			{EventID: eA.ID, Summary: "合并后的状态"},
		},
	}, batchOf(m1), activeOf(eA, eB))

	if stats.Merged != 1 || stats.Assigned != 1 || stats.Updated != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	if got := loadMemoryByID(t, db, "m1").EventID; got == nil || *got != eB.ID {
		t.Fatalf("m1 should be remapped to B, got %v", got)
	}
	if got := loadEventByID(t, db, eB.ID).Summary; got != "合并后的状态" {
		t.Fatalf("B summary should be updated, got %q", got)
	}
}

func TestBuildCandidates_RanksBySimilarity(t *testing.T) {
	a, db := setupEventAssembler(t, &fakeEmbedder{}, 0)
	ctx := context.Background()
	eA := seedEvent(t, db, "u1", "A", "")
	eB := seedEvent(t, db, "u1", "B", "")
	seedMemory(t, db, "u1", "mA", "A 的记忆")
	seedMemory(t, db, "u1", "mB", "B 的记忆")
	mNew := seedMemory(t, db, "u1", "mNew", "新记忆")
	if err := service.NewEventRepo(db).AssignMemories(ctx, "u1", eA.ID, []string{"mA"}); err != nil {
		t.Fatalf("assign A: %v", err)
	}
	if err := service.NewEventRepo(db).AssignMemories(ctx, "u1", eB.ID, []string{"mB"}); err != nil {
		t.Fatalf("assign B: %v", err)
	}
	seedVec := func(id string, vec []float64) {
		s := llm.EmbeddingToString(vec)
		if err := db.Create(&model.Embedding{ID: id, EntityType: model.EntityTypeMemory, EntityID: id, Embedding: &s}).Error; err != nil {
			t.Fatalf("seed vector %s: %v", id, err)
		}
	}
	seedVec("mA", []float64{1, 0, 0})
	seedVec("mB", []float64{0, 1, 0})
	seedVec("mNew", []float64{0.9, 0.1, 0})

	candidates, err := a.buildCandidates(ctx, "u1", []model.UserMemory{*mNew}, activeOf(eA, eB))
	if err != nil {
		t.Fatalf("build candidates: %v", err)
	}
	if len(candidates) != 2 || candidates[0].Event.ID != eA.ID {
		t.Fatalf("expected A first, got %+v", candidates)
	}
	if candidates[0].Sim < 0.8 {
		t.Fatalf("expected high sim for A, got %.3f", candidates[0].Sim)
	}
}

func TestMemoryWorker_SetsPhaseDoneWhenDisabled(t *testing.T) {
	w := NewMemoryWorker(&MemoryWorkerConfig{MemoryAutoExtract: false})
	sess := &SessionSnapshot{ConvID: "c1", UserID: "u1"}
	if err := w.Process(context.Background(), sess); err != nil {
		t.Fatalf("process: %v", err)
	}
	if !sess.MemoryPhaseDone {
		t.Fatal("expected MemoryPhaseDone=true when extraction disabled")
	}
}

func TestEventWorker_SkipsWhenMemoryPhaseNotDone(t *testing.T) {
	// 未置 done 时不应触碰任何依赖（memRepo/assembler 都传 nil）。
	w := &EventWorker{}
	sess := &SessionSnapshot{ConvID: "c1", UserID: "u1", msgs: []model.Message{{CreatedAt: time.Now()}}}
	if err := w.Process(context.Background(), sess); err != nil {
		t.Fatalf("process: %v", err)
	}
}

func TestMemoryWorker_ErrorDoesNotSetPhaseDone(t *testing.T) {
	// 抽取失败 → 不置 done，EventWorker 会跳过（不用半成品记忆拼事件）。
	extractor := NewMemoryExtractor(&MemoryExtractorConfig{LLMClient: &syncMockLLM{}})
	w := NewMemoryWorker(&MemoryWorkerConfig{Extractor: extractor, MemoryAutoExtract: true})
	sess := &SessionSnapshot{ConvID: "c1", UserID: "u1", msgs: []model.Message{{Role: "user", Content: "hi"}}}
	if err := w.Process(context.Background(), sess); err == nil {
		t.Fatal("expected extraction error")
	}
	if sess.MemoryPhaseDone {
		t.Fatal("MemoryPhaseDone must stay false on error")
	}
}

func TestAssemble_BatchesAndAccumulates(t *testing.T) {
	mock := &syncMockLLM{responses: []llm.Message{
		{Content: `{"assignments":[],"new_events":[],"event_updates":[],"merge":[]}`},
		{Content: `{"assignments":[],"new_events":[],"event_updates":[],"merge":[]}`},
	}}
	a, db := setupEventAssembler(t, &fakeEmbedder{}, 0)
	a.flashClient = mock
	a.batchSize = 2
	m1 := seedMemory(t, db, "u1", "m1", "a")
	m2 := seedMemory(t, db, "u1", "m2", "b")
	m3 := seedMemory(t, db, "u1", "m3", "c")

	stats, err := a.Assemble(context.Background(), "u1", []model.UserMemory{*m1, *m2, *m3})
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if stats.Batches != 2 || stats.Memories != 3 {
		t.Fatalf("expected 2 batches / 3 memories, got %+v", stats)
	}
	if mock.callCount != 2 {
		t.Fatalf("expected one LLM call per batch, got %d", mock.callCount)
	}
}

func TestEventWorker_SuccessPath(t *testing.T) {
	mock := &syncMockLLM{responses: []llm.Message{
		{Content: `{"assignments":[{"memory_id":"m1","event_id":"EVENT_ID"}],"new_events":[],"event_updates":[],"merge":[]}`},
	}}
	a, db := setupEventAssembler(t, &fakeEmbedder{}, 0)
	a.flashClient = mock
	e := seedEvent(t, db, "u1", "回西安准备", "打包中")
	mock.responses[0].Content = strings.ReplaceAll(mock.responses[0].Content, "EVENT_ID", e.ID)
	seedMemory(t, db, "u1", "m1", "开始打包")

	w := NewEventWorker(&EventWorkerConfig{MemRepo: service.NewUserMemoryRepo(db), Assembler: a})
	sess := &SessionSnapshot{
		ConvID:          "c1",
		UserID:          "u1",
		msgs:            []model.Message{{CreatedAt: time.Now().Add(-time.Hour)}},
		MemoryPhaseDone: true,
	}
	if err := w.Process(context.Background(), sess); err != nil {
		t.Fatalf("process: %v", err)
	}
	if got := loadMemoryByID(t, db, "m1").EventID; got == nil || *got != e.ID {
		t.Fatalf("m1 should be assigned to event, got %v", got)
	}
}

func TestApplyPlan_NewEventsDoNotShareMemory(t *testing.T) {
	a, db := setupEventAssembler(t, &fakeEmbedder{fn: func(text string) []float64 {
		if strings.HasPrefix(text, "A") {
			return []float64{1, 0, 0}
		}
		return []float64{0, 1, 0}
	}}, 0)
	ctx := context.Background()
	m1 := seedMemory(t, db, "u1", "m1", "唯一记忆")

	stats := a.applyPlan(ctx, "u1", &assemblyPlan{
		NewEvents: []assemblyNewEvent{
			{Name: "A 事件", Summary: "a", MemoryIDs: []string{"m1"}},
			{Name: "B 事件", Summary: "b", MemoryIDs: []string{"m1"}},
		},
	}, batchOf(m1), map[string]*model.Event{})

	if stats.Created != 1 || stats.Rejected != 1 {
		t.Fatalf("expected one created + one rejected, got %+v", stats)
	}
	var events []model.Event
	if err := db.Find(&events).Error; err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected exactly one event, got %d", len(events))
	}
	if got := loadMemoryByID(t, db, "m1").EventID; got == nil || *got != events[0].ID {
		t.Fatalf("m1 should belong to the first event, got %v", got)
	}
}

func TestApplyPlan_NewEventFailsClosedOnEmbedError(t *testing.T) {
	a, db := setupEventAssembler(t, &fakeEmbedder{err: errors.New("embed down")}, 0)
	ctx := context.Background()
	e := seedEvent(t, db, "u1", "旧事件", "旧摘要")
	m1 := seedMemory(t, db, "u1", "m1", "新记忆")

	stats := a.applyPlan(ctx, "u1", &assemblyPlan{
		NewEvents: []assemblyNewEvent{{Name: "新事件", Summary: "新摘要", MemoryIDs: []string{"m1"}}},
	}, batchOf(m1), activeOf(e))

	if stats.Created != 0 || stats.Rejected != 1 {
		t.Fatalf("expected fail-closed rejection, got %+v", stats)
	}
	if got := loadMemoryByID(t, db, "m1").EventID; got != nil {
		t.Fatalf("memory should stay in pool, got %v", got)
	}
}

func TestBuildAssemblyUserMessage_KeepsChineseSummary(t *testing.T) {
	long := strings.Repeat("中文摘要", 60) // 240 runes，超过 300 bytes
	e := &model.Event{ID: "e1", Name: "事件", Summary: long}
	msg := buildAssemblyUserMessage(nil, []assemblyCandidate{{Event: e}})
	if !utf8.ValidString(msg) {
		t.Fatal("prompt must be valid UTF-8")
	}
	if !strings.Contains(msg, long) {
		t.Fatal("summary must not be byte-truncated")
	}
}
