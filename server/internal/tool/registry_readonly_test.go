package tool

import (
	"encoding/json"
	"testing"
)

// mixedFuncTool 一半只读、一半可写：模拟 document（含 create/update/delete/reply_annotation）。
type mixedFuncTool struct{}

func (mixedFuncTool) Name() string        { return "document" }
func (mixedFuncTool) Description() string { return "文档工具" }
func (mixedFuncTool) Functions() []FuncSchema {
	return []FuncSchema{
		{Name: "get_document"},
		{Name: "list_annotations"},
		{Name: "create_document"},
		{Name: "update_document"},
		{Name: "delete_document"},
		{Name: "reply_annotation"},
	}
}
func (mixedFuncTool) ReadOnlyFuncs() map[string]bool {
	return map[string]bool{"get_document": true, "list_annotations": true}
}
func (mixedFuncTool) Execute(*ExecuteContext, string, json.RawMessage) *Result {
	return &Result{Success: true}
}

// allReadOnlyTool 全部只读：模拟 unified_search（AllReadOnly）。
type allReadOnlyTool struct{}

func (allReadOnlyTool) Name() string        { return "unified_search" }
func (allReadOnlyTool) Description() string { return "统一搜索" }
func (allReadOnlyTool) Functions() []FuncSchema {
	return []FuncSchema{{Name: "semantic_search"}, {Name: "keyword_search"}}
}
func (t allReadOnlyTool) ReadOnlyFuncs() map[string]bool { return AllReadOnly(t) }
func (allReadOnlyTool) Execute(*ExecuteContext, string, json.RawMessage) *Result {
	return &Result{Success: true}
}

// GetReadOnlyLLMToolsFiltered：函数级只读过滤 —— 写函数既不下发也不可见；
// 无只读声明的工具整体跳过；未注册名跳过；名单顺序保持。
func TestGetReadOnlyLLMToolsFiltered_StripsWriteFuncs(t *testing.T) {
	r := NewRegistry()
	r.Register(mixedFuncTool{})
	r.Register(allReadOnlyTool{})
	r.Register(&mockOrderTool{name: "writable_only", desc: "全可写", fns: []FuncSchema{{Name: "do_write"}}})

	defs := r.GetReadOnlyLLMToolsFiltered([]string{"document", "unified_search", "writable_only", "missing"})
	if len(defs) != 2 {
		t.Fatalf("应只返回 document + unified_search，got %d: %+v", len(defs), defs)
	}
	if defs[0].Name != "document" || defs[1].Name != "unified_search" {
		t.Fatalf("名单顺序应保持，got [%s %s]", defs[0].Name, defs[1].Name)
	}

	got := map[string]bool{}
	for _, f := range defs[0].Functions {
		got[f.Name] = true
	}
	if len(got) != 2 || !got["get_document"] || !got["list_annotations"] {
		t.Fatalf("document 应只留只读函数，got %v", got)
	}
	for _, w := range []string{"create_document", "update_document", "delete_document", "reply_annotation"} {
		if got[w] {
			t.Fatalf("写函数 %s 不应下发", w)
		}
	}
}
