package agent

import (
	"context"
	"encoding/json"
	"testing"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/tool"
)

// mockExposeTool 测试用工具：有函数定义，用于验证暴露后请求体包含其完整 schema。
type mockExposeTool struct{}

func (m *mockExposeTool) Name() string        { return "note" }
func (m *mockExposeTool) Description() string { return "笔记工具" }
func (m *mockExposeTool) Functions() []tool.FuncSchema {
	return []tool.FuncSchema{{Name: "record_note"}}
}
func (m *mockExposeTool) ReadOnlyFuncs() map[string]bool { return nil }
func (m *mockExposeTool) Execute(ec *tool.ExecuteContext, funcName string, args json.RawMessage) *tool.Result {
	return &tool.Result{Success: true}
}

// namedMockTool 测试用工具：可指定名字与函数名。
type namedMockTool struct {
	name string
	fns  []string
}

func (m *namedMockTool) Name() string                   { return m.name }
func (m *namedMockTool) Description() string            { return "mock tool: " + m.name }
func (m *namedMockTool) ReadOnlyFuncs() map[string]bool { return nil }
func (m *namedMockTool) Functions() []tool.FuncSchema {
	out := make([]tool.FuncSchema, len(m.fns))
	for i, f := range m.fns {
		out[i] = tool.FuncSchema{Name: f}
	}
	return out
}
func (m *namedMockTool) Execute(ec *tool.ExecuteContext, funcName string, args json.RawMessage) *tool.Result {
	return &tool.Result{Success: true}
}

func TestExposure_LoadFromExtra_KeyMatch(t *testing.T) {
	convRepo := &mockConvRepo{extra: &model.ConversationExtra{
		ToolExposure: &model.ToolExposure{SessionStartMsgID: "window-1", Tools: []string{"note.record_note"}},
	}}
	o := &Orchestrator{convRepo: convRepo, useNativeFC: true}
	state := &reactState{}
	req := &RunRequest{Window: WindowResult{SessionStartMsgID: "window-1"}}
	o.loadExposure(context.Background(), req, &model.Message{ID: "msg-1"}, state)

	if !state.exposedTools["note.record_note"] {
		t.Error("expected note.record_note loaded from Extra when key matches")
	}
}

func TestExposure_LoadFromExtra_KeyMismatchResets(t *testing.T) {
	convRepo := &mockConvRepo{extra: &model.ConversationExtra{
		ToolExposure: &model.ToolExposure{SessionStartMsgID: "old-window", Tools: []string{"note.record_note"}},
	}}
	o := &Orchestrator{convRepo: convRepo, useNativeFC: true}
	state := &reactState{}
	req := &RunRequest{Window: WindowResult{SessionStartMsgID: "new-window"}}
	o.loadExposure(context.Background(), req, &model.Message{ID: "msg-2"}, state)

	if state.exposedTools["note.record_note"] {
		t.Error("expected stale window exposure to be ignored")
	}
}

func TestExposure_LoadFromExtra_LegacyBareGroupExpands(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(&mockExposeTool{}) // note: record_note
	convRepo := &mockConvRepo{extra: &model.ConversationExtra{
		ToolExposure: &model.ToolExposure{SessionStartMsgID: "window-1", Tools: []string{"note"}},
	}}
	o := &Orchestrator{convRepo: convRepo, registry: reg, useNativeFC: true}
	state := &reactState{}
	req := &RunRequest{Window: WindowResult{SessionStartMsgID: "window-1"}}
	o.loadExposure(context.Background(), req, &model.Message{ID: "msg-1"}, state)

	if !state.exposedTools["note.record_note"] {
		t.Error("expected legacy bare group name expanded to all its functions")
	}
}

func TestExposure_PersistWritesBack(t *testing.T) {
	convRepo := &mockConvRepo{extra: &model.ConversationExtra{}}
	o := &Orchestrator{convRepo: convRepo, useNativeFC: true}
	state := &reactState{exposedTools: map[string]bool{"note.record_note": true, "bookkeeping.create_bill": true}}
	req := &RunRequest{Window: WindowResult{SessionStartMsgID: "window-1"}}
	o.persistExposure(context.Background(), req, &model.Message{ID: "msg-3"}, state)

	if convRepo.extra.ToolExposure == nil {
		t.Fatal("expected ToolExposure persisted")
	}
	if convRepo.extra.ToolExposure.SessionStartMsgID != "window-1" {
		t.Errorf("SessionStartMsgID: want window-1, got %s", convRepo.extra.ToolExposure.SessionStartMsgID)
	}
	if len(convRepo.extra.ToolExposure.Tools) != 2 {
		t.Errorf("Tools: want 2, got %v", convRepo.extra.ToolExposure.Tools)
	}
	// 字节稳定：排序写出
	if convRepo.extra.ToolExposure.Tools[0] != "bookkeeping.create_bill" ||
		convRepo.extra.ToolExposure.Tools[1] != "note.record_note" {
		t.Errorf("Tools: want sorted entries, got %v", convRepo.extra.ToolExposure.Tools)
	}
}

