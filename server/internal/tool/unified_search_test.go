package tool

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/agent/retrieval"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// mockEmbedder is reuse from memory_search_test.go

// mockContextSource 实现 retrieval.ContextSource，直接返回注入的数据
type mockContextSource struct {
	name string
	pkts []retrieval.ContextPacket
}

func (m *mockContextSource) Name() string { return m.name }

func (m *mockContextSource) Fetch(_ context.Context, _ retrieval.SearchOptions) ([]retrieval.ContextPacket, []retrieval.NearMissItem, error) {
	return m.pkts, nil, nil
}

func (m *mockContextSource) Warmup(_ context.Context, _ *llm.Embedder) {}

// ==================== Schema Tests ====================

func TestUnifiedSearch_Functions(t *testing.T) {
	tool := NewUnifiedSearchTool(nil, &mockEmbedder{}, nil, nil, nil, nil, nil)
	fns := tool.Functions()
	if len(fns) != 2 {
		t.Fatalf("expected 2 functions, got %d", len(fns))
	}
	names := make(map[string]bool)
	for _, f := range fns {
		names[f.Name] = true
	}
	for _, n := range []string{"semantic_search", "keyword_search"} {
		if !names[n] {
			t.Errorf("missing function: %s", n)
		}
	}
}

func TestUnifiedSearch_NameDesc(t *testing.T) {
	tool := NewUnifiedSearchTool(nil, &mockEmbedder{}, nil, nil, nil, nil, nil)
	if tool.Name() != "unified_search" {
		t.Errorf("expected name=unified_search, got %s", tool.Name())
	}
	if tool.Description() == "" {
		t.Error("description should not be empty")
	}
}

// ==================== Error Handling Tests ====================

func TestUnifiedSearch_Errors(t *testing.T) {
	tool := NewUnifiedSearchTool(
		map[string]retrieval.ContextSource{"memory": &mockContextSource{name: "memory"}},
		&mockEmbedder{}, nil, nil, nil, nil, nil,
	)
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user-1"}

	t.Run("semantic_search empty query", func(t *testing.T) {
		res := tool.Execute(ec, "semantic_search", mustMarshal(t, map[string]string{"query": ""}))
		if res.Success {
			t.Error("expected error for empty query")
		}
	})

	t.Run("keyword_search empty keywords", func(t *testing.T) {
		res := tool.Execute(ec, "keyword_search", mustMarshal(t, map[string]string{"keywords": ""}))
		if res.Success {
			t.Error("expected error for empty keywords")
		}
	})

	t.Run("keyword_search no valid keywords", func(t *testing.T) {
		res := tool.Execute(ec, "keyword_search", mustMarshal(t, map[string]string{"keywords": "、， "}))
		if res.Success {
			t.Error("expected error for no valid keywords")
		}
	})

	t.Run("unknown function", func(t *testing.T) {
		res := tool.Execute(ec, "nope", mustMarshal(t, map[string]string{}))
		if res.Success {
			t.Error("expected error for unknown function")
		}
	})
}

func TestUnifiedSearch_TopKClamp(t *testing.T) {
	src := &mockContextSource{name: "memory", pkts: []retrieval.ContextPacket{
		{ID: "m1", Type: "memory", Meta: retrieval.EntityMeta{Content: "测试内容", CreatedAt: time.Now()}},
	}}
	tool := NewUnifiedSearchTool(
		map[string]retrieval.ContextSource{"memory": src},
		&mockEmbedder{}, nil, nil, nil, nil, nil,
	)
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user-1"}

	t.Run("top_k=0 defaults to 5", func(t *testing.T) {
		res := tool.Execute(ec, "semantic_search", mustMarshal(t, map[string]interface{}{"query": "测试", "top_k": 0}))
		if !res.Success {
			t.Fatalf("unexpected error: %s", res.Error)
		}
	})

	t.Run("top_k=20 clamps to 10", func(t *testing.T) {
		res := tool.Execute(ec, "semantic_search", mustMarshal(t, map[string]interface{}{"query": "测试", "top_k": 20}))
		if !res.Success {
			t.Fatalf("unexpected error: %s", res.Error)
		}
	})
}

// ==================== semantic_search Tests (mock sources) ====================

