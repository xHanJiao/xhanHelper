package controller

import (
	"encoding/json"
	"strings"
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

// TestCleanMessages_KeepsMarkdownWithInlineJSON 回归：正文里嵌的 JSON（内联代码、代码块、
// 示例）不能把 assistant 正文截断到第一个 '{' 之前。
func TestCleanMessages_KeepsMarkdownWithInlineJSON(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "inline empty json code span",
			content: "1. `extra` 统一名字，TEXT 存 JSON，默认 `'{}'`\n\n后面还有很长的正文。",
			want:    "1. `extra` 统一名字，TEXT 存 JSON，默认 `'{}'`\n\n后面还有很长的正文。",
		},
		{
			name:    "fenced json block with trailing prose",
			content: "配置如下：\n\n```json\n{\"a\":1}\n```\n\n以上。",
			want:    "配置如下：\n\n```json\n{\"a\":1}\n```\n\n以上。",
		},
		{
			name:    "json example without reply key",
			content: "示例：{\"foo\":\"bar\"} 结束。",
			want:    "示例：{\"foo\":\"bar\"} 结束。",
		},
		{
			name:    "real envelope with reply",
			content: `{"reply":"hello","tool_calls":[]}`,
			want:    "hello",
		},
		{
			name:    "prefix plus envelope",
			content: "ok\n\n{\"reply\":\"test\"}",
			want:    "ok\n\ntest",
		},
		{
			name:    "pure tool envelope blanked",
			content: `{"reply":"","tool_calls":[{"tool":"x"}]}`,
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msgs := []model.Message{
				{Role: "user", Content: "hi"},
				{Role: "assistant", Content: tt.content},
			}
			result := cleanMessages(msgs)
			if tt.want == "" {
				if len(result) != 1 {
					t.Fatalf("expected empty assistant to be filtered, got %d messages", len(result))
				}
				return
			}
			if len(result) != 2 {
				t.Fatalf("expected 2 messages, got %d", len(result))
			}
			if result[1].Content != tt.want {
				t.Errorf("cleanMessages content = %q, want %q", result[1].Content, tt.want)
			}
		})
	}
}

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
	if merged.Content != "好的，我来查\n\n已记录" {
		t.Errorf("expected content='好的，我来查\\n\\n已记录' (concatenated), got '%s'", merged.Content)
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
	if merged.ReasoningContent == nil || *merged.ReasoningContent != "thinking 1\n\nthinking 2" {
		t.Errorf("expected concatenated reasoning, got %v", merged.ReasoningContent)
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
	// 第二轮 LLM 返回 JSON 带 tool_calls。合并后 content 拼接所有轮，tool_calls 只取 JSON 那轮的。
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
	if merged.Content != "我觉得这个分析很有道理，让我展开说说...\n\n已记录" {
		t.Errorf("expected concatenated content, got '%s'", merged.Content)
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
	// content 拼接所有轮，tool_calls 保留第一轮的 JSON
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
	if merged.Content != "已记录\n\n还需要我做什么吗？" {
		t.Errorf("expected concatenated content, got '%s'", merged.Content)
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

// TestMergeConsecutiveAssistant_PreservesAllContent 回归测试：
// 合并时所有非空 content 都应保留，不能只取最后一条。
// 对应 bug：assistant 带 content+tool_calls 的轮次，其文本被合并吞掉。
func TestMergeConsecutiveAssistant_PreservesAllContent(t *testing.T) {
	// 模拟 2955be55 场景：第一轮 assistant 有大量用户可见文本 + tool_call，
	// 工具执行后第二轮 assistant 仅简短确认。tool 消息已被 cleanMessages 过滤，
	// 两条 assistant 变为相邻，合并时必须保留第一轮的文本。
	tc := `[{"tool":"note","function":"record_note","args":{"topic":"购物计划"}}]`
	msgs := []model.Message{
		{Role: "user", Content: "帮我记一下父亲节买笔记本的计划"},
		{Role: "assistant", Content: "好，2000-3000预算，用途就是上网看视频、WPS轻办公、QQ围棋。\n\n我先记一下这个计划。", ToolCallsJSON: ptrStr(tc)},
		{Role: "assistant", Content: "已帮你记下了"},
	}
	result := mergeConsecutiveAssistant(msgs)
	if len(result) != 2 {
		t.Fatalf("expected 2 messages (user + merged assistant), got %d", len(result))
	}
	merged := result[1]
	// 两条都有非空 content，应拼接
	want := "好，2000-3000预算，用途就是上网看视频、WPS轻办公、QQ围棋。\n\n我先记一下这个计划。\n\n已帮你记下了"
	if merged.Content != want {
		t.Errorf("expected concatenated content, got '%s'", merged.Content)
	}
	// tool_calls 应合并
	if merged.ToolCallsJSON == nil {
		t.Fatal("expected ToolCallsJSON to be preserved")
	}
}

// TestMergeConsecutiveAssistant_SkipsEmptyContent 回归测试：
// 空 content 的 assistant（纯 tool_call 包装轮）不应在拼接时产生多余换行。
func TestMergeConsecutiveAssistant_SkipsEmptyContent(t *testing.T) {
	tc1 := `[{"tool":"datetime","function":"now","args":{}}]`
	tc2 := `[{"tool":"note","function":"search","args":{"q":"test"}}]`
	msgs := []model.Message{
		{Role: "assistant", Content: "让我查一下", ToolCallsJSON: ptrStr(tc1)},
		{Role: "assistant", Content: "", ToolCallsJSON: ptrStr(tc2)}, // 纯工具调用轮，无文本
		{Role: "assistant", Content: "查到了，结果是..."},
	}
	result := mergeConsecutiveAssistant(msgs)
	if len(result) != 1 {
		t.Fatalf("expected 1 merged message, got %d", len(result))
	}
	merged := result[0]
	// 中间的 "" 应被跳过，不产生多余 \n\n
	if merged.Content != "让我查一下\n\n查到了，结果是..." {
		t.Errorf("expected content without empty gaps, got '%s'", merged.Content)
	}
}

// TestMergeConsecutiveAssistant_MiddleEmptyPreservesReasoning
// 即使中间 assistant 的 content 为空，其 reasoning 仍应被拼接。
func TestMergeConsecutiveAssistant_MiddleEmptyPreservesReasoning(t *testing.T) {
	r1 := "需要查时间"
	r2 := "需要搜索笔记"
	r3 := "结果已汇总"
	msgs := []model.Message{
		{Role: "assistant", Content: "开始处理", ReasoningContent: &r1},
		{Role: "assistant", Content: "", ReasoningContent: &r2}, // 纯工具调用轮
		{Role: "assistant", Content: "处理完成", ReasoningContent: &r3},
	}
	result := mergeConsecutiveAssistant(msgs)
	merged := result[0]
	if merged.ReasoningContent == nil {
		t.Fatal("expected ReasoningContent to be non-nil")
	}
	want := "需要查时间\n\n需要搜索笔记\n\n结果已汇总"
	if *merged.ReasoningContent != want {
		t.Errorf("expected concatenated reasoning, got '%s'", *merged.ReasoningContent)
	}
}

// —— injectToolResult ——

func TestInjectToolResult_Basic(t *testing.T) {
	tcJSON := `[{"tool":"note","function":"search","args":{"q":"x"}}]`
	msg := &model.Message{ToolCallsJSON: ptrStr(tcJSON)}
	resultJSON := `{"success":true,"data":{"notes":["a","b"]}}`

	injectToolResult(msg, resultJSON)

	var tcs []map[string]interface{}
	if err := json.Unmarshal([]byte(*msg.ToolCallsJSON), &tcs); err != nil {
		t.Fatal(err)
	}
	if len(tcs) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(tcs))
	}
	if tcs[0]["result"] == nil {
		t.Fatal("expected result to be injected")
	}
	result := tcs[0]["result"].(map[string]interface{})
	if result["success"] != true {
		t.Errorf("expected result.success=true, got %v", result["success"])
	}
}

func TestInjectToolResult_MultipleSequential(t *testing.T) {
	tcJSON := `[{"tool":"a","function":"f1","args":{}},{"tool":"b","function":"f2","args":{}},{"tool":"c","function":"f3","args":{}}]`
	msg := &model.Message{ToolCallsJSON: ptrStr(tcJSON)}

	injectToolResult(msg, `{"success":true,"data":"r1"}`)
	injectToolResult(msg, `{"success":true,"data":"r2"}`)
	injectToolResult(msg, `{"success":false,"error":"fail"}`)

	var tcs []map[string]interface{}
	if err := json.Unmarshal([]byte(*msg.ToolCallsJSON), &tcs); err != nil {
		t.Fatal(err)
	}
	if len(tcs) != 3 {
		t.Fatalf("expected 3 tool calls, got %d", len(tcs))
	}
	if tcs[0]["result"].(map[string]interface{})["data"] != "r1" {
		t.Errorf("tc[0] result mismatch")
	}
	if tcs[1]["result"].(map[string]interface{})["data"] != "r2" {
		t.Errorf("tc[1] result mismatch")
	}
	if tcs[2]["result"].(map[string]interface{})["success"] != false {
		t.Errorf("tc[2] result mismatch")
	}
}

func TestInjectToolResult_NoEmptySlot(t *testing.T) {
	// 所有 tool call 都已有 result — 不应崩溃，也不应增删 tool call
	tcJSON := `[{"tool":"a","function":"f1","args":{},"result":"done"}]`
	msg := &model.Message{ToolCallsJSON: ptrStr(tcJSON)}

	injectToolResult(msg, `{"extra":"should-be-ignored"}`)

	var tcs []map[string]interface{}
	if err := json.Unmarshal([]byte(*msg.ToolCallsJSON), &tcs); err != nil {
		t.Fatal(err)
	}
	if len(tcs) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(tcs))
	}
	// 原有 result 不应被覆盖
	if tcs[0]["result"] != "done" {
		t.Errorf("expected original result 'done', got %v", tcs[0]["result"])
	}
	// 不应有额外字段被注入
	if _, ok := tcs[0]["extra"]; ok {
		t.Errorf("unexpected extra field injected")
	}
}

