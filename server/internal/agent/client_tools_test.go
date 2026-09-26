package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
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

func TestFilterSubAgentOnlyClientTools(t *testing.T) {
	groups := []string{"browser", "fs"}
	cs := []ClientTool{
		{ServerName: "browser", Name: "navigate"},
		{ServerName: "fs", Name: "read"},
		{ServerName: "calendar", Name: "list"},
	}
	got := filterSubAgentOnlyClientTools(cs, groups)
	if len(got) != 1 || got[0].ServerName != "calendar" {
		t.Errorf("expected only calendar kept, got %+v", got)
	}
	if out := filterSubAgentOnlyClientTools(cs, nil); len(out) != len(cs) {
		t.Errorf("nil groups should passthrough, got %d", len(out))
	}
	infos := []tool.ClientToolInfo{
		{ServerName: "browser", Name: "navigate"},
		{ServerName: "fs", Name: "read"},
		{ServerName: "calendar", Name: "list"},
	}
	gi := filterSubAgentOnlyClientToolInfos(infos, groups)
	if len(gi) != 1 || gi[0].ServerName != "calendar" {
		t.Errorf("infos filter wrong: %+v", gi)
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
	// 组内按名排序（click < navigate），断言按名字查找而非下标
	var navDesc, clickDesc string
	for _, fn := range defs[0].Functions {
		switch fn.Name {
		case "navigate":
			navDesc = fn.Description
		case "click":
			clickDesc = fn.Description
		}
	}
	if navDesc != "first" {
		t.Errorf("dedup should keep first occurrence, got %q", navDesc)
	}
	if clickDesc != "" {
		t.Errorf("click description should be empty, got %q", clickDesc)
	}
}

func TestBuildClientToolDefs_Sorted(t *testing.T) {
	// 输入乱序 → 输出按 server_name 排序、组内函数按名排序、两次调用字节一致
	reg := tool.NewRegistry()
	input := []ClientTool{
		{ServerName: "zeta", Name: "fn_b"},
		{ServerName: "alpha", Name: "fn_b"},
		{ServerName: "zeta", Name: "fn_a"},
		{ServerName: "alpha", Name: "fn_a"},
		{ServerName: "mid", Name: "fn_a"},
	}
	a := buildClientToolDefs(input, reg)
	b := buildClientToolDefs(input, reg)

	if len(a) != 3 {
		t.Fatalf("expected 3 groups, got %d", len(a))
	}
	got := []string{a[0].Name, a[1].Name, a[2].Name}
	want := []string{"alpha", "mid", "zeta"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("groups 应按 server_name 排序, got %v, want %v", got, want)
		}
	}
	// 组内函数按名排序
	if a[0].Functions[0].Name != "fn_a" || a[0].Functions[1].Name != "fn_b" {
		t.Errorf("alpha 组内函数应按名排序, got %+v", a[0].Functions)
	}

	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	if string(ja) != string(jb) {
		t.Error("buildClientToolDefs 两次调用应字节一致")
	}
}

