package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/tool"
)

func mkRegistryWithTool(t *testing.T, name string) *tool.Registry {
	t.Helper()
	r := tool.NewRegistry()
	r.Register(&fakeTool{name: name})
	return r
}

func TestBuildClientToolDefs_GroupByServer(t *testing.T) {
	reg := tool.NewRegistry()
	defs := buildClientToolDefs([]ClientTool{
		{ServerName: "browser", Name: "browser_navigate", Description: "nav"},
		{ServerName: "browser", Name: "browser_click", Description: "click"},
		{ServerName: "fs", Name: "read_file", Description: "read"},
	}, reg)

	if len(defs) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(defs))
	}
	if defs[0].Name != "browser" || len(defs[0].Functions) != 2 {
		t.Errorf("browser group wrong: %+v", defs[0])
	}
	if defs[1].Name != "fs" || len(defs[1].Functions) != 1 {
		t.Errorf("fs group wrong: %+v", defs[1])
	}
}

func TestBuildClientToolDefs_SkipInvalid(t *testing.T) {
	reg := mkRegistryWithTool(t, "note")
	defs := buildClientToolDefs([]ClientTool{
		{ServerName: "", Name: "x"},                       // 空 server_name
		{ServerName: "browser", Name: ""},                 // 空 name
		{ServerName: "bad__srv", Name: "x"},               // server_name 含 __
		{ServerName: "browser", Name: "bad__tool"},        // name 含 __
		{ServerName: "note", Name: "hijack"},              // 与 registry 工具冲突
		{ServerName: "browser", Name: "browser_navigate"}, // 唯一合法
	}, reg)

	if len(defs) != 1 || defs[0].Name != "browser" || len(defs[0].Functions) != 1 {
		t.Fatalf("expected only 1 valid tool, got %+v", defs)
	}
}

func TestBuildClientToolDefs_Empty(t *testing.T) {
	if defs := buildClientToolDefs(nil, tool.NewRegistry()); defs != nil {
		t.Errorf("expected nil for empty input, got %+v", defs)
	}
}

func TestBuildClientToolDefs_Truncate(t *testing.T) {
	reg := tool.NewRegistry()
	tools := make([]ClientTool, maxClientTools+10)
	for i := range tools {
		tools[i] = ClientTool{ServerName: "srv", Name: nameN(i)}
	}
	defs := buildClientToolDefs(tools, reg)
	if len(defs) != 1 {
		t.Fatalf("expected 1 group, got %d", len(defs))
	}
	if len(defs[0].Functions) != maxClientTools {
		t.Errorf("expected %d functions after truncate, got %d", maxClientTools, len(defs[0].Functions))
	}
}

func TestClientToolIndex(t *testing.T) {
	reg := tool.NewRegistry()
	idx := clientToolIndex([]ClientTool{
		{ServerName: "browser", Name: "browser_navigate"},
	}, reg)
	if !idx["browser"] {
		t.Error("expected browser in index")
	}
	if idx["note"] {
		t.Error("unexpected note in index")
	}
	if got := clientToolIndex(nil, reg); got != nil {
		t.Errorf("expected nil index for no client tools, got %v", got)
	}
}

func TestBuildClientToolDefs_DedupDuplicateName(t *testing.T) {
	reg := tool.NewRegistry()
	defs := buildClientToolDefs([]ClientTool{
		{ServerName: "browser", Name: "navigate", Description: "first"},
		{ServerName: "browser", Name: "navigate", Description: "dup"},
		{ServerName: "browser", Name: "click"},
	}, reg)
	if len(defs) != 1 || len(defs[0].Functions) != 2 {
		t.Fatalf("expected 1 group with 2 functions after dedup, got %+v", defs)
	}
	if defs[0].Functions[0].Description != "first" {
		t.Errorf("dedup should keep first occurrence, got %q", defs[0].Functions[0].Description)
	}
}