func TestUnifiedSearch_SemanticAllTypes(t *testing.T) {
	now := time.Now()
	sources := map[string]retrieval.ContextSource{
		"memory": &mockContextSource{name: "memory", pkts: []retrieval.ContextPacket{
			{ID: "m1", Type: "memory", Score: 0.85, Meta: retrieval.EntityMeta{Content: "用户喜欢吃辣", Topic: "偏好", CreatedAt: now}},
		}},
		"note": &mockContextSource{name: "note", pkts: []retrieval.ContextPacket{
			{ID: "n1", Type: "note", Score: 0.72, Meta: retrieval.EntityMeta{Content: "项目笔记", Topic: "工作", CreatedAt: now}},
		}},
		"summary": &mockContextSource{name: "summary", pkts: []retrieval.ContextPacket{
			{ID: "s1", Type: "summary", Score: 0.66, Meta: retrieval.SummaryMeta{SummaryID: "s1", SummaryText: "讨论了健身", Topics: "健康", CoverFromMsgID: "m1", CoverToMsgID: "m5"}},
		}},
		"event": &mockContextSource{name: "event", pkts: []retrieval.ContextPacket{
			{ID: "e1", Type: "event", Score: 0.78, Meta: retrieval.EventMeta{EventID: "e1", Title: "日本旅行", Summary: "在做攻略", Mode: retrieval.EventModeFull, MemoryCount: 3}},
		}},
	}
	tool := NewUnifiedSearchTool(sources, &mockEmbedder{}, nil, nil, nil, nil, nil)
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user-1"}

	res := tool.Execute(ec, "semantic_search", mustMarshal(t, map[string]string{"query": "旅行"}))
	if !res.Success {
		t.Fatalf("unexpected error: %s", res.Error)
	}

	var out searchResult
	if err := json.Unmarshal(res.Data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if out.Memories == nil {
		t.Error("expected memories")
	}
	if out.Notes == nil {
		t.Error("expected notes")
	}
	if out.Summaries == nil {
		t.Error("expected summaries")
	}
	if out.Events == nil {
		t.Error("expected events")
	}
}

func TestUnifiedSearch_SemanticTypeFilter(t *testing.T) {
	sources := map[string]retrieval.ContextSource{
		"memory": &mockContextSource{name: "memory", pkts: []retrieval.ContextPacket{
			{ID: "m1", Type: "memory", Meta: retrieval.EntityMeta{Content: "测试", CreatedAt: time.Now()}},
		}},
		"note": &mockContextSource{name: "note", pkts: []retrieval.ContextPacket{
			{ID: "n1", Type: "note", Meta: retrieval.EntityMeta{Content: "笔记", CreatedAt: time.Now()}},
		}},
	}
	tool := NewUnifiedSearchTool(sources, &mockEmbedder{}, nil, nil, nil, nil, nil)
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user-1"}

	res := tool.Execute(ec, "semantic_search", mustMarshal(t, map[string]interface{}{
		"query": "测试",
		"types": []string{"memory"},
	}))
	if !res.Success {
		t.Fatalf("unexpected error: %s", res.Error)
	}

	var out searchResult
	json.Unmarshal(res.Data, &out)

	if out.Memories == nil {
		t.Error("expected memories")
	}
	if out.Notes != nil {
		t.Error("notes should not appear when filtered out")
	}
}

func TestUnifiedSearch_SemanticEmptyResults(t *testing.T) {
	sources := map[string]retrieval.ContextSource{
		"memory": &mockContextSource{name: "memory", pkts: nil},
		"note":   &mockContextSource{name: "note", pkts: []retrieval.ContextPacket{}},
	}
	tool := NewUnifiedSearchTool(sources, &mockEmbedder{}, nil, nil, nil, nil, nil)
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user-1"}

	res := tool.Execute(ec, "semantic_search", mustMarshal(t, map[string]string{"query": "不存在"}))
	if !res.Success {
		t.Fatalf("unexpected error: %s", res.Error)
	}

	var out map[string]interface{}
	json.Unmarshal(res.Data, &out)
	if _, ok := out["memories"]; ok {
		t.Error("memories should be omitted when empty")
	}
	if _, ok := out["notes"]; ok {
		t.Error("notes should be omitted when empty")
	}
}

func TestUnifiedSearch_SemanticScoreIncluded(t *testing.T) {
	sources := map[string]retrieval.ContextSource{
		"memory": &mockContextSource{name: "memory", pkts: []retrieval.ContextPacket{
			{ID: "m1", Type: "memory", Score: 0.85, Meta: retrieval.EntityMeta{Content: "测试", CreatedAt: time.Now()}},
		}},
	}
	tool := NewUnifiedSearchTool(sources, &mockEmbedder{}, nil, nil, nil, nil, nil)
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user-1"}

	res := tool.Execute(ec, "semantic_search", mustMarshal(t, map[string]string{"query": "测试"}))
	var out searchResult
	json.Unmarshal(res.Data, &out)

	items := out.Memories.Data.([]interface{})
	first := items[0].(map[string]interface{})
	if _, ok := first["score"]; !ok {
		t.Error("semantic search should include score field")
	}
}

func TestUnifiedSearch_SemanticScoreRounded(t *testing.T) {
	sources := map[string]retrieval.ContextSource{
		"memory": &mockContextSource{name: "memory", pkts: []retrieval.ContextPacket{
			{ID: "m1", Type: "memory", Score: 0.85327, Meta: retrieval.EntityMeta{Content: "测试", CreatedAt: time.Now()}},
		}},
	}
	tool := NewUnifiedSearchTool(sources, &mockEmbedder{}, nil, nil, nil, nil, nil)
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user-1"}

	res := tool.Execute(ec, "semantic_search", mustMarshal(t, map[string]string{"query": "测试"}))
	var out searchResult
	json.Unmarshal(res.Data, &out)

	items := out.Memories.Data.([]interface{})
	first := items[0].(map[string]interface{})
	if first["score"].(float64) != 0.8533 {
		t.Errorf("expected score=0.8533 (rounded to 4dp), got %v", first["score"])
	}
}

// queryAwareSource 根据 query 返回不同 packets，用于多 sub-query 合并测试
type queryAwareSource struct {
	name string
	pkts map[string][]retrieval.ContextPacket
}

func (m *queryAwareSource) Name() string { return m.name }
func (m *queryAwareSource) Fetch(_ context.Context, opts retrieval.SearchOptions) ([]retrieval.ContextPacket, []retrieval.NearMissItem, error) {
	if pkts, ok := m.pkts[opts.Query]; ok {
		return pkts, nil, nil
	}
	return nil, nil, nil
}
func (m *queryAwareSource) Warmup(_ context.Context, _ *llm.Embedder) {}

func TestUnifiedSearch_SemanticMultiQueryMerge(t *testing.T) {
	now := time.Now()
	sources := map[string]retrieval.ContextSource{
		"memory": &queryAwareSource{
			name: "memory",
			pkts: map[string][]retrieval.ContextPacket{
				"睡眠": {{ID: "m1", Type: "memory", Score: 0.8523, Meta: retrieval.EntityMeta{Content: "最近睡眠不好", Topic: "健康", CreatedAt: now}}},
				"早睡": {
					{ID: "m1", Type: "memory", Score: 0.6134, Meta: retrieval.EntityMeta{Content: "最近睡眠不好", Topic: "健康", CreatedAt: now}},
					{ID: "m2", Type: "memory", Score: 0.7210, Meta: retrieval.EntityMeta{Content: "晚上11点前睡觉", Topic: "习惯", CreatedAt: now}},
				},
			},
		},
	}
	tool := NewUnifiedSearchTool(sources, &mockEmbedder{}, nil, nil, nil, nil, nil)
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user-1"}

	res := tool.Execute(ec, "semantic_search", mustMarshal(t, map[string]string{"query": "睡眠 早睡"}))
	if !res.Success {
		t.Fatalf("unexpected error: %s", res.Error)
	}

	var out searchResult
	json.Unmarshal(res.Data, &out)

	items := out.Memories.Data.([]interface{})
	if len(items) != 2 {
		t.Fatalf("expected 2 deduped memory items, got %d", len(items))
	}

	// Verify m1 has the higher score from "睡眠" (0.8523, not 0.6134 from "早睡")
	found := make(map[string]map[string]interface{})
	for _, it := range items {
		m := it.(map[string]interface{})
		found[m["id"].(string)] = m
	}

	if m1, ok := found["m1"]; !ok {
		t.Error("missing m1")
	} else if m1["score"].(float64) != 0.8523 {
		t.Errorf("expected m1 score=0.8523 (highest), got %v", m1["score"])
	}

	if m2, ok := found["m2"]; !ok {
		t.Error("missing m2")
	} else if m2["score"].(float64) != 0.721 {
		t.Errorf("expected m2 score=0.721, got %v", m2["score"])
	}
}

func TestUnifiedSearch_SemanticMultiQuerySingleFragment(t *testing.T) {
	// 无空格的 query 走单次搜索原路径，结果不变
	sources := map[string]retrieval.ContextSource{
		"memory": &mockContextSource{name: "memory", pkts: []retrieval.ContextPacket{
			{ID: "m1", Type: "memory", Score: 0.85, Meta: retrieval.EntityMeta{Content: "测试", CreatedAt: time.Now()}},
		}},
	}
	tool := NewUnifiedSearchTool(sources, &mockEmbedder{}, nil, nil, nil, nil, nil)
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user-1"}

	res := tool.Execute(ec, "semantic_search", mustMarshal(t, map[string]string{"query": "最近睡眠"}))
	if !res.Success {
		t.Fatalf("unexpected error: %s", res.Error)
	}

	var out searchResult
	json.Unmarshal(res.Data, &out)
	if out.Memories == nil {
		t.Error("expected memories result")
	}
}

func TestUnifiedSearch_SemanticDescriptionPresent(t *testing.T) {
	now := time.Now()
	sources := map[string]retrieval.ContextSource{
		"memory": &mockContextSource{name: "memory", pkts: []retrieval.ContextPacket{
			{ID: "m1", Type: "memory", Meta: retrieval.EntityMeta{Content: "测试", CreatedAt: now}},
		}},
		"note": &mockContextSource{name: "note", pkts: []retrieval.ContextPacket{
			{ID: "n1", Type: "note", Meta: retrieval.EntityMeta{Content: "笔记", CreatedAt: now}},
		}},
		"summary": &mockContextSource{name: "summary", pkts: []retrieval.ContextPacket{
			{ID: "s1", Type: "summary", Meta: retrieval.SummaryMeta{SummaryID: "s1", SummaryText: "摘要"}},
		}},
		"event": &mockContextSource{name: "event", pkts: []retrieval.ContextPacket{
			{ID: "e1", Type: "event", Meta: retrieval.EventMeta{EventID: "e1", Title: "事件"}},
		}},
	}
	tool := NewUnifiedSearchTool(sources, &mockEmbedder{}, nil, nil, nil, nil, nil)
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user-1"}

	res := tool.Execute(ec, "semantic_search", mustMarshal(t, map[string]string{"query": "测试"}))
	var out searchResult
	json.Unmarshal(res.Data, &out)

	if out.Memories.Description != retrieval.DescMemory {
		t.Errorf("memory description mismatch, got %q", out.Memories.Description)
	}
	if out.Notes.Description != retrieval.DescNotes {
		t.Errorf("note description mismatch, got %q", out.Notes.Description)
	}
	if out.Summaries.Description != retrieval.DescSummary {
		t.Errorf("summary description mismatch, got %q", out.Summaries.Description)
	}
	if out.Events.Description != retrieval.DescEvents {
		t.Errorf("event description mismatch, got %q", out.Events.Description)
	}
}

// ==================== buildSearchResult Meta Fields ====================

func TestUnifiedSearch_MemoryEntityMetaFields(t *testing.T) {
	now := time.Date(2026, 7, 1, 14, 30, 0, 0, time.UTC)
	sources := map[string]retrieval.ContextSource{
		"memory": &mockContextSource{name: "memory", pkts: []retrieval.ContextPacket{
			{ID: "m1", Type: "memory", Meta: retrieval.EntityMeta{Content: "喜欢吃辣", Topic: "偏好", CreatedAt: now}},
		}},
	}
	tool := NewUnifiedSearchTool(sources, &mockEmbedder{}, nil, nil, nil, nil, nil)
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user-1"}

	res := tool.Execute(ec, "semantic_search", mustMarshal(t, map[string]string{"query": "辣"}))
	var out searchResult
	json.Unmarshal(res.Data, &out)

	items := out.Memories.Data.([]interface{})
	first := items[0].(map[string]interface{})
	if first["id"] != "m1" {
		t.Errorf("expected id=m1, got %v", first["id"])
	}
	if first["content"] != "喜欢吃辣" {
		t.Errorf("expected content=喜欢吃辣, got %v", first["content"])
	}
	if first["topic"] != "偏好" {
		t.Errorf("expected topic=偏好, got %v", first["topic"])
	}
	if first["created_at"] == "" {
		t.Error("created_at should not be empty")
	}
}

func TestUnifiedSearch_SummaryMetaFields(t *testing.T) {
	sources := map[string]retrieval.ContextSource{
		"summary": &mockContextSource{name: "summary", pkts: []retrieval.ContextPacket{
			{ID: "s1", Type: "summary", Score: 0.72, Meta: retrieval.SummaryMeta{
				SummaryID: "s1", SummaryText: "讨论了健身计划", Topics: "健康,运动",
				CoverFromMsgID: "msg_1", CoverToMsgID: "msg_5", ParentSummaryID: "parent_s1",
			}},
		}},
	}
	tool := NewUnifiedSearchTool(sources, &mockEmbedder{}, nil, nil, nil, nil, nil)
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user-1"}

	res := tool.Execute(ec, "semantic_search", mustMarshal(t, map[string]string{"query": "健身"}))
	var out searchResult
	json.Unmarshal(res.Data, &out)

	items := out.Summaries.Data.([]interface{})
	first := items[0].(map[string]interface{})
	if first["id"] != "s1" {
		t.Errorf("expected id=s1, got %v", first["id"])
	}
	if first["summary_text"] != "讨论了健身计划" {
		t.Errorf("expected summary_text, got %v", first["summary_text"])
	}
	if first["topics"] != "健康,运动" {
		t.Errorf("expected topics=健康,运动, got %v", first["topics"])
	}
	if first["cover_from_msg_id"] != "msg_1" {
		t.Errorf("expected cover_from_msg_id=msg_1, got %v", first["cover_from_msg_id"])
	}
	if first["cover_to_msg_id"] != "msg_5" {
		t.Errorf("expected cover_to_msg_id=msg_5, got %v", first["cover_to_msg_id"])
	}
	if first["parent_summary_id"] != "parent_s1" {
		t.Errorf("expected parent_summary_id=parent_s1, got %v", first["parent_summary_id"])
	}
}

func TestUnifiedSearch_EventMetaFields(t *testing.T) {
	sources := map[string]retrieval.ContextSource{
		"event": &mockContextSource{name: "event", pkts: []retrieval.ContextPacket{
			{ID: "e1", Type: "event", Score: 0.78, Meta: retrieval.EventMeta{
				EventID: "e1", Title: "日本旅行计划", Summary: "在做攻略", Mode: retrieval.EventModeCompact, MemoryCount: 5,
			}},
		}},
	}
	tool := NewUnifiedSearchTool(sources, &mockEmbedder{}, nil, nil, nil, nil, nil)
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user-1"}

	res := tool.Execute(ec, "semantic_search", mustMarshal(t, map[string]string{"query": "旅行"}))
	var out searchResult
	json.Unmarshal(res.Data, &out)

	items := out.Events.Data.([]interface{})
	first := items[0].(map[string]interface{})
	if first["id"] != "e1" {
		t.Errorf("expected id=e1, got %v", first["id"])
	}
	if first["name"] != "日本旅行计划" {
		t.Errorf("expected name=日本旅行计划, got %v", first["name"])
	}
	if first["summary"] != "在做攻略" {
		t.Errorf("expected summary=在做攻略, got %v", first["summary"])
	}
	if first["mode"] != retrieval.EventModeCompact {
		t.Errorf("expected mode=compact, got %v", first["mode"])
	}
}

func TestUnifiedSearch_EntityCreatedAtZero(t *testing.T) {
	sources := map[string]retrieval.ContextSource{
		"memory": &mockContextSource{name: "memory", pkts: []retrieval.ContextPacket{
			{ID: "m1", Type: "memory", Meta: retrieval.EntityMeta{Content: "无创建时间"}},
		}},
	}
	tool := NewUnifiedSearchTool(sources, &mockEmbedder{}, nil, nil, nil, nil, nil)
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "user-1"}

	res := tool.Execute(ec, "semantic_search", mustMarshal(t, map[string]string{"query": "无"}))
	var out searchResult
	json.Unmarshal(res.Data, &out)

	items := out.Memories.Data.([]interface{})
	first := items[0].(map[string]interface{})
	if first["created_at"] != "" {
		t.Errorf("expected empty created_at for zero time, got %q", first["created_at"])
	}
}

