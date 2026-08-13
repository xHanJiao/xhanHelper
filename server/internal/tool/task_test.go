package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// mockSubAgentRunner — 测试用 runner，记录收到的 opts，返回预设结果
type mockSubAgentRunner struct {
	gotOpts *SubAgentRunOptions
	result  *SubAgentRunResult
}

func (m *mockSubAgentRunner) RunSubAgent(ctx context.Context, opts *SubAgentRunOptions) *SubAgentRunResult {
	m.gotOpts = opts
	return m.result
}

func newTaskTestTool(runner *mockSubAgentRunner) *TaskTool {
	return NewTaskTool(runner)
}

func runTask(t *testing.T, tool *TaskTool, ec *ExecuteContext, args map[string]interface{}) *Result {
	t.Helper()
	b, _ := json.Marshal(args)
	return tool.Execute(ec, "run", b)
}

func TestTaskTool_WrapCompleted(t *testing.T) {
	r := &mockSubAgentRunner{result: &SubAgentRunResult{Success: true, State: "completed", Text: "调研结论", ConvID: "sub-1"}}
	tool := newTaskTestTool(r)
	res := runTask(t, tool, &ExecuteContext{ConvID: "main-1"}, map[string]interface{}{
		"description": "调研LLM", "subagent_type": "explore", "prompt": "去调研",
	})

	if !res.Success {
		t.Fatalf("completed 应成功: %v", res.Error)
	}
	var m map[string]string
	if err := json.Unmarshal(res.Data, &m); err != nil {
		t.Fatal(err)
	}
	out := m["result"]
	if !strings.Contains(out, `<task id="sub-1" state="completed">`) {
		t.Errorf("包裹格式错误: %s", out)
	}
	if !strings.Contains(out, "调研结论") || !strings.Contains(out, "子任务完成") {
		t.Errorf("缺少结论或完成标记: %s", out)
	}
	if r.gotOpts == nil {
		t.Fatal("runner 应收到 opts")
	}
	if r.gotOpts.ParentConvID != "main-1" || r.gotOpts.Prompt != "去调研" || r.gotOpts.SubAgentType != "explore" {
		t.Errorf("opts 透传错误: %+v", r.gotOpts)
	}
}

func TestTaskTool_PartialResultOnTimeout(t *testing.T) {
	r := &mockSubAgentRunner{result: &SubAgentRunResult{
		Success: false, State: "timeout", Error: "子任务超时",
		Text: "工具日志摘要", ConvID: "sub-2",
	}}
	tool := newTaskTestTool(r)
	res := runTask(t, tool, &ExecuteContext{}, map[string]interface{}{
		"description": "调研", "subagent_type": "explore", "prompt": "p",
	})

	if !res.Success {
		t.Fatalf("有部分产出时应包裹返回而非硬错误: %v", res.Error)
	}
	var m map[string]string
	if err := json.Unmarshal(res.Data, &m); err != nil {
		t.Fatal(err)
	}
	out := m["result"]
	if !strings.Contains(out, `state="timeout"`) {
		t.Errorf("state 应为 timeout: %s", out)
	}
	if !strings.Contains(out, "工具日志摘要") || !strings.Contains(out, "含部分结果") {
		t.Errorf("应含部分成果: %s", out)
	}
}

func TestTaskTool_HardErrorWhenNoText(t *testing.T) {
	r := &mockSubAgentRunner{result: &SubAgentRunResult{Success: false, State: "failed", Error: "LLM API 错误"}}
	tool := newTaskTestTool(r)
	res := runTask(t, tool, &ExecuteContext{}, map[string]interface{}{
		"description": "调研", "subagent_type": "explore", "prompt": "p",
	})
	if res.Success {
		t.Fatal("无产出失败应返回硬错误")
	}
	if !strings.Contains(res.Error, "LLM API 错误") {
		t.Errorf("应透传错误: %s", res.Error)
	}
}

func TestTaskTool_StateDefaultCompleted(t *testing.T) {
	r := &mockSubAgentRunner{result: &SubAgentRunResult{Success: true, Text: "x", ConvID: "sub-3"}}
	tool := newTaskTestTool(r)
	res := runTask(t, tool, &ExecuteContext{}, map[string]interface{}{
		"description": "d", "subagent_type": "explore", "prompt": "p",
	})
	if !res.Success {
		t.Fatal("state 为空且 Success 应视为 completed")
	}
	var m map[string]string
	json.Unmarshal(res.Data, &m)
	if !strings.Contains(m["result"], `state="completed"`) {
		t.Errorf("空 state 应默认 completed: %s", m["result"])
	}
}

func TestTaskTool_ClientToolsPropagated(t *testing.T) {
	r := &mockSubAgentRunner{result: &SubAgentRunResult{Success: true, Text: "x", ConvID: "sub-4"}}
	tool := newTaskTestTool(r)
	cts := []ClientToolInfo{{ServerName: "browser", Name: "navigate", IsReadOnly: true}}
	runTask(t, tool, &ExecuteContext{ClientTools: cts}, map[string]interface{}{
		"description": "d", "subagent_type": "explore", "prompt": "p",
	})
	if len(r.gotOpts.ClientTools) != 1 || r.gotOpts.ClientTools[0].ServerName != "browser" {
		t.Errorf("ClientTools 应透传: %+v", r.gotOpts.ClientTools)
	}
}

func TestTaskTool_RunnerNil(t *testing.T) {
	tool := NewTaskTool(nil)
	res := runTask(t, tool, &ExecuteContext{}, map[string]interface{}{
		"description": "d", "subagent_type": "explore", "prompt": "p",
	})
	if res.Success || !strings.Contains(res.Error, "未启用") {
		t.Errorf("runner 未注入应报未启用: %+v", res)
	}
}

func TestTaskTool_BadArgs(t *testing.T) {
	tool := NewTaskTool(nil)
	res := tool.Execute(&ExecuteContext{}, "run", json.RawMessage(`{"bad`))
	if res.Success {
		t.Fatal("非法参数应失败")
	}
	res = tool.Execute(&ExecuteContext{}, "nope", nil)
	if res.Success {
		t.Fatal("未知函数应失败")
	}
}
