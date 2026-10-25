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

	var called []ExposeSpec
	ec := &ExecuteContext{
		Expose: func(specs []ExposeSpec) *ExposeResult {
			called = specs
			enabled := make([]string, 0, len(specs))
			for _, s := range specs {
				enabled = append(enabled, s.Tool+"."+s.Functions[0])
			}
			return &ExposeResult{Enabled: enabled, Available: []string{"note", "bookkeeping"}}
		},
	}
	res := tt.Execute(ec, "enable", json.RawMessage(`{"tools":[{"name":"note","functions":["search"]},{"name":"bookkeeping","functions":["create_bill"]}]}`))
	if !res.Success {
		t.Fatalf("expected success, got %+v", res)
	}
	if len(called) != 2 || called[0].Tool != "note" || called[0].Functions[0] != "search" {
		t.Errorf("callback specs: want [note.search bookkeeping.create_bill], got %+v", called)
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
	res := tt.Execute(ec, "enable", json.RawMessage(`{"tools":[{"name":"note","functions":["search"]}]}`))
	if res.Success {
		t.Error("expected failure when Expose callback is nil")
	}
}

func TestExposeToolsTool_BadArgs(t *testing.T) {
	tt := NewExposeToolsTool()
	ec := &ExecuteContext{Expose: func([]ExposeSpec) *ExposeResult { return &ExposeResult{} }}
	if res := tt.Execute(ec, "enable", json.RawMessage(`{}`)); res.Success {
		t.Error("expected failure on empty tools")
	}
	if res := tt.Execute(ec, "enable", json.RawMessage(`{"groups":["note"]}`)); res.Success {
		t.Error("expected failure on legacy groups param")
	}
	if res := tt.Execute(ec, "enable", json.RawMessage(`{"tools":[{"name":"note"}]}`)); res.Success {
		t.Error("expected failure on missing functions")
	}
	if res := tt.Execute(ec, "enable", json.RawMessage(`{"tools":[{"name":"note","functions":[]}]}`)); res.Success {
		t.Error("expected failure on empty functions")
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
	reg.Register(&mockOrderTool{name: "profile", desc: "隐藏工具", fns: []FuncSchema{{Name: "fetch"}}})
	reg.HideTool("profile")

	catalog := reg.ToolCatalog()
	if catalog == "" {
		t.Fatal("catalog should not be empty")
	}
	if strings.Contains(catalog, "profile") {
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

// mockCatalogTool — 实现 CatalogProvider 的 mock，验证目录渲染走定制信息
type mockCatalogTool struct {
	mockOrderTool
	entry CatalogEntry
}

func (m *mockCatalogTool) Catalog() (CatalogEntry, bool) { return m.entry, true }

func TestToolCatalog_UsesProviderEntry(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&mockCatalogTool{
		mockOrderTool: mockOrderTool{name: "bookkeeping", desc: "冗长描述", fns: []FuncSchema{
			{Name: "create_bill"}, {Name: "update_bill"}, {Name: "query_bills"}, {Name: "get_category_options"},
		}},
		entry: CatalogEntry{
			ShortDesc: "记账。加/改/查/删账单。",
			Funcs:     []string{"create_bill", "update_bill", "query_bills"},
			Others:    "批量改、取消、统计、选项 等",
		},
	})

	catalog := reg.ToolCatalog()
	if strings.Contains(catalog, "冗长描述") {
		t.Error("provider 应覆盖 Description(), 不应出现冗长描述")
	}
	if !strings.Contains(catalog, "记账。加/改/查/删账单。") {
		t.Errorf("应包含 ShortDesc, got:\n%s", catalog)
	}
	if !strings.Contains(catalog, "create_bill, update_bill, query_bills, get_category_options") {
		t.Errorf("应列出全部函数名（函数级暴露需要精确名）, got:\n%s", catalog)
	}
	if !strings.Contains(catalog, "另有: 批量改、取消、统计、选项 等") {
		t.Errorf("应包含 Others 概略, got:\n%s", catalog)
	}
}

func TestValidateCatalog_PanicsOnMissingProvider(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&mockOrderTool{name: "plain", desc: "无 Provider", fns: []FuncSchema{{Name: "fn"}}})
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic when visible tool lacks CatalogProvider")
		}
	}()
	reg.ValidateCatalog()
}

func TestValidateCatalog_PanicsOnUndefinedFunc(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&mockCatalogTool{
		mockOrderTool: mockOrderTool{name: "broken", desc: "x", fns: []FuncSchema{{Name: "real_fn"}}},
		entry:         CatalogEntry{ShortDesc: "ok", Funcs: []string{"typo_fn"}},
	})
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic when Funcs references undefined function")
		}
	}()
	reg.ValidateCatalog()
}

func TestValidateCatalog_PanicsOnEmptyShortDesc(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&mockCatalogTool{
		mockOrderTool: mockOrderTool{name: "blank", desc: "x", fns: []FuncSchema{{Name: "fn"}}},
		entry:         CatalogEntry{ShortDesc: "  ", Funcs: []string{"fn"}},
	})
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic when ShortDesc empty")
		}
	}()
	reg.ValidateCatalog()
}

func TestValidateCatalog_IgnoresExposeAndHidden(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&mockOrderTool{name: "expose_tools", desc: "元工具", fns: []FuncSchema{{Name: "enable"}}})
	reg.Register(&mockOrderTool{name: "hidden_tool", desc: "隐藏", fns: []FuncSchema{{Name: "fn"}}})
	reg.HideTool("hidden_tool")
	// 不应 panic：expose_tools 与 hidden 都跳过校验
	reg.ValidateCatalog()
}

func TestValidateCatalog_AcceptsProviderTools(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&mockCatalogTool{
		mockOrderTool: mockOrderTool{name: "good", desc: "x", fns: []FuncSchema{{Name: "a"}, {Name: "b"}}},
		entry:         CatalogEntry{ShortDesc: "ok", Funcs: []string{"a", "b"}},
	})
	// 不应 panic
	reg.ValidateCatalog()
}