// ==================== keyword_search Tests (DB-backed) ====================

// appReposForTest holds repos for seed operations
type appReposForTest struct {
	mem     *service.UserMemoryRepo
	note    *service.NoteRepo
	summary *service.ConversationSummaryRepo
	event   *service.EventRepo
	msg     *service.MessageRepo
}

func setupUnifiedSearchDB(t *testing.T) (*UnifiedSearchTool, *appReposForTest, func()) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(
		&model.UserMemory{},
		&model.Note{},
		&model.ConversationSummary{},
		&model.Event{},
		&model.Message{},
	); err != nil {
		t.Fatalf("auto-migrate: %v", err)
	}

	repos := &appReposForTest{
		mem:     service.NewUserMemoryRepo(db),
		note:    service.NewNoteRepo(db),
		summary: service.NewConversationSummaryRepo(db),
		event:   service.NewEventRepo(db),
		msg:     service.NewMessageRepo(db),
	}

	tool := NewUnifiedSearchTool(
		nil,
		nil, // embedder not needed for keyword search
		repos.msg,
		repos.mem,
		repos.note,
		repos.summary,
		repos.event,
	)
	return tool, repos, func() {}
}

func seedAllDomains(t *testing.T, repos *appReposForTest, userID string) {
	t.Helper()
	ctx := context.Background()

	memories := []model.UserMemory{
		{ID: "mem-1", UserID: userID, MemoryValue: "用户喜欢吃辣的食物", Topic: "偏好", ContentHash: "h1", MemoryKey: "k1", ConfirmCount: 1, CreatedAt: time.Now().Add(-72 * time.Hour)},
		{ID: "mem-2", UserID: userID, MemoryValue: "用户是后端开发工程师，主要用 Go 和 Python", Topic: "职业", ContentHash: "h2", MemoryKey: "k2", ConfirmCount: 1, CreatedAt: time.Now().Add(-48 * time.Hour)},
		{ID: "mem-3", UserID: userID, MemoryValue: "用户住在深圳南山区", Topic: "住址", ContentHash: "h3", MemoryKey: "k3", ConfirmCount: 1, CreatedAt: time.Now().Add(-24 * time.Hour)},
		{ID: "mem-4", UserID: "other-user", MemoryValue: "另一个用户的记忆不应出现", Topic: "偏好", ContentHash: "h4", MemoryKey: "k4", ConfirmCount: 1, CreatedAt: time.Now()},
	}
	for _, m := range memories {
		if err := repos.mem.Create(ctx, &m); err != nil {
			t.Fatalf("seed memory %s: %v", m.ID, err)
		}
	}

	notes := []model.Note{
		{ID: "note-1", UserID: userID, Content: "辣味菜谱收集：口水鸡、麻辣火锅", Topic: "美食", Status: model.NoteStatusNormal, CreatedAt: time.Now().Add(-96 * time.Hour)},
		{ID: "note-2", UserID: userID, Content: "Go 泛型学习笔记", Topic: "编程", Status: model.NoteStatusNormal, CreatedAt: time.Now().Add(-24 * time.Hour)},
	}
	for _, n := range notes {
		if err := repos.note.Create(ctx, &n); err != nil {
			t.Fatalf("seed note %s: %v", n.ID, err)
		}
	}

	summaries := []model.ConversationSummary{
		{ID: "sum-1", UserID: userID, SummaryText: "上次讨论了健身计划和饮食调整", Topics: "健康,运动", CoverFromMsgID: "m1", CoverToMsgID: "m5", Status: "active", CreatedAt: time.Now().Add(-120 * time.Hour)},
	}
	for _, s := range summaries {
		if err := repos.summary.Create(ctx, &s); err != nil {
			t.Fatalf("seed summary %s: %v", s.ID, err)
		}
	}

	events := []model.Event{
		{ID: "ev-1", UserID: userID, Name: "日本旅行计划", Summary: "正在做攻略和预算", CreatedAt: time.Now().Add(-240 * time.Hour)},
		{ID: "ev-2", UserID: userID, Name: "学习 Rust", Summary: "通过写一个 KV 存储练手", CreatedAt: time.Now().Add(-120 * time.Hour)},
	}
	for _, e := range events {
		if err := repos.event.Create(ctx, &e); err != nil {
			t.Fatalf("seed event %s: %v", e.ID, err)
		}
	}

	messages := []model.Message{
		{ID: "msg-1", UserID: userID, ConversationID: "conv-1", Role: "user", Content: "最近吃麻辣火锅上火了", Status: model.MsgStatusNormal, CreatedAt: time.Now().Add(-48 * time.Hour)},
		{ID: "msg-2", UserID: userID, ConversationID: "conv-1", Role: "user", Content: "Go 的泛型约束怎么写比较优雅？", Status: model.MsgStatusNormal, CreatedAt: time.Now().Add(-24 * time.Hour)},
		{ID: "msg-3", UserID: userID, ConversationID: "conv-2", Role: "assistant", Content: "你可以用 interface 定义约束", Status: model.MsgStatusNormal, CreatedAt: time.Now().Add(-24 * time.Hour)},
		{ID: "msg-4", UserID: "other-user", ConversationID: "conv-3", Role: "user", Content: "另一个用户的消息不应出现", Status: model.MsgStatusNormal, CreatedAt: time.Now()},
	}
	for _, m := range messages {
		if err := repos.msg.Create(ctx, &m); err != nil {
			t.Fatalf("seed message %s: %v", m.ID, err)
		}
	}
}

