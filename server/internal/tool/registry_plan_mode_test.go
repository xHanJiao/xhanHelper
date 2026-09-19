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
	// 2026-09-15 只读化：plan 模式放行 ReadOnlyFuncs 声明的只读函数
	r := newPlanMockRegistry()
	ec := &ExecuteContext{Ctx: context.Background(), Mode: model.ConvModePlan}
	res := r.ExecuteByName(ec, "mock_mixed", "query", json.RawMessage(`{}`))
	if !res.Success {
		t.Fatalf("plan 模式应放行只读函数, got: %s", res.Error)
	}
}

func TestExecuteByName_StrictModeBlocksReadAndWrite(t *testing.T) {
	// strict 档：无任何工具，只读函数也一并拦截
	r := newPlanMockRegistry()
	ec := &ExecuteContext{Ctx: context.Background(), Mode: model.ConvModeStrict}
	res := r.ExecuteByName(ec, "mock_mixed", "query", json.RawMessage(`{}`))
	if res.Success {
		t.Fatal("strict 模式应拦截只读函数")
	}
	if !strings.Contains(res.Error, "严格计划模式") {
		t.Errorf("拦截错误应说明严格计划模式, got: %s", res.Error)
	}
	if res := r.ExecuteByName(ec, "mock_mixed", "write", json.RawMessage(`{}`)); res.Success {
		t.Error("strict 模式应拦截写函数")
	}
	if res := r.ExecuteByName(ec, "mock_write_all", "do", json.RawMessage(`{}`)); res.Success {
		t.Error("strict 模式应拦截 ReadOnlyFuncs=nil 的工具")
	}
}

func TestModeRestrictsTools(t *testing.T) {
	if model.ModeRestrictsTools(model.ConvModeWork) {
		t.Error("work 不应受限")
	}
	if !model.ModeRestrictsTools(model.ConvModePlan) || !model.ModeRestrictsTools(model.ConvModeStrict) {
		t.Error("plan/strict 应受限")
	}
}

func TestTaskTool_ReadOnlyFuncsNil(t *testing.T) {
	// task 触发子 agent，可能间接写：声明为 nil，plan 模式全拦
	if ro := NewTaskTool(nil, 0, 0).ReadOnlyFuncs(); len(ro) != 0 {
		t.Errorf("task.ReadOnlyFuncs 应为 nil/空（plan 模式全拦）, got: %v", ro)
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

func TestReadOnlyToolDefinitions_OnlyReadOnlyFuncs(t *testing.T) {
	r := newPlanMockRegistry()
	defs := r.ReadOnlyToolDefinitions()
	if len(defs) != 1 {
		t.Fatalf("只应导出含只读函数的工具, got %d", len(defs))
	}
	if defs[0].Name != "mock_mixed" {
		t.Errorf("应导出 mock_mixed, got %s", defs[0].Name)
	}
	if len(defs[0].Functions) != 1 || defs[0].Functions[0].Name != "query" {
		t.Errorf("只应含只读函数 query, got %+v", defs[0].Functions)
	}
	for _, d := range defs {
		if d.Name == "mock_write_all" {
			t.Error("ReadOnlyFuncs=nil 的工具不应导出")
		}
	}
}

func TestGenerateReadOnlyPrompt_OnlyReadOnlyFuncs(t *testing.T) {
	r := newPlanMockRegistry()
	p := r.GenerateReadOnlyPrompt()
	if !strings.Contains(p, "mock_mixed") || !strings.Contains(p, "query") {
		t.Error("只读 prompt 应含只读工具与只读函数")
	}
	if strings.Contains(p, "mock_write_all") {
		t.Error("只读 prompt 不应含 ReadOnlyFuncs=nil 的工具")
	}
	if strings.Contains(p, "- write:") {
		t.Error("只读 prompt 不应含写函数 write")
	}
}
