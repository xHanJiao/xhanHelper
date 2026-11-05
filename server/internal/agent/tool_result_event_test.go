package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/tool"
)

// tool_result 事件必须带 LLM tool_call id（客户端按 id 配对），
// 但落库/回灌 LLM 的 content 必须保持纯 tool.Result —— 不能把展示字段写进上下文。
func TestCollectToolResultsNativeCarriesToolCallIDInEventOnly(t *testing.T) {
	o := &Orchestrator{}
	events := make(chan *llm.StreamEvent, 4)

	internal := []tool.ToolCall{
		{ID: "call_a", Tool: "fs", Function: "read"},
		{ID: "call_b", Tool: "web", Function: "search"},
	}
	native := []llm.ToolCall{{ID: "call_a"}, {ID: "call_b"}}
	results := []toolExecResult{
		{tc: internal[0], result: &tool.Result{Success: true, Data: json.RawMessage(`{"text":"ok"}`)}},
		{tc: internal[1], result: &tool.Result{Success: false, Error: "boom"}},
	}

	msgs := o.collectToolResultsNative(
		context.Background(), internal, native, results, events, "conv-1", "user-1", nil, false)
	close(events)

	var got []map[string]any
	for evt := range events {
		if evt.Type != "tool_result" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(evt.Content), &m); err != nil {
			t.Fatalf("tool_result 不是合法 JSON: %v (%s)", err, evt.Content)
		}
		got = append(got, m)
	}
	if len(got) != 2 {
		t.Fatalf("应收到 2 条 tool_result，got %d", len(got))
	}
	if got[0]["tool_call_id"] != "call_a" || got[1]["tool_call_id"] != "call_b" {
		t.Fatalf("tool_call_id 不对：%v / %v", got[0]["tool_call_id"], got[1]["tool_call_id"])
	}
	// 原有扁平字段必须保留（web 前端 / 旧客户端只读这些）
	if got[0]["success"] != true {
		t.Errorf("success 字段丢失：%v", got[0])
	}
	if data, ok := got[0]["data"].(map[string]any); !ok || data["text"] != "ok" {
		t.Errorf("data 字段丢失：%v", got[0]["data"])
	}

	if len(msgs) != 2 {
		t.Fatalf("应回填 2 条 tool 消息，got %d", len(msgs))
	}
	for i, m := range msgs {
		if strings.Contains(m.Content, "tool_call_id") {
			t.Errorf("落库/回灌内容被污染（%d）：%s", i, m.Content)
		}
		if m.ToolCallID != native[i].ID {
			t.Errorf("消息 tool_call_id 不对（%d）：%s", i, m.ToolCallID)
		}
	}
	if msgs[0].Content != `{"success":true,"data":{"text":"ok"}}` {
		t.Errorf("msgs[0].Content 应保持纯 Result：%s", msgs[0].Content)
	}
}
