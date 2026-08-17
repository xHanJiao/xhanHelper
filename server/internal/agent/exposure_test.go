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
		ToolExposure: &model.ToolExposure{SessionStartMsgID: "window-1", Tools: []string{"note"}},
	}}
	o := &Orchestrator{convRepo: convRepo, useNativeFC: true}
	state := &reactState{}
	req := &RunRequest{Window: WindowResult{SessionStartMsgID: "window-1"}}
	o.loadExposure(context.Background(), req, &model.Message{ID: "msg-1"}, state)

	if !state.exposedTools["note"] {
		t.Error("expected note loaded from Extra when key matches")
	}
}

func TestExposure_LoadFromExtra_KeyMismatchResets(t *testing.T) {
	convRepo := &mockConvRepo{extra: &model.ConversationExtra{
		ToolExposure: &model.ToolExposure{SessionStartMsgID: "old-window", Tools: []string{"note"}},
	}}
	o := &Orchestrator{convRepo: convRepo, useNativeFC: true}
	state := &reactState{}
	req := &RunRequest{Window: WindowResult{SessionStartMsgID: "new-window"}}
	o.loadExposure(context.Background(), req, &model.Message{ID: "msg-2"}, state)

	if state.exposedTools["note"] {
		t.Error("expected stale window exposure to be ignored")
	}
}

func TestExposure_PersistWritesBack(t *testing.T) {
	convRepo := &mockConvRepo{extra: &model.ConversationExtra{}}
	o := &Orchestrator{convRepo: convRepo, useNativeFC: true}
	state := &reactState{exposedTools: map[string]bool{"note": true, "bookkeeping": true}}
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
}

func TestExposure_PersistPreservesOtherFields(t *testing.T) {
	// 并发写入的其它字段（used_tool_ids 等）不能被覆盖
	convRepo := &mockConvRepo{extra: &model.ConversationExtra{UsedToolIDs: []string{"tool-1"}}}
	o := &Orchestrator{convRepo: convRepo, useNativeFC: true}
	state := &reactState{exposedTools: map[string]bool{"note": true}}
	req := &RunRequest{Window: WindowResult{SessionStartMsgID: "window-1"}}
	o.persistExposure(context.Background(), req, &model.Message{ID: "msg-5"}, state)

	if len(convRepo.extra.UsedToolIDs) != 1 || convRepo.extra.UsedToolIDs[0] != "tool-1" {
		t.Errorf("used_tool_ids must be preserved, got %v", convRepo.extra.UsedToolIDs)
	}
}

func TestExposure_PersistSkipsEmpty(t *testing.T) {
	convRepo := &mockConvRepo{extra: &model.ConversationExtra{}}
	o := &Orchestrator{convRepo: convRepo, useNativeFC: true}
	state := &reactState{} // no exposed tools
	req := &RunRequest{Window: WindowResult{SessionStartMsgID: "window-1"}}
	o.persistExposure(context.Background(), req, &model.Message{ID: "msg-4"}, state)

	if convRepo.extra.ToolExposure != nil {
		t.Error("expected no write when exposure is empty")
	}
}

func TestExposure_MkLlmChatReq_OnlyExposedAndExposeTools(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(&mockExposeTool{})
	reg.Register(&namedMockTool{name: "bookkeeping", fns: []string{"create_bill"}})
	o := &Orchestrator{
		contextFormatter: &mockFormatter{},
		registry:         reg,
		useNativeFC:      true,
	}
	req := &RunRequest{UserID: "u1"}
	state := &reactState{msgs: []llm.Message{}, exposedTools: map[string]bool{"note": true}}
	cr := o.mkLlmChatReq(req, "sys", state)

	if len(cr.Tools) != 2 {
		t.Fatalf("want 2 tools (expose_tools + note), got %d: %+v", len(cr.Tools), toolNames(cr.Tools))
	}
	names := toolNames(cr.Tools)
	if names[0] != "expose_tools" || names[1] != "note" {
		t.Errorf("unexpected tool order/names: %v", names)
	}
	// note 应带完整 schema（含函数）
	for _, d := range cr.Tools {
		if d.Name == "note" && len(d.Functions) != 1 {
			t.Errorf("note schema should include functions, got %d", len(d.Functions))
		}
	}
	// 未暴露的 bookkeeping 不应出现
	if containsName(names, "bookkeeping") {
		t.Error("unexposed bookkeeping should not be in request tools")
	}
}

func TestExposure_MakeExposeFn_Validates(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(&mockExposeTool{}) // note
	reg.Register(&namedMockTool{name: "bookkeeping", fns: []string{"create_bill"}})
	reg.Register(&namedMockTool{name: "webfetch", fns: []string{"fetch"}})
	reg.HideTool("webfetch")
	o := &Orchestrator{
		registry:     reg,
		useNativeFC:  true,
		toolExcludes: []string{"bookkeeping"},
	}
	state := &reactState{exposedTools: map[string]bool{}}
	fn := o.makeExposeFn(state)

	res := fn([]string{"note", "bookkeeping", "webfetch", "expose_tools", "ghost"})
	if len(res.Enabled) != 1 || res.Enabled[0] != "note" {
		t.Errorf("Enabled: want [note], got %v", res.Enabled)
	}
	if !state.exposedTools["note"] {
		t.Error("note should be marked exposed")
	}
	if state.exposedTools["bookkeeping"] || state.exposedTools["webfetch"] {
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

func toolNames(defs []llm.ToolDefinition) []string {
	out := make([]string, len(defs))
	for i, d := range defs {
		out[i] = d.Name
	}
	return out
}

func containsName(names []string, target string) bool {
	for _, n := range names {
		if n == target {
			return true
		}
	}
	return false
}
