package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"xhan-helper/internal/model"
)

// planMockTool — plan 拦截测试用工具：可配置只读函数集合
type planMockTool struct {
	name string
	fns  []FuncSchema
	ro   map[string]bool
}

func (m *planMockTool) Name() string                   { return m.name }
func (m *planMockTool) Description() string            { return "mock" }
func (m *planMockTool) Functions() []FuncSchema        { return m.fns }
func (m *planMockTool) ReadOnlyFuncs() map[string]bool { return m.ro }
func (m *planMockTool) Execute(_ *ExecuteContext, funcName string, _ json.RawMessage) *Result {
	return &Result{Success: true, Data: json.RawMessage(`"ok"`)}
}

// newPlanMockRegistry 构造混合工具：query 只读，write 可写；另加一个全写工具
func newPlanMockRegistry() *Registry {
	r := NewRegistry()
	r.Register(&planMockTool{
		name: "mock_mixed",
		fns: []FuncSchema{
			{Name: "query", Parameters: map[string]interface{}{"type": "object"}},
			{Name: "write", Parameters: map[string]interface{}{"type": "object"}},
		},
		ro: map[string]bool{"query": true},
	})
	r.Register(&planMockTool{
		name: "mock_write_all",
		fns:  []FuncSchema{{Name: "do", Parameters: map[string]interface{}{"type": "object"}}},
	})
	return r
}

func TestExecuteByName_PlanModeBlocksWrite(t *testing.T) {
	r := newPlanMockRegistry()
	ec := &ExecuteContext{Ctx: context.Background(), Mode: model.ConvModePlan}
	res := r.ExecuteByName(ec, "mock_mixed", "write", json.RawMessage(`{}`))
	if res.Success {
		t.Fatal("plan 模式应拦截写函数")
	}
	if !strings.Contains(res.Error, "计划模式") {
		t.Errorf("拦截错误应说明计划模式, got: %s", res.Error)
	}
	if !strings.Contains(res.Error, "mock_mixed.write") {
		t.Errorf("拦截错误应包含工具.函数名, got: %s", res.Error)
	}
}

func TestExecuteByName_PlanModeAllowsRead(t *testing.T) {
	r := newPlanMockRegistry()
	ec := &ExecuteContext{Ctx: context.Background(), Mode: model.ConvModePlan}
	res := r.ExecuteByName(ec, "mock_mixed", "query", json.RawMessage(`{}`))
	if !res.Success {
		t.Errorf("plan 模式应放行只读函数, got: %s", res.Error)
	}
}

func TestExecuteByName_PlanModeBlocksAllOfWriteOnlyTool(t *testing.T) {
	r := newPlanMockRegistry()
	ec := &ExecuteContext{Ctx: context.Background(), Mode: model.ConvModePlan}
	// ReadOnlyFuncs 返回 nil 的工具：plan 模式全拦
	res := r.ExecuteByName(ec, "mock_write_all", "do", json.RawMessage(`{}`))
	if res.Success {
		t.Fatal("ReadOnlyFuncs=nil 的工具在 plan 模式应全拦")
	}
}

func TestExecuteByName_WorkModeAllowsAll(t *testing.T) {
	r := newPlanMockRegistry()
	ec := &ExecuteContext{Ctx: context.Background(), Mode: model.ConvModeWork}
	if res := r.ExecuteByName(ec, "mock_mixed", "write", json.RawMessage(`{}`)); !res.Success {
		t.Errorf("work 模式写函数应放行: %s", res.Error)
	}
	if res := r.ExecuteByName(ec, "mock_write_all", "do", json.RawMessage(`{}`)); !res.Success {
		t.Errorf("work 模式全写工具应放行: %s", res.Error)
	}
}

func TestExecuteByName_EmptyModeMeansWork(t *testing.T) {
	// Mode 为空（旧调用方未设置）应按 work 处理，不误拦
	r := newPlanMockRegistry()
	ec := &ExecuteContext{Ctx: context.Background()}
	if res := r.ExecuteByName(ec, "mock_mixed", "write", json.RawMessage(`{}`)); !res.Success {
		t.Errorf("Mode 为空应放行（兼容旧调用方）: %s", res.Error)
	}
}

func TestValidateReadOnlyFuncs_PanicsOnUndefinedFunction(t *testing.T) {
	r := NewRegistry()
	r.Register(&planMockTool{
		name: "bad",
		fns:  []FuncSchema{{Name: "real_fn", Parameters: map[string]interface{}{"type": "object"}}},
		ro:   map[string]bool{"typo_fn": true}, // 拼错的函数名
	})
	defer func() {
		if recover() == nil {
			t.Fatal("ReadOnlyFuncs 引用不存在的函数应 panic")
		}
	}()
	r.ValidateReadOnlyFuncs()
}

func TestValidateReadOnlyFuncs_Passes(t *testing.T) {
	r := newPlanMockRegistry()
	// 不 panic 即通过
	r.ValidateReadOnlyFuncs()
}
