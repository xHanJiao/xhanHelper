package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

type eventTestEnv struct {
	tool     *EventTool
	event    *service.EventRepo
	mem      *service.UserMemoryRepo
	conv     *service.ConversationRepo
	convID   string
	userID   string
	readOnly Tool
}

func setupEventToolDB(t *testing.T) *eventTestEnv {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Event{}, &model.UserMemory{}, &model.Conversation{}); err != nil {
		t.Fatalf("auto-migrate: %v", err)
	}
	env := &eventTestEnv{
		event:  service.NewEventRepo(db),
		mem:    service.NewUserMemoryRepo(db),
		conv:   service.NewConversationRepo(db),
		convID: "conv-1",
		userID: "user-1",
	}
	env.tool = NewEventTool(env.event, env.mem, env.conv, nil, nil, 3)
	env.readOnly = env.tool.ReadOnly()

	if err := env.conv.Create(context.Background(), &model.Conversation{ID: env.convID, UserID: env.userID, Extra: "{}"}); err != nil {
		t.Fatalf("seed conversation: %v", err)
	}
	return env
}

func (e *eventTestEnv) ec() *ExecuteContext {
	return &ExecuteContext{Ctx: context.Background(), UserID: e.userID, ConvID: e.convID}
}

func (e *eventTestEnv) seedEvent(t *testing.T, id, name, summary string) {
	t.Helper()
	if err := e.event.Create(context.Background(), &model.Event{ID: id, UserID: e.userID, Name: name, Summary: summary}); err != nil {
		t.Fatalf("seed event %s: %v", id, err)
	}
}

func (e *eventTestEnv) seedMemory(t *testing.T, id, content string, eventID *string) {
	t.Helper()
	facts := `["用户原话"]`
	m := &model.UserMemory{
		ID: id, UserID: e.userID, MemoryKey: "k", ContentHash: id,
		MemoryValue: content, EventID: eventID, Facts: &facts,
	}
	if err := e.mem.Create(context.Background(), m); err != nil {
		t.Fatalf("seed memory %s: %v", id, err)
	}
}

func TestEventTool_ListAndGet(t *testing.T) {
	env := setupEventToolDB(t)
	env.seedEvent(t, "e1", "回西安准备", "打包中")
	env.seedEvent(t, "e2", "驾照考取", "科三待补考")
	eid := "e1"
	env.seedMemory(t, "m1", "已经买了火车票", &eid)
	env.seedMemory(t, "m2", "还没收拾行李", &eid)
	env.seedMemory(t, "m3", "普通记忆", nil)

	res := env.tool.Execute(env.ec(), "list", nil)
	if !res.Success {
		t.Fatalf("list: %s", res.Error)
	}
	var listOut struct {
		Events []eventHit `json:"events"`
	}
	if err := json.Unmarshal(res.Data, &listOut); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	if len(listOut.Events) != 2 {
		t.Fatalf("expected 2 events, got %+v", listOut.Events)
	}
	byID := map[string]eventHit{}
	for _, h := range listOut.Events {
		byID[h.ID] = h
	}
	if byID["e1"].MemoryCount != 2 {
		t.Errorf("e1 memory_count = %d, want 2", byID["e1"].MemoryCount)
	}

	res = env.tool.Execute(env.ec(), "get", mustMarshal(t, map[string]string{"name": "回西安准备"}))
	if !res.Success {
		t.Fatalf("get: %s", res.Error)
	}
	var detail struct {
		ID       string            `json:"id"`
		Memories []eventMemoryItem `json:"memories"`
	}
	if err := json.Unmarshal(res.Data, &detail); err != nil {
		t.Fatalf("unmarshal detail: %v", err)
	}
	if detail.ID != "e1" || len(detail.Memories) != 2 {
		t.Fatalf("unexpected detail: %+v", detail)
	}
	if len(detail.Memories[0].Facts) != 1 || detail.Memories[0].Facts[0] != "用户原话" {
		t.Errorf("facts not parsed: %+v", detail.Memories[0].Facts)
	}
}

