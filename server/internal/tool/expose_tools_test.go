package tool

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestExposeToolsTool_Enable(t *testing.T) {
	tt := NewExposeToolsTool()
	if tt.Name() != "expose_tools" {
		t.Errorf("Name: want expose_tools, got %s", tt.Name())
	}
	if len(tt.Functions()) != 1 || tt.Functions()[0].Name != "enable" {
		t.Fatalf("Functions: want single 'enable', got %+v", tt.Functions())
	}

	var called []string
	ec := &ExecuteContext{
		Expose: func(groups []string) *ExposeResult {
			called = groups
			return &ExposeResult{Enabled: groups, Available: []string{"note", "bookkeeping"}}
		},
	}
	res := tt.Execute(ec, "enable", json.RawMessage(`{"groups":["note","bookkeeping"]}`))
	if !res.Success {
		t.Fatalf("expected success, got %+v", res)
	}
	if len(called) != 2 || called[0] != "note" {
		t.Errorf("callback groups: want [note bookkeeping], got %v", called)
	}
	var out ExposeResult
	if err := json.Unmarshal(res.Data, &out); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(out.Enabled) != 2 {
		t.Errorf("Enabled: want 2, got %v", out.Enabled)
	}
}

func TestExposeToolsTool_NilCallback(t *testing.T) {
	tt := NewExposeToolsTool()
	ec := &ExecuteContext{} // Expose nil
	res := tt.Execute(ec, "enable", json.RawMessage(`{"groups":["note"]}`))
	if res.Success {
		t.Error("expected failure when Expose callback is nil")
	}
}

func TestExposeToolsTool_BadArgs(t *testing.T) {
	tt := NewExposeToolsTool()
	ec := &ExecuteContext{Expose: func([]string) *ExposeResult { return &ExposeResult{} }}
	if res := tt.Execute(ec, "enable", json.RawMessage(`{}`)); res.Success {
		t.Error("expected failure on empty groups")
	}
	if res := tt.Execute(ec, "enable", json.RawMessage(`not-json`)); res.Success {
		t.Error("expected failure on invalid JSON")
	}
	if res := tt.Execute(ec, "nope", json.RawMessage(`{}`)); res.Success {
		t.Error("expected failure on unknown function")
	}
}

func TestToolCatalog(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&mockOrderTool{name: "bookkeeping", desc: "记账工具", fns: []FuncSchema{{Name: "create_bill"}, {Name: "query_bills"}}})
	reg.Register(&mockOrderTool{name: "note", desc: "笔记工具", fns: []FuncSchema{{Name: "record_note"}}})
	reg.Register(&mockOrderTool{name: "webfetch", desc: "隐藏工具", fns: []FuncSchema{{Name: "fetch"}}})
	reg.HideTool("webfetch")

	catalog := reg.ToolCatalog()
	if catalog == "" {
		t.Fatal("catalog should not be empty")
	}
	if strings.Contains(catalog, "webfetch") {
		t.Error("hidden tool should not appear in catalog")
	}
	if !strings.Contains(catalog, "create_bill") {
		t.Error("catalog should include function name list (create_bill)")
	}
	if !strings.Contains(catalog, "record_note") {
		t.Error("catalog should include function name list (record_note)")
	}
	// 字节稳定：两次调用一致
	if a, b := reg.ToolCatalog(), reg.ToolCatalog(); a != b {
		t.Error("catalog must be byte-stable across calls")
	}
}