func TestExposure_PersistPreservesOtherFields(t *testing.T) {
	// 并发写入的其它字段（used_tool_ids 等）不能被覆盖
	convRepo := &mockConvRepo{extra: &model.ConversationExtra{UsedToolIDs: []string{"tool-1"}}}
	o := &Orchestrator{convRepo: convRepo, useNativeFC: true}
	state := &reactState{exposedTools: map[string]bool{"note.record_note": true}}
	req := &RunRequest{Window: WindowResult{SessionStartMsgID: "window-1"}}
	o.persistExposure(context.Background(), req, &model.Message{ID: "msg-5"}, state)

	if len(convRepo.extra.UsedToolIDs) != 1 || convRepo.extra.UsedToolIDs[0] != "tool-1" {
		t.Errorf("used_tool_ids must be preserved, got %v", convRepo.extra.UsedToolIDs)
	}
}

func TestExposure_PersistSkipsEmpty(t *testing.T) {
	convRepo := &mockConvRepo{extra: &model.ConversationExtra{}}
	o := &Orchestrator{convRepo: convRepo, useNativeFC: true}
	state := &reactState{} // no exposed funcs
	req := &RunRequest{Window: WindowResult{SessionStartMsgID: "window-1"}}
	o.persistExposure(context.Background(), req, &model.Message{ID: "msg-4"}, state)

	if convRepo.extra.ToolExposure != nil {
		t.Error("expected no write when exposure is empty")
	}
}

func TestExposure_MkLlmChatReq_OnlyExposedFuncs(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(&mockExposeTool{}) // note: record_note
	reg.Register(&namedMockTool{name: "bookkeeping", fns: []string{"create_bill", "query_bills"}})
	o := &Orchestrator{
		contextFormatter: &mockFormatter{},
		registry:         reg,
		useNativeFC:      true,
	}
	req := &RunRequest{UserID: "u1"}
	// note 暴露 record_note；bookkeeping 只暴露 create_bill（query_bills 未暴露）
	state := &reactState{msgs: []llm.Message{}, exposedTools: map[string]bool{
		"note.record_note":        true,
		"bookkeeping.create_bill": true,
	}}
	cr := o.mkLlmChatReq(req, "sys", state)

	if len(cr.Tools) != 3 {
		t.Fatalf("want 3 tools (expose_tools + note + bookkeeping), got %d: %+v", len(cr.Tools), toolNames(cr.Tools))
	}
	names := toolNames(cr.Tools)
	if names[0] != "expose_tools" || names[1] != "bookkeeping" || names[2] != "note" {
		t.Errorf("unexpected tool order/names: %v", names)
	}
	for _, d := range cr.Tools {
		switch d.Name {
		case "note":
			if len(d.Functions) != 1 || d.Functions[0].Name != "record_note" {
				t.Errorf("note should expose only record_note, got %+v", d.Functions)
			}
		case "bookkeeping":
			if len(d.Functions) != 1 || d.Functions[0].Name != "create_bill" {
				t.Errorf("bookkeeping should expose only create_bill (query_bills not exposed), got %+v", d.Functions)
			}
		}
	}
}

func TestExposure_MakeExposeFn_Validates(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(&mockExposeTool{}) // note: record_note
	reg.Register(&namedMockTool{name: "bookkeeping", fns: []string{"create_bill"}})
	reg.Register(&namedMockTool{name: "webfetch", fns: []string{"fetch"}})
	reg.HideTool("webfetch")
	o := &Orchestrator{
		registry:     reg,
		useNativeFC:  true,
		toolExcludes: []string{"bookkeeping"},
	}
	state := &reactState{exposedTools: map[string]bool{}}
	fn := o.makeExposeFn(state, nil)

	res := fn([]tool.ExposeSpec{
		{Tool: "note", Functions: []string{"record_note"}},
		{Tool: "bookkeeping", Functions: []string{"create_bill"}},
		{Tool: "webfetch", Functions: []string{"fetch"}},
		{Tool: "expose_tools", Functions: []string{"enable"}},
		{Tool: "ghost", Functions: []string{"x"}},
	})
	if len(res.Enabled) != 1 || res.Enabled[0] != "note.record_note" {
		t.Errorf("Enabled: want [note.record_note], got %v", res.Enabled)
	}
	if !state.exposedTools["note.record_note"] {
		t.Error("note.record_note should be marked exposed")
	}
	if state.exposedTools["bookkeeping.create_bill"] || state.exposedTools["webfetch.fetch"] {
		t.Error("excluded/hidden tools must not be exposed")
	}
	rejected := map[string]bool{}
	for _, r := range res.Rejected {
		rejected[r] = true
	}
	for _, want := range []string{"bookkeeping", "webfetch", "expose_tools", "ghost"} {
		if !rejected[want] {
			t.Errorf("expected %s rejected, got %v", want, res.Rejected)
		}
	}
}