func TestClientToolCatalog(t *testing.T) {
	reg := tool.NewRegistry()
	if got := clientToolCatalog(nil, reg); got != "" {
		t.Errorf("nil client tools should give empty catalog, got %q", got)
	}
	ct := []ClientTool{
		{ServerName: "browser", Name: "navigate", Description: "nav"},
		{ServerName: "browser", Name: "click", Description: "click"},
	}
	got := clientToolCatalog(ct, reg)
	if !strings.Contains(got, "browser") || !strings.Contains(got, "navigate") || !strings.Contains(got, "click") {
		t.Errorf("catalog should list group and function names, got %q", got)
	}
	if got2 := clientToolCatalog(ct, reg); got != got2 {
		t.Error("catalog 两次调用应字节一致")
	}
	// 与 registry 冲突的客户端组被 buildClientToolDefs 跳过，目录为空
	reg.Register(&fakeTool{name: "browser"})
	if got := clientToolCatalog(ct, reg); got != "" {
		t.Errorf("conflicting client group should not appear in catalog, got %q", got)
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

// 执行超时 ≠ 客户端断连（事件已送达、客户端可能只是慢/排队）：不置位 unavailable
func TestExecuteClientTool_TimeoutNotUnavailable(t *testing.T) {
	o := &Orchestrator{clientBridge: NewClientToolBridge(), clientToolTimeout: 50 * time.Millisecond}
	events := make(chan *llm.StreamEvent, 16)

	r, unavailable := o.executeClientTool(context.Background(), events, "u1", "c1", "t1", tool.ToolCall{Tool: "browser", Function: "navigate"}, "", "main")
	if r.Success {
		t.Error("expected failure on timeout")
	}
	if unavailable {
		t.Error("执行超时不应标记 unavailable（可重试）")
	}
	if !strings.Contains(r.Error, "超时") || !strings.Contains(r.Error, "重试") {
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

	r, unavailable := o.executeClientTool(context.Background(), events, "u1", "c1", "t1", tool.ToolCall{Tool: "browser", Function: "navigate"}, "", "main")
	if !r.Success || unavailable {
		t.Fatalf("expected success, got %+v unavailable=%v", r, unavailable)
	}
	if string(r.Data) != `"plain text"` {
		t.Errorf("plain text result should be wrapped, got %s", r.Data)
	}
}

// executeClientTool 必须透传 owner（标签归属）：子任务清理传 "subagent:<convID>"，
// 若被写成 "main" 客户端归属保护会拒绝关闭。
func TestExecuteClientTool_ForwardsOwner(t *testing.T) {
	o := &Orchestrator{clientBridge: NewClientToolBridge(), clientToolTimeout: 2 * time.Second}
	events := make(chan *llm.StreamEvent, 16)

	go func() {
		for evt := range events {
			if evt.Type != "client_tool_call" {
				continue
			}
			var p struct {
				CallID string `json:"call_id"`
				Owner  string `json:"owner"`
			}
			if err := json.Unmarshal([]byte(evt.Content), &p); err != nil {
				t.Errorf("bad payload: %v", err)
				return
			}
			if p.Owner != "subagent:sub-9" {
				t.Errorf("owner 应透传 subagent:sub-9, got %q", p.Owner)
			}
			_ = o.clientBridge.OnResult(p.CallID, "u1", ClientToolResult{Result: json.RawMessage(`{"closed":true}`)})
			return
		}
	}()

	r, unavailable := o.executeClientTool(context.Background(), events, "u1", "c1", "",
		tool.ToolCall{Tool: "browser", Function: "agent_browser_close"}, "", "subagent:sub-9")
	if !r.Success || unavailable {
		t.Fatalf("expected success, got %+v unavailable=%v", r, unavailable)
	}
}

// 执行超时不置位 unavailable（可重试）：两个调用都真实执行并各自超时
func TestExecuteConcurrently_TimeoutDoesNotSetUnavailable(t *testing.T) {
	o := &Orchestrator{clientBridge: NewClientToolBridge(), clientToolTimeout: 30 * time.Millisecond}
	events := make(chan *llm.StreamEvent, 32)
	state := &reactState{}
	calls := []tool.ToolCall{
		{Tool: "browser", Function: "navigate"},
		{Tool: "browser", Function: "click"},
	}

	results := o.executeConcurrently(context.Background(), events, "u1", "c1", "t1",
		tool.NewRegistry(), calls, map[string]bool{"browser": true}, state, "", nil, nil)

	if state.clientToolsUnavailable {
		t.Error("执行超时不应置位 clientToolsUnavailable（可重试）")
	}
	if results[0].result.Success || results[1].result.Success {
		t.Error("both client calls should fail")
	}
	if strings.Contains(results[1].result.Error, "已跳过") {
		t.Errorf("超时不快速失败，second call 应真实执行: %s", results[1].result.Error)
	}
}

func nameN(i int) string {
	return "tool_" + string(rune('a'+i%26)) + string(rune('a'+(i/26)%26))
}

// —— plan 模式客户端工具分派拦截 ——

func TestExecuteConcurrently_PlanModeBlocksClientWrite(t *testing.T) {
	// plan 模式：客户端写工具（IsReadOnly=false）在分派前被拦，不会发到客户端
	o := &Orchestrator{clientBridge: NewClientToolBridge(), clientToolTimeout: 2 * time.Second}
	events := make(chan *llm.StreamEvent, 16)
	state := &reactState{}
	calls := []tool.ToolCall{
		{Tool: "browser", Function: "agent_browser_click"}, // 写
		{Tool: "chart", Function: "render"},                // 写
	}
	clientTools := []ClientTool{
		{ServerName: "browser", Name: "agent_browser_click"}, // 默认 IsReadOnly=false
		{ServerName: "chart", Name: "render"},
	}

	results := o.executeConcurrently(context.Background(), events, "u1", "c1", "t1",
		tool.NewRegistry(), calls, map[string]bool{"browser": true, "chart": true}, state,
		model.ConvModePlan, clientToolReadOnlyIndex(clientTools), nil)

	for i, res := range results {
		if res.result.Success {
			t.Errorf("plan 模式客户端写工具 #%d 应被拦截", i)
		}
		if !strings.Contains(res.result.Error, "计划模式") {
			t.Errorf("拦截错误应说明计划模式, #%d: %s", i, res.result.Error)
		}
	}
	if state.clientToolsUnavailable {
		t.Error("拦截不应标记客户端不可用")
	}
}

func TestExecuteConcurrently_PlanModeBlocksClientReadOnly(t *testing.T) {
	// 2026-08-14 激进收紧：plan 模式客户端工具全量禁用，只读工具（agent_browser 特例）同样拦截
	o := &Orchestrator{clientBridge: NewClientToolBridge(), clientToolTimeout: 50 * time.Millisecond}
	events := make(chan *llm.StreamEvent, 16)
	state := &reactState{}
	calls := []tool.ToolCall{
		{Tool: "browser", Function: "agent_browser_open"}, // 只读（旧特例，现同样拦截）
	}
	clientTools := []ClientTool{
		{ServerName: "browser", Name: "agent_browser_open", IsReadOnly: true},
	}

	results := o.executeConcurrently(context.Background(), events, "u1", "c1", "t1",
		tool.NewRegistry(), calls, map[string]bool{"browser": true}, state,
		model.ConvModePlan, clientToolReadOnlyIndex(clientTools), nil)

	if results[0].result.Success {
		t.Fatal("plan 模式只读客户端工具应被拦截")
	}
	if !strings.Contains(results[0].result.Error, "计划模式") {
		t.Errorf("拦截错误应说明计划模式, got: %s", results[0].result.Error)
	}
	if state.clientToolsUnavailable {
		t.Error("拦截不应标记客户端不可用")
	}
}

func TestExecuteConcurrently_WorkModeAllowsClientWrite(t *testing.T) {
	o := &Orchestrator{clientBridge: NewClientToolBridge(), clientToolTimeout: 50 * time.Millisecond}
	events := make(chan *llm.StreamEvent, 16)
	state := &reactState{}
	calls := []tool.ToolCall{{Tool: "browser", Function: "agent_browser_click"}}
	clientTools := []ClientTool{{ServerName: "browser", Name: "agent_browser_click"}}

	results := o.executeConcurrently(context.Background(), events, "u1", "c1", "t1",
		tool.NewRegistry(), calls, map[string]bool{"browser": true}, state,
		model.ConvModeWork, clientToolReadOnlyIndex(clientTools), nil)

	if strings.Contains(results[0].result.Error, "计划模式") {
		t.Errorf("work 模式客户端写工具应放行, got: %s", results[0].result.Error)
	}
}

func TestClientToolReadOnlyIndex(t *testing.T) {
	idx := clientToolReadOnlyIndex([]ClientTool{
		{ServerName: "browser", Name: "open", IsReadOnly: true},
		{ServerName: "browser", Name: "click"},
	})
	if !idx["browser__open"] {
		t.Error("browser__open 应标记只读")
	}
	if idx["browser__click"] {
		t.Error("browser__click 不应标记只读")
	}
	if idx["browser__nope"] {
		t.Error("未声明的函数不应命中")
	}
	if got := clientToolReadOnlyIndex(nil); got != nil {
		t.Errorf("空输入应返回 nil, got %v", got)
	}
}

// fakeTool — 测试用最小工具实现
type fakeTool struct{ name string }

func (f *fakeTool) Name() string                   { return f.name }
func (f *fakeTool) Description() string            { return "fake" }
func (f *fakeTool) Functions() []tool.FuncSchema   { return nil }
func (f *fakeTool) ReadOnlyFuncs() map[string]bool { return nil }

func (f *fakeTool) Execute(ec *tool.ExecuteContext, funcName string, args json.RawMessage) *tool.Result {
	return &tool.Result{Success: true}
}