func TestEventTool_EnableDisableAndCap(t *testing.T) {
	env := setupEventToolDB(t)
	for _, id := range []string{"e1", "e2", "e3", "e4"} {
		env.seedEvent(t, id, id, "")
	}

	for _, id := range []string{"e1", "e2", "e3"} {
		res := env.tool.Execute(env.ec(), "enable", mustMarshal(t, map[string]string{"event_id": id}))
		if !res.Success {
			t.Fatalf("enable %s: %s", id, res.Error)
		}
	}
	res := env.tool.Execute(env.ec(), "enable", mustMarshal(t, map[string]string{"event_id": "e4"}))
	if res.Success {
		t.Fatal("expected cap error on 4th enable")
	}

	extra, err := env.conv.GetExtra(context.Background(), env.convID)
	if err != nil {
		t.Fatalf("get extra: %v", err)
	}
	if extra.EventFocus == nil || len(extra.EventFocus.Enabled) != 3 {
		t.Fatalf("unexpected extra: %+v", extra)
	}

	if res := env.tool.Execute(env.ec(), "disable", mustMarshal(t, map[string]string{"event_id": "e2"})); !res.Success {
		t.Fatalf("disable: %s", res.Error)
	}
	extra, err = env.conv.GetExtra(context.Background(), env.convID)
	if err != nil {
		t.Fatalf("get extra: %v", err)
	}
	if len(extra.EventFocus.Enabled) != 2 {
		t.Fatalf("expected 2 pinned, got %+v", extra.EventFocus.Enabled)
	}

	// 幂等：重复 enable 不占额度
	if res := env.tool.Execute(env.ec(), "enable", mustMarshal(t, map[string]string{"event_id": "e1"})); !res.Success {
		t.Fatalf("re-enable: %s", res.Error)
	}
	extra, _ = env.conv.GetExtra(context.Background(), env.convID)
	if len(extra.EventFocus.Enabled) != 2 {
		t.Fatalf("re-enable should not duplicate, got %+v", extra.EventFocus.Enabled)
	}
}

func TestEventTool_UpdateDeleteMerge(t *testing.T) {
	env := setupEventToolDB(t)
	env.seedEvent(t, "e1", "旧名字", "旧状态")
	env.seedEvent(t, "e2", "驾照考取", "科三待补考")
	eid := "e1"
	env.seedMemory(t, "m1", "挂在 e1 上", &eid)
	env.seedMemory(t, "m2", "还没归属", nil)

	res := env.tool.Execute(env.ec(), "update", mustMarshal(t, map[string]interface{}{
		"event_id": "e1", "new_name": "新名字", "summary": "新状态", "memory_ids": []string{"m2"},
	}))
	if !res.Success {
		t.Fatalf("update: %s", res.Error)
	}
	e, _ := env.event.GetByID(context.Background(), env.userID, "e1")
	if e.Name != "新名字" || e.Summary != "新状态" {
		t.Fatalf("update not applied: %+v", e)
	}
	m2, _ := env.mem.FindByID(context.Background(), env.userID, "m2")
	if m2.EventID == nil || *m2.EventID != "e1" {
		t.Fatalf("memory_ids not assigned: %+v", m2.EventID)
	}

	res = env.tool.Execute(env.ec(), "merge", mustMarshal(t, map[string]string{"from_id": "e1", "to_id": "e2"}))
	if !res.Success {
		t.Fatalf("merge: %s", res.Error)
	}
	m, _ := env.mem.FindByID(context.Background(), env.userID, "m1")
	if m.EventID == nil || *m.EventID != "e2" {
		t.Fatalf("memory not moved: %+v", m.EventID)
	}

	res = env.tool.Execute(env.ec(), "delete", mustMarshal(t, map[string]string{"event_id": "e2"}))
	if !res.Success {
		t.Fatalf("delete: %s", res.Error)
	}
	m, _ = env.mem.FindByID(context.Background(), env.userID, "m1")
	if m.EventID != nil {
		t.Fatalf("deleted event memories must return to pool, got %+v", m.EventID)
	}
}