func TestExposure_MakeExposeFn_RejectsUnknownFunc(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(&mockExposeTool{}) // note: record_note
	o := &Orchestrator{registry: reg, useNativeFC: true}
	state := &reactState{exposedTools: map[string]bool{}}
	fn := o.makeExposeFn(state, nil)

	res := fn([]tool.ExposeSpec{{Tool: "note", Functions: []string{"record_note", "bogus"}}})
	if len(res.Enabled) != 1 || res.Enabled[0] != "note.record_note" {
		t.Errorf("Enabled: want [note.record_note], got %v", res.Enabled)
	}
	if !state.exposedTools["note.record_note"] {
		t.Error("valid function should still be exposed")
	}
	if state.exposedTools["note.bogus"] {
		t.Error("unknown function must not be exposed")
	}
	found := false
	for _, r := range res.Rejected {
		if r == "note.bogus" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected note.bogus rejected, got %v", res.Rejected)
	}
}

func TestExposure_MakeExposeFn_ClientTools(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(&mockExposeTool{}) // note: record_note
	o := &Orchestrator{registry: reg, useNativeFC: true}
	state := &reactState{exposedTools: map[string]bool{}}
	clientGroups := map[string]map[string]bool{
		"browser": {"navigate": true, "click": true},
	}
	fn := o.makeExposeFn(state, clientGroups)

	res := fn([]tool.ExposeSpec{
		{Tool: "browser", Functions: []string{"navigate", "bogus"}},
	})
	if len(res.Enabled) != 1 || res.Enabled[0] != "browser.navigate" {
		t.Errorf("Enabled: want [browser.navigate], got %v", res.Enabled)
	}
	if !state.exposedTools["browser.navigate"] || state.exposedTools["browser.bogus"] {
		t.Error("browser.navigate should be exposed, browser.bogus must not")
	}
	found := false
	for _, r := range res.Rejected {
		if r == "browser.bogus" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected browser.bogus rejected, got %v", res.Rejected)
	}
	found = false
	for _, a := range res.Available {
		if a == "browser" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected browser in Available, got %v", res.Available)
	}
}

func TestExposure_MakeExposeFn_ClientToolsExcluded(t *testing.T) {
	reg := tool.NewRegistry()
	o := &Orchestrator{registry: reg, toolExcludes: []string{"browser"}, useNativeFC: true}
	state := &reactState{exposedTools: map[string]bool{}}
	clientGroups := map[string]map[string]bool{"browser": {"navigate": true}}
	fn := o.makeExposeFn(state, clientGroups)

	res := fn([]tool.ExposeSpec{{Tool: "browser", Functions: []string{"navigate"}}})
	if len(res.Enabled) != 0 {
		t.Errorf("excluded client group must not be enabled, got %v", res.Enabled)
	}
	if state.exposedTools["browser.navigate"] {
		t.Error("excluded client group must not be exposed")
	}
	rejected := false
	for _, r := range res.Rejected {
		if r == "browser" {
			rejected = true
		}
	}
	if !rejected {
		t.Errorf("expected browser rejected, got %v", res.Rejected)
	}
}

func TestExposure_MkLlmChatReq_ClientToolsExposed(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(&mockExposeTool{}) // note: record_note
	o := &Orchestrator{
		contextFormatter: &mockFormatter{},
		registry:         reg,
		useNativeFC:      true,
	}
	clientTools := []ClientTool{
		{ServerName: "browser", Name: "navigate", Description: "nav"},
		{ServerName: "browser", Name: "click", Description: "click"},
	}
	req := &RunRequest{UserID: "u1", ClientTools: clientTools}

	// 未暴露：无客户端 def
	cr := o.mkLlmChatReq(req, "sys", &reactState{msgs: []llm.Message{}})
	for _, d := range cr.Tools {
		if d.Name == "browser" {
			t.Error("browser must not be injected before exposure")
		}
	}

	// 只暴露 navigate：browser def 只含 navigate
	state := &reactState{msgs: []llm.Message{}, exposedTools: map[string]bool{"browser.navigate": true}}
	cr = o.mkLlmChatReq(req, "sys", state)
	var browser *llm.ToolDefinition
	for i := range cr.Tools {
		if cr.Tools[i].Name == "browser" {
			browser = &cr.Tools[i]
		}
	}
	if browser == nil {
		t.Fatalf("browser def should be injected after exposure, tools: %v", toolNames(cr.Tools))
	}
	if len(browser.Functions) != 1 || browser.Functions[0].Name != "navigate" {
		t.Errorf("browser should expose only navigate, got %+v", browser.Functions)
	}
}

func toolNames(defs []llm.ToolDefinition) []string {
	out := make([]string, len(defs))
	for i, d := range defs {
		out[i] = d.Name
	}
	return out
}
