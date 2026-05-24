package controller

import (
	"encoding/json"
	"testing"

	"xhan-helper/internal/model"
)

func TestExtractFlashReply(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{"json only", `{"reply":"hello","tool_calls":[]}`, "hello"},
		{"with prefix text", "ok let me do that\n\n{\"reply\":\"doing it\"}", "ok let me do that\n\ndoing it"},
		{"markdown code block", "```json\n{\"reply\":\"hi\"}\n```", "hi"},
		{"pure text no json", "just chatting", ""},
		{"json without reply field", `{"tool_calls":[{"tool":"x"}]}`, ""},
		{"json after text with reply", "ok\n{\n    \"reply\": \"test\"\n}\nbye", "ok\n\ntest"},
		{"chinese text", "好的，马上删除这笔。\n\n{\"reply\": \"正在删除...\"}", "好的，马上删除这笔。\n\n正在删除..."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractFlashReply(tt.content)
			if got != tt.want {
				t.Errorf("extractFlashReply(%q) = %q, want %q", tt.content, got, tt.want)
			}
		})
	}
}

func ptrStr(s string) *string { return &s }

func TestMergeConsecutiveAssistant_SingleMsg(t *testing.T) {
	msgs := []model.Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello"},
	}
	result := mergeConsecutiveAssistant(msgs)
	if len(result) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(result))
	}
}

func TestMergeConsecutiveAssistant_MergesTwo(t *testing.T) {
	tc1 := `[{"tool":"datetime","function":"now","args":{}}]`
	tc2 := `[{"tool":"note","function":"record_note","args":{"topic":"t"}}]`
	msgs := []model.Message{
		{Role: "user", Content: "帮我做事"},
		{Role: "assistant", Content: "好的，我来查", ToolCallsJSON: ptrStr(tc1)},
		{Role: "assistant", Content: "已记录", ToolCallsJSON: ptrStr(tc2)},
	}
	result := mergeConsecutiveAssistant(msgs)
	if len(result) != 2 {
		t.Fatalf("expected 2 messages (user + merged assistant), got %d", len(result))
	}
	merged := result[1]
	if merged.Content != "已记录" {
		t.Errorf("expected content='已记录' (last msg), got '%s'", merged.Content)
	}
	if merged.ToolCallsJSON == nil {
		t.Fatal("expected ToolCallsJSON to be non-nil")
	}
	// 应包含两个 tool_call
	var tcs []map[string]interface{}
	if err := json.Unmarshal([]byte(*merged.ToolCallsJSON), &tcs); err != nil {
		t.Fatalf("ToolCallsJSON should be valid JSON array: %v", err)
	}
	if len(tcs) != 2 {
		t.Errorf("expected 2 tool calls merged, got %d", len(tcs))
	}
}

func TestMergeConsecutiveAssistant_OnlyMergesConsecutive(t *testing.T) {
	msgs := []model.Message{
		{Role: "assistant", Content: "msg1"},
		{Role: "user", Content: "interrupt"},
		{Role: "assistant", Content: "msg2"},
	}
	result := mergeConsecutiveAssistant(msgs)
	if len(result) != 3 {
		t.Fatalf("expected 3 messages (user breaks merge), got %d", len(result))
	}
}

func TestMergeConsecutiveAssistant_SkipsEmptyToolCalls(t *testing.T) {
	empty := ""
	msgs := []model.Message{
		{Role: "assistant", Content: "first", ToolCallsJSON: &empty},
		{Role: "assistant", Content: "second"},
	}
	result := mergeConsecutiveAssistant(msgs)
	if len(result) != 1 {
		t.Fatalf("expected 1 merged message, got %d", len(result))
	}
	merged := result[0]
	if merged.ToolCallsJSON != nil && *merged.ToolCallsJSON != "" {
		t.Errorf("expected nil/empty ToolCallsJSON, got '%s'", *merged.ToolCallsJSON)
	}
}

func TestMergeConsecutiveAssistant_KeepsLastReasoning(t *testing.T) {
	r1 := "thinking 1"
	r2 := "thinking 2"
	msgs := []model.Message{
		{Role: "assistant", Content: "a", ReasoningContent: &r1},
		{Role: "assistant", Content: "b", ReasoningContent: &r2},
	}
	result := mergeConsecutiveAssistant(msgs)
	merged := result[0]
	if merged.ReasoningContent == nil || *merged.ReasoningContent != "thinking 2" {
		t.Errorf("expected last reasoning, got %v", merged.ReasoningContent)
	}
}

func TestMergeConsecutiveAssistant_NoMergeOnSingle(t *testing.T) {
	msgs := []model.Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello"},
		{Role: "user", Content: "ok"},
		{Role: "assistant", Content: "bye"},
	}
	result := mergeConsecutiveAssistant(msgs)
	if len(result) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(result))
	}
}

func TestMergeConsecutiveAssistant_MixedJSONAndPlainText(t *testing.T) {
	// 模拟真实场景：第一轮 LLM 返回纯文本（非 JSON），被容错接受
	// 第二轮 LLM 返回 JSON 带 tool_calls。合并后 content 取最后的，tool_calls 只取 JSON 那轮的。
	tc := `[{"tool":"note","function":"record_note","args":{"topic":"t"}}]`
	msgs := []model.Message{
		{Role: "assistant", Content: "我觉得这个分析很有道理，让我展开说说..."},
		{Role: "assistant", Content: "已记录", ToolCallsJSON: ptrStr(tc)},
	}
	result := mergeConsecutiveAssistant(msgs)
	if len(result) != 1 {
		t.Fatalf("expected 1 merged message, got %d", len(result))
	}
	merged := result[0]
	if merged.Content != "已记录" {
		t.Errorf("expected content='已记录' (from last msg), got '%s'", merged.Content)
	}
	if merged.ToolCallsJSON == nil {
		t.Fatal("expected ToolCallsJSON from the JSON round")
	}
	var tcs []map[string]interface{}
	json.Unmarshal([]byte(*merged.ToolCallsJSON), &tcs)
	if len(tcs) != 1 {
		t.Errorf("expected 1 tool call, got %d", len(tcs))
	}
}

func TestMergeConsecutiveAssistant_JSONThenPlainText(t *testing.T) {
	// 模拟：第一轮 JSON 带 tool_call → 工具执行 → 第二轮纯文本总结
	// content 取第二轮纯文本，tool_calls 保留第一轮的 JSON
	tc := `[{"tool":"note","function":"record_note","args":{"topic":"t"}}]`
	msgs := []model.Message{
		{Role: "assistant", Content: "已记录", ToolCallsJSON: ptrStr(tc)},
		{Role: "assistant", Content: "还需要我做什么吗？"},
	}
	result := mergeConsecutiveAssistant(msgs)
	if len(result) != 1 {
		t.Fatalf("expected 1 merged message, got %d", len(result))
	}
	merged := result[0]
	if merged.Content != "还需要我做什么吗？" {
		t.Errorf("expected content='还需要我做什么吗？' (from last msg), got '%s'", merged.Content)
	}
	if merged.ToolCallsJSON == nil {
		t.Fatal("expected ToolCallsJSON preserved from first round")
	}
	var tcs []map[string]interface{}
	json.Unmarshal([]byte(*merged.ToolCallsJSON), &tcs)
	if len(tcs) != 1 {
		t.Errorf("expected 1 tool call, got %d", len(tcs))
	}
}

func TestMergeConsecutiveAssistant_EmptyInput(t *testing.T) {
	result := mergeConsecutiveAssistant(nil)
	if len(result) != 0 {
		t.Errorf("expected empty, got %d", len(result))
	}
}
