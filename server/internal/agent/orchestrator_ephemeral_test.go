package agent

import (
	"encoding/json"
	"testing"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/tool"
)

// ephemeralMockTool — 可指定名字 / 函数 / 只读白名单，用来同时覆盖 work 与 plan 两条工具分支。
type ephemeralMockTool struct {
	name     string
	fns      []string
	readOnly map[string]bool
}

func (m *ephemeralMockTool) Name() string                   { return m.name }
func (m *ephemeralMockTool) Description() string            { return "mock tool: " + m.name }
func (m *ephemeralMockTool) ReadOnlyFuncs() map[string]bool { return m.readOnly }
func (m *ephemeralMockTool) Functions() []tool.FuncSchema {
	out := make([]tool.FuncSchema, len(m.fns))
	for i, f := range m.fns {
		out[i] = tool.FuncSchema{Name: f}
	}
	return out
}
func (m *ephemeralMockTool) Execute(*tool.ExecuteContext, string, json.RawMessage) *tool.Result {
	return &tool.Result{Success: true}
}

func newEphemeralTestOrch() *Orchestrator {
	reg := tool.NewRegistry()
	// 两个工具都声明只读白名单：否则 plan 分支拿不到它们，过滤断言会变成空断言。
	reg.Register(&ephemeralMockTool{
		name: "record_memory", fns: []string{"record_memory"},
		readOnly: map[string]bool{"record_memory": true},
	})
	reg.Register(&ephemeralMockTool{
		name: "note", fns: []string{"record_note"},
		readOnly: map[string]bool{"record_note": true},
	})
	return &Orchestrator{registry: reg}
}

func containsToolName(defs []llm.ToolDefinition, name string) bool {
	for _, d := range defs {
		if d.Name == name {
			return true
		}
	}
	return false
}

// 无痕会话：模型不该看到 record_memory（请求体 tools、expose 的 available 都藏）。
// 走 newReActState 而不是手工塞 extraExcludes——接线断了（req.Ephemeral 不再进 state）这条测试必须挂。
func TestMkLlmChatReq_Ephemeral_HidesRecordMemory(t *testing.T) {
	o := newEphemeralTestOrch()
	ctxXML := "资源区占位"

	for _, mode := range []string{model.ConvModeWork, model.ConvModePlan} {
		req := &RunRequest{UserID: "u1", Mode: mode}
		newState := func() *reactState {
			st := newReActState(req, &model.Message{ID: "msg-1", XMLContext: &ctxXML})
			st.exposedTools = map[string]bool{"record_memory.record_memory": true, "note.record_note": true}
			return st
		}

		if !containsToolName(o.mkLlmChatReq(req, "sys", newState()).Tools, "record_memory") {
			t.Fatalf("mode=%s: 普通会话必须下发 record_memory", mode)
		}

		req.Ephemeral = true
		defs := o.mkLlmChatReq(req, "sys", newState()).Tools
		if containsToolName(defs, "record_memory") {
			t.Errorf("mode=%s: 无痕会话不该下发 record_memory", mode)
		}
		if !containsToolName(defs, "note") {
			t.Errorf("mode=%s: 其他工具不该被连坐隐藏", mode)
		}
	}
}

func TestMakeExposeFn_Ephemeral_RejectsRecordMemory(t *testing.T) {
	o := newEphemeralTestOrch()

	state := &reactState{exposedTools: map[string]bool{}, extraExcludes: ephemeralToolExcludes(&RunRequest{Ephemeral: true})}
	res := o.makeExposeFn(state, nil)([]tool.ExposeSpec{{Tool: "record_memory", Functions: []string{"record_memory"}}})
	if len(res.Enabled) != 0 {
		t.Errorf("ephemeral conv must not enable record_memory, got %v", res.Enabled)
	}
	if len(res.Rejected) != 1 || res.Rejected[0] != "record_memory" {
		t.Errorf("expected record_memory rejected, got %v", res.Rejected)
	}
	for _, name := range res.Available {
		if name == "record_memory" {
			t.Error("available 不该推荐一个永远被拒的组")
		}
	}

	// 非无痕会话行为不变
	normalState := &reactState{exposedTools: map[string]bool{}}
	res2 := o.makeExposeFn(normalState, nil)([]tool.ExposeSpec{{Tool: "record_memory", Functions: []string{"record_memory"}}})
	if len(res2.Enabled) != 1 {
		t.Errorf("normal conv should enable record_memory, got enabled=%v rejected=%v", res2.Enabled, res2.Rejected)
	}
}