func TestNormalizeClientToolData(t *testing.T) {
	// 合法 JSON 原样透传
	valid := json.RawMessage(`{"ok":1}`)
	if got := normalizeClientToolData(valid); string(got) != `{"ok":1}` {
		t.Errorf("valid JSON should pass through, got %s", got)
	}

	// 纯文本包装为 JSON 字符串
	if got := normalizeClientToolData(json.RawMessage("plain text")); string(got) != `"plain text"` {
		t.Errorf("plain text should be wrapped, got %s", got)
	}

	// 超长内容截断且结果为合法 JSON
	huge := json.RawMessage(`{"data":"` + strings.Repeat("a", clientToolInjectBudgetBytes+5000) + `"}`)
	got := normalizeClientToolData(huge)
	if len(got) > clientToolInjectBudgetBytes+1000 {
		t.Errorf("oversize result not truncated: %d bytes", len(got))
	}
	if !json.Valid(got) {
		t.Error("truncated result must be valid JSON")
	}
	var s string
	if err := json.Unmarshal(got, &s); err != nil || !strings.Contains(s, "已截断") {
		t.Errorf("truncated result should be a JSON string with marker, err=%v", err)
	}
}

func TestExecuteClientTool_TimeoutSetsUnavailable(t *testing.T) {
	o := &Orchestrator{clientBridge: NewClientToolBridge(), clientToolTimeout: 50 * time.Millisecond}
	events := make(chan *llm.StreamEvent, 16)

	r, unavailable := o.executeClientTool(context.Background(), events, "u1", tool.ToolCall{Tool: "browser", Function: "navigate"})
	if r.Success {
		t.Error("expected failure on timeout")
	}
	if !unavailable {
		t.Error("timeout should mark client unavailable")
	}
	if !strings.Contains(r.Error, "超时") {
		t.Errorf("unexpected error: %s", r.Error)
	}
}

func TestExecuteClientTool_ResultDelivered(t *testing.T) {
	o := &Orchestrator{clientBridge: NewClientToolBridge(), clientToolTimeout: 2 * time.Second}
	events := make(chan *llm.StreamEvent, 16)

	go func() {
		for evt := range events {
			if evt.Type != "client_tool_call" {
				continue
			}
			var p struct {
				CallID string `json:"call_id"`
			}
			if err := json.Unmarshal([]byte(evt.Content), &p); err != nil {
				t.Errorf("bad client_tool_call payload: %v", err)
				return
			}
			if err := o.clientBridge.OnResult(p.CallID, "u1", ClientToolResult{Result: json.RawMessage("plain text")}); err != nil {
				t.Errorf("OnResult error: %v", err)
			}
			return
		}
	}()

	r, unavailable := o.executeClientTool(context.Background(), events, "u1", tool.ToolCall{Tool: "browser", Function: "navigate"})
	if !r.Success || unavailable {
		t.Fatalf("expected success, got %+v unavailable=%v", r, unavailable)
	}
	if string(r.Data) != `"plain text"` {
		t.Errorf("plain text result should be wrapped, got %s", r.Data)
	}
}

func TestExecuteConcurrently_SkipsClientToolsAfterUnavailable(t *testing.T) {
	o := &Orchestrator{clientBridge: NewClientToolBridge(), clientToolTimeout: 30 * time.Millisecond}
	events := make(chan *llm.StreamEvent, 32)
	state := &reactState{}
	calls := []tool.ToolCall{
		{Tool: "browser", Function: "navigate"},
		{Tool: "browser", Function: "click"},
	}

	start := time.Now()
	results := o.executeConcurrently(context.Background(), events, "u1", "c1",
		tool.NewRegistry(), calls, map[string]bool{"browser": true}, state)
	elapsed := time.Since(start)

	if !state.clientToolsUnavailable {
		t.Error("expected clientToolsUnavailable flag set after timeout")
	}
	if results[0].result.Success || results[1].result.Success {
		t.Error("both client calls should fail")
	}
	if !strings.Contains(results[1].result.Error, "已跳过") {
		t.Errorf("second call should be skipped fast, got: %s", results[1].result.Error)
	}
	if elapsed > 2*time.Second {
		t.Errorf("second call should not wait another timeout cycle, elapsed=%v", elapsed)
	}
}

func nameN(i int) string {
	return "tool_" + string(rune('a'+i%26)) + string(rune('a'+(i/26)%26))
}

// fakeTool — 测试用最小工具实现
type fakeTool struct{ name string }

func (f *fakeTool) Name() string                 { return f.name }
func (f *fakeTool) Description() string          { return "fake" }
func (f *fakeTool) Functions() []tool.FuncSchema { return nil }
func (f *fakeTool) Execute(ec *tool.ExecuteContext, funcName string, args json.RawMessage) *tool.Result {
	return &tool.Result{Success: true}
}