func TestInjectToolResult_InvalidJSON(t *testing.T) {
	msg := &model.Message{ToolCallsJSON: ptrStr(`not-json`)}
	before := *msg.ToolCallsJSON
	// 不应 panic
	injectToolResult(msg, `{"x":1}`)
	if *msg.ToolCallsJSON != before {
		t.Errorf("expected no change on invalid tool_calls_json")
	}
}

func TestInjectToolResult_PlainStringResult(t *testing.T) {
	tcJSON := `[{"tool":"x","function":"y","args":{}}]`
	msg := &model.Message{ToolCallsJSON: ptrStr(tcJSON)}

	// resultJSON 不是合法 JSON 时退化为字符串
	injectToolResult(msg, `plain text result`)

	var tcs []map[string]interface{}
	json.Unmarshal([]byte(*msg.ToolCallsJSON), &tcs)
	if tcs[0]["result"] != "plain text result" {
		t.Errorf("expected plain string result, got %v", tcs[0]["result"])
	}
}

// —— cleanMessages with tool results ——

func TestCleanMessages_InjectsSingleToolResult(t *testing.T) {
	tcJSON := `[{"tool":"note","function":"search","args":{}}]`
	resultJSON := `{"success":true,"data":[{"id":"1","content":"hi"}]}`

	msgs := []model.Message{
		{Role: "user", Content: "查笔记"},
		{Role: "assistant", Content: `{"reply":"","tool_calls":[{"tool":"note","function":"search","args":{}}]}`, ToolCallsJSON: ptrStr(tcJSON)},
		{Role: "tool", Content: resultJSON, IsInternal: true},
		{Role: "assistant", Content: `{"reply":"找到了你的笔记","tool_calls":[]}`},
	}

	result := cleanMessages(msgs)

	// 应合并为一个 assistant 消息
	if len(result) != 2 {
		t.Fatalf("expected 2 messages (user + merged assistant), got %d", len(result))
	}
	assistant := result[1]
	if assistant.Role != "assistant" {
		t.Fatalf("expected assistant role, got %s", assistant.Role)
	}
	if assistant.ToolCallsJSON == nil {
		t.Fatal("expected ToolCallsJSON")
	}
	var tcs []map[string]interface{}
	json.Unmarshal([]byte(*assistant.ToolCallsJSON), &tcs)
	if len(tcs) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(tcs))
	}
	if tcs[0]["result"] == nil {
		t.Fatal("expected result to be injected into tool call")
	}
	r := tcs[0]["result"].(map[string]interface{})
	if r["success"] != true {
		t.Errorf("expected result.success=true, got %v", r["success"])
	}
	// content 应来自最后一条 assistant
	if assistant.Content != "找到了你的笔记" {
		t.Errorf("expected content from last assistant, got '%s'", assistant.Content)
	}
}

