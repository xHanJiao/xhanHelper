package tool

import (
	"encoding/json"
	"testing"
)

// mockOrderTool — 测试用最小工具实现
type mockOrderTool struct {
	name string
	desc string
	fns  []FuncSchema
}

func (m *mockOrderTool) Name() string            { return m.name }
func (m *mockOrderTool) Description() string     { return m.desc }
func (m *mockOrderTool) Functions() []FuncSchema { return m.fns }
func (m *mockOrderTool) Execute(_ *ExecuteContext, funcName string, _ json.RawMessage) *Result {
	return &Result{Success: true, Data: json.RawMessage(`"ok"`)}
}

// newOrderTestRegistry 乱序注册三个工具（zeta/alpha/mid），验证输出与注册顺序无关
func newOrderTestRegistry() *Registry {
	r := NewRegistry()
	r.Register(&mockOrderTool{name: "zeta", desc: "z 描述", fns: []FuncSchema{
		{Name: "fn_z", Description: "z 函数"},
	}})
	r.Register(&mockOrderTool{name: "alpha", desc: "a 描述", fns: []FuncSchema{
		{Name: "fn_a", Description: "a 函数"},
	}})
	r.Register(&mockOrderTool{name: "mid", desc: "m 描述", fns: []FuncSchema{
		{Name: "fn_m", Description: "m 函数"},
	}})
	return r
}

func TestGetNativeFCTools_Sorted(t *testing.T) {
	r := newOrderTestRegistry()
	defs := r.GetNativeFCTools()
	if len(defs) != 3 {
		t.Fatalf("expected 3 defs, got %d", len(defs))
	}
	if defs[0].Name != "alpha" || defs[1].Name != "mid" || defs[2].Name != "zeta" {
		t.Errorf("GetNativeFCTools 应按名字排序, got %v %v %v", defs[0].Name, defs[1].Name, defs[2].Name)
	}
}

func TestGetNativeFCTools_Deterministic(t *testing.T) {
	r := newOrderTestRegistry()
	a, _ := json.Marshal(r.GetNativeFCTools())
	b, _ := json.Marshal(r.GetNativeFCTools())
	if string(a) != string(b) {
		t.Error("GetNativeFCTools 两次调用应产生字节一致的输出")
	}
}

func TestGetNativeFCTools_RegisterOrderIndependent(t *testing.T) {
	r1 := newOrderTestRegistry()
	r2 := NewRegistry()
	// 反序注册
	r2.Register(&mockOrderTool{name: "mid", desc: "m 描述", fns: []FuncSchema{{Name: "fn_m", Description: "m 函数"}}})
	r2.Register(&mockOrderTool{name: "alpha", desc: "a 描述", fns: []FuncSchema{{Name: "fn_a", Description: "a 函数"}}})
	r2.Register(&mockOrderTool{name: "zeta", desc: "z 描述", fns: []FuncSchema{{Name: "fn_z", Description: "z 函数"}}})

	a, _ := json.Marshal(r1.GetNativeFCTools())
	b, _ := json.Marshal(r2.GetNativeFCTools())
	if string(a) != string(b) {
		t.Error("不同注册顺序应产生相同输出（缓存字节稳定性）")
	}
}

func TestGetNativeFCTools_HiddenExcluded(t *testing.T) {
	r := newOrderTestRegistry()
	r.HideTool("mid")
	defs := r.GetNativeFCTools()
	if len(defs) != 2 {
		t.Fatalf("hidden 工具应被排除, got %d", len(defs))
	}
	for _, d := range defs {
		if d.Name == "mid" {
			t.Error("hidden 工具不应出现在 GetNativeFCTools 输出中")
		}
	}
}

func TestGetLLMTools_Sorted(t *testing.T) {
	r := newOrderTestRegistry()
	defs := r.GetLLMTools()
	if len(defs) != 3 {
		t.Fatalf("expected 3 defs, got %d", len(defs))
	}
	if defs[0].Name != "alpha" || defs[1].Name != "mid" || defs[2].Name != "zeta" {
		t.Errorf("GetLLMTools 应按名字排序, got %v %v %v", defs[0].Name, defs[1].Name, defs[2].Name)
	}
}

func TestGeneratePrompt_Deterministic(t *testing.T) {
	r := newOrderTestRegistry()
	a := r.GeneratePrompt()
	b := r.GeneratePrompt()
	if a != b {
		t.Error("GeneratePrompt 两次调用应字节一致")
	}
	// 工具定义段应按名排序：alpha 先于 mid 先于 zeta
	ia := indexOf(a, "## 工具: alpha")
	im := indexOf(a, "## 工具: mid")
	iz := indexOf(a, "## 工具: zeta")
	if ia < 0 || im < 0 || iz < 0 {
		t.Fatalf("GeneratePrompt 应包含全部工具定义: %d %d %d", ia, im, iz)
	}
	if !(ia < im && im < iz) {
		t.Error("GeneratePrompt 工具段应按名排序")
	}
}

func TestGeneratePromptWithOverview_Deterministic(t *testing.T) {
	r := newOrderTestRegistry()
	a := r.GeneratePromptWithOverview()
	b := r.GeneratePromptWithOverview()
	if a != b {
		t.Error("GeneratePromptWithOverview 两次调用应字节一致")
	}
	ia := indexOf(a, "工具名: alpha")
	im := indexOf(a, "工具名: mid")
	iz := indexOf(a, "工具名: zeta")
	if ia < 0 || im < 0 || iz < 0 {
		t.Fatalf("概览应包含全部工具: %d %d %d", ia, im, iz)
	}
	if !(ia < im && im < iz) {
		t.Error("GeneratePromptWithOverview 概览应按名排序")
	}
}

func TestToolOverview_Deterministic(t *testing.T) {
	r := newOrderTestRegistry()
	a := r.ToolOverview()
	b := r.ToolOverview()
	if a != b {
		t.Error("ToolOverview 两次调用应字节一致")
	}
	ia, im, iz := indexOf(a, "alpha"), indexOf(a, "mid"), indexOf(a, "zeta")
	if !(ia < im && im < iz) {
		t.Error("ToolOverview 应按名排序")
	}
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