func TestEventTool_KeywordSearch(t *testing.T) {
	env := setupEventToolDB(t)
	env.seedEvent(t, "e1", "回西安准备", "打包中")
	env.seedEvent(t, "e2", "驾照考取", "科三待补考")

	res := env.tool.Execute(env.ec(), "search", mustMarshal(t, map[string]string{"keyword": "驾照"}))
	if !res.Success {
		t.Fatalf("search: %s", res.Error)
	}
	var out struct {
		Events []eventHit `json:"events"`
	}
	if err := json.Unmarshal(res.Data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Events) != 1 || out.Events[0].ID != "e2" {
		t.Fatalf("unexpected search result: %+v", out.Events)
	}
}

func TestEventTool_ReadOnlyViewOnlyExposesGet(t *testing.T) {
	env := setupEventToolDB(t)
	env.seedEvent(t, "e1", "回西安准备", "打包中")

	fns := env.readOnly.Functions()
	if len(fns) != 1 || fns[0].Name != "get" {
		t.Fatalf("read-only view should expose only get, got %+v", fns)
	}
	if res := env.readOnly.Execute(env.ec(), "delete", mustMarshal(t, map[string]string{"event_id": "e1"})); res.Success {
		t.Fatal("read-only view must reject write functions")
	}
	if res := env.readOnly.Execute(env.ec(), "get", mustMarshal(t, map[string]string{"event_id": "e1"})); !res.Success {
		t.Fatalf("read-only get failed: %s", res.Error)
	}
}

func TestEventTool_EnablePrunesStalePinnedIDs(t *testing.T) {
	env := setupEventToolDB(t)
	env.seedEvent(t, "e1", "回西安准备", "")
	env.seedEvent(t, "e2", "驾照考取", "")
	if err := env.conv.SaveExtra(context.Background(), env.convID, &model.ConversationExtra{
		EventFocus: &model.EventFocus{Enabled: []string{"gone", "e1"}},
	}); err != nil {
		t.Fatalf("seed extra: %v", err)
	}

	if res := env.tool.Execute(env.ec(), "enable", mustMarshal(t, map[string]string{"event_id": "e2"})); !res.Success {
		t.Fatalf("enable: %s", res.Error)
	}
	extra, err := env.conv.GetExtra(context.Background(), env.convID)
	if err != nil {
		t.Fatalf("get extra: %v", err)
	}
	if len(extra.EventFocus.Enabled) != 2 {
		t.Fatalf("stale pinned id should be pruned, got %+v", extra.EventFocus.Enabled)
	}
	for _, id := range extra.EventFocus.Enabled {
		if id == "gone" {
			t.Fatalf("stale id not pruned: %+v", extra.EventFocus.Enabled)
		}
	}
}

func TestEventTool_NameLookupDeletedEvent(t *testing.T) {
	env := setupEventToolDB(t)
	env.seedEvent(t, "e1", "回西安准备", "")

	if res := env.tool.Execute(env.ec(), "delete", mustMarshal(t, map[string]string{"event_id": "e1"})); !res.Success {
		t.Fatalf("delete: %s", res.Error)
	}

	// 按名字：与按 ID 同一句文案
	res := env.tool.Execute(env.ec(), "enable", mustMarshal(t, map[string]string{"name": "回西安准备"}))
	if res.Success || !strings.Contains(res.Error, "已删除") {
		t.Fatalf("expected deleted-event error, got %+v", res)
	}

	// get 仍能看到已删事件
	res = env.tool.Execute(env.ec(), "get", mustMarshal(t, map[string]string{"name": "回西安准备"}))
	if !res.Success {
		t.Fatalf("get deleted: %s", res.Error)
	}
	var detail struct {
		IsDeleted bool `json:"is_deleted"`
	}
	if err := json.Unmarshal(res.Data, &detail); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !detail.IsDeleted {
		t.Fatal("get should return is_deleted=true")
	}
}