func TestCleanMessages_InjectsMultipleToolResults(t *testing.T) {
	tcJSON := `[{"tool":"a","function":"f1","args":{}},{"tool":"b","function":"f2","args":{}}]`

	msgs := []model.Message{
		{Role: "user", Content: "做两件事"},
		{Role: "assistant", Content: `{"reply":"","tool_calls":[...]}`, ToolCallsJSON: ptrStr(tcJSON)},
		{Role: "tool", Content: `{"success":true,"data":"r1"}`, IsInternal: true},
		{Role: "tool", Content: `{"success":true,"data":"r2"}`, IsInternal: true},
		{Role: "assistant", Content: `{"reply":"两件事都完成了","tool_calls":[]}`},
	}

	result := cleanMessages(msgs)
	if len(result) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(result))
	}

	var tcs []map[string]interface{}
	json.Unmarshal([]byte(*result[1].ToolCallsJSON), &tcs)
	if len(tcs) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(tcs))
	}
	if tcs[0]["result"].(map[string]interface{})["data"] != "r1" {
		t.Errorf("tc[0] result mismatch")
	}
	if tcs[1]["result"].(map[string]interface{})["data"] != "r2" {
		t.Errorf("tc[1] result mismatch")
	}
}

func TestCleanMessages_MultiRoundReActWithToolResults(t *testing.T) {
	tc1 := `[{"tool":"datetime","function":"now","args":{}}]`
	tc2 := `[{"tool":"note","function":"create","args":{"topic":"t"}}]`

	msgs := []model.Message{
		{Role: "user", Content: "现在几点？记个笔记"},
		// round 1: 查时间
		{Role: "assistant", Content: `{"reply":"","tool_calls":[{"tool":"datetime","function":"now","args":{}}]}`, ToolCallsJSON: ptrStr(tc1)},
		{Role: "tool", Content: `{"success":true,"data":"2024-01-15 10:30"}`, IsInternal: true},
		// round 2: 记笔记
		{Role: "assistant", Content: `{"reply":"","tool_calls":[{"tool":"note","function":"create","args":{"topic":"t"}}]}`, ToolCallsJSON: ptrStr(tc2)},
		{Role: "tool", Content: `{"success":true,"data":"note created"}`, IsInternal: true},
		// round 3: 文本回复
		{Role: "assistant", Content: `{"reply":"现在是10:30，笔记已记录","tool_calls":[]}`},
	}

	result := cleanMessages(msgs)
	if len(result) != 2 {
		t.Fatalf("expected 2 messages (user + merged assistant), got %d", len(result))
	}

	merged := result[1]
	if merged.Content != "现在是10:30，笔记已记录" {
		t.Errorf("unexpected content: '%s'", merged.Content)
	}

	var tcs []map[string]interface{}
	json.Unmarshal([]byte(*merged.ToolCallsJSON), &tcs)
	if len(tcs) != 2 {
		t.Fatalf("expected 2 tool calls merged, got %d", len(tcs))
	}
	// round 1 tool
	if tcs[0]["tool"] != "datetime" {
		t.Errorf("tc[0] tool mismatch: %v", tcs[0]["tool"])
	}
	if tcs[0]["result"].(map[string]interface{})["data"] != "2024-01-15 10:30" {
		t.Errorf("tc[0] result mismatch")
	}
	// round 2 tool
	if tcs[1]["tool"] != "note" {
		t.Errorf("tc[1] tool mismatch: %v", tcs[1]["tool"])
	}
	if tcs[1]["result"].(map[string]interface{})["data"] != "note created" {
		t.Errorf("tc[1] result mismatch")
	}
}

