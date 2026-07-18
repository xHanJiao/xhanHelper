package agent

import (
	"encoding/json"
	"testing"

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