func TestUnifiedSearch_KeywordBasic(t *testing.T) {
	tool, repos, cleanup := setupUnifiedSearchDB(t)
	defer cleanup()
	userID := "user-1"
	seedAllDomains(t, repos, userID)

	ec := &ExecuteContext{Ctx: context.Background(), UserID: userID}

	res := tool.Execute(ec, "keyword_search", mustMarshal(t, map[string]string{"keywords": "辣"}))
	if !res.Success {
		t.Fatalf("keyword search failed: %s", res.Error)
	}

	var out searchResult
	if err := json.Unmarshal(res.Data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if out.Memories == nil {
		t.Error("expected memories for keyword '辣'")
	}
	if out.Notes == nil {
		t.Error("expected notes for keyword '辣'")
	}
}

func TestUnifiedSearch_KeywordTopicFilter(t *testing.T) {
	tool, repos, cleanup := setupUnifiedSearchDB(t)
	defer cleanup()
	userID := "user-1"
	seedAllDomains(t, repos, userID)

	ec := &ExecuteContext{Ctx: context.Background(), UserID: userID}

	res := tool.Execute(ec, "keyword_search", mustMarshal(t, map[string]interface{}{
		"keywords": "用户",
		"topic":    "偏好",
		"types":    []string{"memory"},
	}))
	if !res.Success {
		t.Fatalf("keyword search failed: %s", res.Error)
	}

	var out searchResult
	json.Unmarshal(res.Data, &out)

	items := out.Memories.Data.([]interface{})
	for _, m := range items {
		mm := m.(map[string]interface{})
		if mm["topic"] != "偏好" {
			t.Errorf("expected topic=偏好, got %v", mm["topic"])
		}
	}
}

func TestUnifiedSearch_KeywordNoMatch(t *testing.T) {
	tool, repos, cleanup := setupUnifiedSearchDB(t)
	defer cleanup()
	userID := "user-1"
	seedAllDomains(t, repos, userID)

	ec := &ExecuteContext{Ctx: context.Background(), UserID: userID}

	res := tool.Execute(ec, "keyword_search", mustMarshal(t, map[string]string{"keywords": "外星人"}))
	if !res.Success {
		t.Fatalf("keyword search failed: %s", res.Error)
	}

	var out map[string]interface{}
	json.Unmarshal(res.Data, &out)
	if len(out) != 0 {
		t.Errorf("expected empty result, got %d keys", len(out))
	}
}

func TestUnifiedSearch_KeywordScoreExcluded(t *testing.T) {
	tool, repos, cleanup := setupUnifiedSearchDB(t)
	defer cleanup()
	userID := "user-1"
	seedAllDomains(t, repos, userID)

	ec := &ExecuteContext{Ctx: context.Background(), UserID: userID}

	res := tool.Execute(ec, "keyword_search", mustMarshal(t, map[string]string{"keywords": "辣"}))
	var out searchResult
	json.Unmarshal(res.Data, &out)

	items := out.Memories.Data.([]interface{})
	first := items[0].(map[string]interface{})
	if _, ok := first["score"]; ok {
		t.Error("keyword search should not include score field")
	}
}

func TestUnifiedSearch_KeywordDescriptionPresent(t *testing.T) {
	tool, repos, cleanup := setupUnifiedSearchDB(t)
	defer cleanup()
	userID := "user-1"
	seedAllDomains(t, repos, userID)

	ec := &ExecuteContext{Ctx: context.Background(), UserID: userID}

	res := tool.Execute(ec, "keyword_search", mustMarshal(t, map[string]string{"keywords": "辣"}))
	var out map[string]interface{}
	json.Unmarshal(res.Data, &out)

	for _, domain := range []string{"memories", "notes"} {
		domainData, ok := out[domain]
		if !ok {
			continue
		}
		domainMap := domainData.(map[string]interface{})
		if desc, ok := domainMap["description"].(string); !ok || desc == "" {
			t.Errorf("domain %s should have non-empty description", domain)
		}
	}
}

func TestUnifiedSearch_KeywordMultiKeyword(t *testing.T) {
	tool, repos, cleanup := setupUnifiedSearchDB(t)
	defer cleanup()
	userID := "user-1"
	seedAllDomains(t, repos, userID)

	ec := &ExecuteContext{Ctx: context.Background(), UserID: userID}

	res := tool.Execute(ec, "keyword_search", mustMarshal(t, map[string]interface{}{
		"keywords": "辣 后端",
		"types":    []string{"memory"},
	}))
	if !res.Success {
		t.Fatalf("keyword search failed: %s", res.Error)
	}

	var out searchResult
	json.Unmarshal(res.Data, &out)

	items := out.Memories.Data.([]interface{})
	// "辣" matches mem-1, "后端" matches mem-2, OR semantics
	if len(items) < 2 {
		t.Errorf("expected at least 2 results (OR match on 辣+后端), got %d", len(items))
	}
}

func TestUnifiedSearch_KeywordSummaryFields(t *testing.T) {
	tool, repos, cleanup := setupUnifiedSearchDB(t)
	defer cleanup()
	userID := "user-1"
	seedAllDomains(t, repos, userID)

	ec := &ExecuteContext{Ctx: context.Background(), UserID: userID}

	res := tool.Execute(ec, "keyword_search", mustMarshal(t, map[string]string{"keywords": "健身"}))
	var out searchResult
	json.Unmarshal(res.Data, &out)

	if out.Summaries == nil {
		t.Fatal("expected summaries for keyword '健身'")
	}

	items := out.Summaries.Data.([]interface{})
	first := items[0].(map[string]interface{})
	if first["id"] != "sum-1" {
		t.Errorf("expected id=sum-1, got %v", first["id"])
	}
	if first["summary_text"] == "" {
		t.Error("summary_text should not be empty")
	}
	if first["topics"] != "健康,运动" {
		t.Errorf("expected topics=健康,运动, got %v", first["topics"])
	}
	if first["cover_from_msg_id"] != "m1" {
		t.Errorf("expected cover_from_msg_id=m1, got %v", first["cover_from_msg_id"])
	}
}

func TestUnifiedSearch_KeywordEvent(t *testing.T) {
	tool, repos, cleanup := setupUnifiedSearchDB(t)
	defer cleanup()
	userID := "user-1"
	seedAllDomains(t, repos, userID)

	ec := &ExecuteContext{Ctx: context.Background(), UserID: userID}

	t.Run("match event name", func(t *testing.T) {
		res := tool.Execute(ec, "keyword_search", mustMarshal(t, map[string]string{"keywords": "日本"}))
		var out map[string]interface{}
		json.Unmarshal(res.Data, &out)
		if _, ok := out["events"]; !ok {
			t.Error("expected events for keyword '日本'")
		}
	})

	t.Run("match event summary", func(t *testing.T) {
		res := tool.Execute(ec, "keyword_search", mustMarshal(t, map[string]string{"keywords": "KV"}))
		var out map[string]interface{}
		json.Unmarshal(res.Data, &out)
		if _, ok := out["events"]; !ok {
			t.Error("expected events for keyword 'KV'")
		}
	})
}

// ==================== History Message Search ====================

func TestUnifiedSearch_KeywordHistoryBasic(t *testing.T) {
	tool, repos, cleanup := setupUnifiedSearchDB(t)
	defer cleanup()
	userID := "user-1"
	seedAllDomains(t, repos, userID)

	ec := &ExecuteContext{Ctx: context.Background(), UserID: userID}

	res := tool.Execute(ec, "keyword_search", mustMarshal(t, map[string]interface{}{
		"keywords": "麻辣火锅",
		"types":    []string{"history"},
	}))
	if !res.Success {
		t.Fatalf("history keyword search failed: %s", res.Error)
	}

	var out searchResult
	if err := json.Unmarshal(res.Data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if out.History == nil {
		t.Fatal("expected history results for keyword '麻辣火锅'")
	}

	items := out.History.Data.([]interface{})
	if len(items) != 1 {
		t.Fatalf("expected 1 history item, got %d", len(items))
	}
	first := items[0].(map[string]interface{})
	if first["content"].(string) != "最近吃麻辣火锅上火了" {
		t.Errorf("expected content '最近吃麻辣火锅上火了', got %q", first["content"])
	}
	if first["conversation_id"] != "conv-1" {
		t.Errorf("expected conversation_id=conv-1, got %v", first["conversation_id"])
	}
}

func TestUnifiedSearch_KeywordHistorySummaryFields(t *testing.T) {
	tool, repos, cleanup := setupUnifiedSearchDB(t)
	defer cleanup()
	userID := "user-1"
	seedAllDomains(t, repos, userID)

	ec := &ExecuteContext{Ctx: context.Background(), UserID: userID}

	res := tool.Execute(ec, "keyword_search", mustMarshal(t, map[string]interface{}{
		"keywords": "泛型",
		"types":    []string{"history"},
	}))
	if !res.Success {
		t.Fatalf("history keyword search failed: %s", res.Error)
	}

	var out searchResult
	if err := json.Unmarshal(res.Data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if out.History == nil {
		t.Fatal("expected history results for keyword '泛型'")
	}
	if out.History.Description != retrieval.DescMessages {
		t.Errorf("expected history description, got %q", out.History.Description)
	}

	items := out.History.Data.([]interface{})
	first := items[0].(map[string]interface{})
	if _, ok := first["score"]; ok {
		t.Error("keyword search should not include score field")
	}
	if first["created_at"] == "" {
		t.Error("created_at should not be empty")
	}
}

func TestUnifiedSearch_KeywordHistoryUserIsolation(t *testing.T) {
	tool, repos, cleanup := setupUnifiedSearchDB(t)
	defer cleanup()
	userID := "user-1"
	seedAllDomains(t, repos, userID)

	ec := &ExecuteContext{Ctx: context.Background(), UserID: "other-user"}

	res := tool.Execute(ec, "keyword_search", mustMarshal(t, map[string]interface{}{
		"keywords": "麻辣 泛型",
		"types":    []string{"history"},
	}))
	if !res.Success {
		t.Fatalf("history keyword search failed: %s", res.Error)
	}

	var out searchResult
	json.Unmarshal(res.Data, &out)
	if out.History != nil {
		t.Error("history should be nil for other-user")
	}
}

// ==================== User Isolation ====================

func TestUnifiedSearch_KeywordUserIsolation(t *testing.T) {
	tool, repos, cleanup := setupUnifiedSearchDB(t)
	defer cleanup()
	userID := "user-1"
	seedAllDomains(t, repos, userID)

	ec := &ExecuteContext{Ctx: context.Background(), UserID: "other-user"}

	res := tool.Execute(ec, "keyword_search", mustMarshal(t, map[string]string{"keywords": "辣 后端 Go 健身"}))
	if !res.Success {
		t.Fatalf("keyword search failed: %s", res.Error)
	}

	var out map[string]interface{}
	json.Unmarshal(res.Data, &out)
	if len(out) != 0 {
		t.Errorf("expected empty result for other-user, got %d keys", len(out))
	}
}

// ==================== Semantic Search with Keyword Fallback (not applicable) ====================

// Note: semantic_search and keyword_search are separate functions.
// The tool does not auto-fallback between them; the LLM decides which to call.
// The test coverage above independently verifies each path.

func TestMergePacketBatches_TruncatesPerType(t *testing.T) {
	// 3 个子查询 × 每域 2 条 → 合并去重后 3 条同域不同 ID，maxPerType=2 应截断
	batches := []map[string][]retrieval.ContextPacket{
		{"memory": {
			{ID: "m1", Type: "memory", Score: 0.9},
			{ID: "m2", Type: "memory", Score: 0.7},
		}},
		{"memory": {
			{ID: "m3", Type: "memory", Score: 0.8},
		}},
		{"memory": {
			{ID: "m4", Type: "memory", Score: 0.6},
		}},
	}
	out := mergePacketBatches(batches, 2)
	mem := out["memory"]
	if len(mem) != 2 {
		t.Fatalf("expected 2 memory after truncation, got %d", len(mem))
	}
	// 按 score 降序：m1(0.9), m3(0.8)
	if mem[0].ID != "m1" || mem[1].ID != "m3" {
		t.Errorf("expected top-2 by score (m1,m3), got %s,%s", mem[0].ID, mem[1].ID)
	}
}

func TestMergePacketBatches_DedupKeepsHighestScore(t *testing.T) {
	batches := []map[string][]retrieval.ContextPacket{
		{"note": {{ID: "n1", Type: "note", Score: 0.5}}},
		{"note": {{ID: "n1", Type: "note", Score: 0.95}}}, // 同 ID 高分
	}
	out := mergePacketBatches(batches, 5)
	notes := out["note"]
	if len(notes) != 1 {
		t.Fatalf("expected 1 after dedup, got %d", len(notes))
	}
	if notes[0].Score != 0.95 {
		t.Errorf("expected highest score kept (0.95), got %.2f", notes[0].Score)
	}
}

func TestMergePacketBatches_DefaultMaxPerType(t *testing.T) {
	batches := []map[string][]retrieval.ContextPacket{
		{"event": {
			{ID: "e1", Type: "event", Score: 1},
			{ID: "e2", Type: "event", Score: 1},
			{ID: "e3", Type: "event", Score: 1},
			{ID: "e4", Type: "event", Score: 1},
			{ID: "e5", Type: "event", Score: 1},
			{ID: "e6", Type: "event", Score: 1},
		}},
	}
	out := mergePacketBatches(batches, 0)
	if len(out["event"]) != 5 {
		t.Errorf("default maxPerType should be 5, got %d", len(out["event"]))
	}
}