func TestCleanMessages_NoToolMessages(t *testing.T) {
	// 无工具调用的普通对话 — 应保持原有行为
	msgs := []model.Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: `{"reply":"hello","tool_calls":[]}`},
	}
	result := cleanMessages(msgs)
	if len(result) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(result))
	}
	if result[1].Content != "hello" {
		t.Errorf("expected reply extraction, got '%s'", result[1].Content)
	}
}

func TestCleanMessages_ToolMessageBeforeAnyAssistant(t *testing.T) {
	// 异常顺序：tool 消息在 assistant 之前 — 不应 panic
	msgs := []model.Message{
		{Role: "user", Content: "hi"},
		{Role: "tool", Content: `{"orphan":true}`, IsInternal: true},
		{Role: "assistant", Content: `{"reply":"hello","tool_calls":[]}`},
	}
	// 不应 panic
	result := cleanMessages(msgs)
	if len(result) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(result))
	}
}

func TestCleanMessages_KeepNonToolInternalMessagesFiltered(t *testing.T) {
	// 非 tool 的 IsInternal 消息应仍然被过滤，不触发注入逻辑
	msgs := []model.Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: `{"reply":"hello","tool_calls":[]}`},
		{Role: "system", Content: "internal note", IsInternal: true},
	}
	result := cleanMessages(msgs)
	if len(result) != 2 {
		t.Fatalf("expected 2 messages (internal non-tool filtered), got %d", len(result))
	}
}

