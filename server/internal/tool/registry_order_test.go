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

func (m *mockOrderTool) Name() string                   { return m.name }
func (m *mockOrderTool) Description() string            { return m.desc }
func (m *mockOrderTool) Functions() []FuncSchema        { return m.fns }
func (m *mockOrderTool) ReadOnlyFuncs() map[string]bool { return nil }

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

// TestGetLLMToolsFiltered_OrderAndDeterminism 主链路契约：调用方传入已排序的名字
// （orchestrator 在组装请求前 sort.Strings），注册表必须按入参顺序输出、同输入字节一致，
// 未知名字静默跳过。这条替代了已删除的 GetLLMTools/GetNativeFCTools 排序测试。
func TestGetLLMToolsFiltered_OrderAndDeterminism(t *testing.T) {
	r := newOrderTestRegistry()
	names := []string{"alpha", "mid", "zeta"}
	a := r.GetLLMToolsFiltered(names)
	b := r.GetLLMToolsFiltered(names)
	if len(a) != 3 {
		t.Fatalf("expected 3 defs, got %d", len(a))
	}
	for i, want := range names {
		if a[i].Name != want {
			t.Fatalf("defs[%d] = %s, want %s（须与入参顺序一致）", i, a[i].Name, want)
		}
	}
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	if string(ja) != string(jb) {
		t.Error("同一入参两次调用应字节一致（前缀缓存前提）")
	}
	if got := r.GetLLMToolsFiltered([]string{"alpha", "nope"}); len(got) != 1 || got[0].Name != "alpha" {
		t.Errorf("未知名字应跳过, got %+v", got)
	}
}