func TestMergeConsecutiveAssistant_NoMergeBreaksGrouping(t *testing.T) {
	extraNoMerge := `{"no_merge":true}`
	msgs := []model.Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "first"},
		{Role: "assistant", Content: "standalone", Extra: &extraNoMerge},
		{Role: "assistant", Content: "third"},
	}
	result := mergeConsecutiveAssistant(msgs)
	if len(result) != 4 {
		t.Fatalf("expected 4 (user + first + standalone + third), got %d", len(result))
	}
	if result[1].Content != "first" {
		t.Errorf("first: got %q", result[1].Content)
	}
	if result[2].Content != "standalone" {
		t.Errorf("standalone: got %q", result[2].Content)
	}
	if result[3].Content != "third" {
		t.Errorf("third: got %q", result[3].Content)
	}
}

func TestMergeConsecutiveAssistant_NoMergeAtStart(t *testing.T) {
	extraNoMerge := `{"no_merge":true}`
	msgs := []model.Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "standalone", Extra: &extraNoMerge},
		{Role: "assistant", Content: "after"},
	}
	result := mergeConsecutiveAssistant(msgs)
	if len(result) != 3 {
		t.Fatalf("expected 3, got %d", len(result))
	}
}

// 下发不按大小截断：只有 html.get(full) 的整页换 stub，其余原样。
func TestInjectToolResult_StubsOnlyHTMLFull(t *testing.T) {
	page, _ := json.Marshal(map[string]any{
		"success": true,
		"data":    map[string]any{"artifact_id": "a1", "html": strings.Repeat("<p>正文</p>", 500)},
	})
	big, _ := json.Marshal(map[string]any{
		"success": true,
		"data":    map[string]any{"items": []string{strings.Repeat("结", 20000)}},
	})

	tcs := `[{"id":"c1","function":{"name":"html__get"}},{"id":"c2","function":{"name":"message_search__search_by_range"}}]`
	msg := &model.Message{ID: "m1", ToolCallsJSON: &tcs}
	injectToolResult(msg, string(page))
	injectToolResult(msg, string(big))

	var got []map[string]any
	if err := json.Unmarshal([]byte(*msg.ToolCallsJSON), &got); err != nil {
		t.Fatalf("注入后应是合法 JSON: %v", err)
	}
	r0, _ := json.Marshal(got[0]["result"])
	if !strings.Contains(string(r0), `"html_omitted":true`) {
		t.Fatalf("html full 应换 stub，实际 %.120s", r0)
	}
	r1, _ := json.Marshal(got[1]["result"])
	if !strings.Contains(string(r1), "结结结") {
		t.Fatal("30KB 的普通结果应原样下发（下发不截断）")
	}
}
