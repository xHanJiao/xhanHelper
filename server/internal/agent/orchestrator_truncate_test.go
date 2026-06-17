package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"xhan-helper/internal/idgen"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/tool"
)

func init() { idgen.Init(1) }

// ==================== truncateToolResult ====================

func TestTruncateToolResult_UnderLimit(t *testing.T) {
	content := "short content"
	result := truncateToolResult(content, 2000)
	if result != content {
		t.Errorf("expected unchanged, got '%s'", result)
	}
}

func TestTruncateToolResult_ExactlyAtLimit(t *testing.T) {
	content := strings.Repeat("x", 2000)
	result := truncateToolResult(content, 2000)
	if result != content {
		t.Errorf("expected unchanged at exact limit, got len=%d", len(result))
	}
}

func TestTruncateToolResult_OverLimit(t *testing.T) {
	content := strings.Repeat("abcdefghij", 500) // 5000 chars
	result := truncateToolResult(content, 2000)

	if len(result) >= len(content) {
		t.Error("expected truncation for 5000-char content with max 2000")
	}
	if !strings.Contains(result, "已截断") {
		t.Error("expected truncation notice in result")
	}
	if !strings.HasPrefix(result, content[:10]) {
		t.Error("expected head preserved")
	}
	if !strings.HasSuffix(result, content[len(content)-10:]) {
		t.Error("expected tail preserved")
	}
}

func TestTruncateToolResult_MaxLenZero(t *testing.T) {
	content := strings.Repeat("x", 5000)
	result := truncateToolResult(content, 0)
	if result != content {
		t.Errorf("expected unchanged when maxLen=0, got len=%d", len(result))
	}
}

func TestTruncateToolResult_MaxLenNegative(t *testing.T) {
	content := strings.Repeat("x", 5000)
	result := truncateToolResult(content, -1)
	if result != content {
		t.Errorf("expected unchanged when maxLen=-1, got len=%d", len(result))
	}
}

func TestTruncateToolResult_ChineseChars(t *testing.T) {
	content := "你好世界" + strings.Repeat("测试文本内容", 500) + "结尾内容"
	result := truncateToolResult(content, 2000)

	if !strings.Contains(result, "已截断") {
		t.Error("expected truncation notice for Chinese content")
	}
	if !strings.Contains(result, "你好世界") {
		t.Error("expected head with Chinese chars preserved")
	}
	if !strings.Contains(result, "结尾内容") {
		t.Error("expected tail with Chinese chars preserved")
	}
}

func TestTruncateToolResult_SlightlyOverLimit(t *testing.T) {
	// head=1400, tail=400, so 2001 chars → truncated=2001-1400-400=201
	content := strings.Repeat("a", 2001)
	result := truncateToolResult(content, 2000)

	if !strings.Contains(result, "已截断 201 字符") {
		t.Errorf("expected '已截断 201 字符', got '%s'", result)
	}
}

// ==================== BuildMessagesForFlash ====================

func TestBuildMessagesForFlash_Empty(t *testing.T) {
	msgs := BuildMessagesForFlash(nil, 2000, false)
	if len(msgs) != 0 {
		t.Errorf("expected 0 msgs, got %d", len(msgs))
	}
}

func TestBuildMessagesForFlash_NonToolMessages(t *testing.T) {
	history := []model.Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi there"},
	}
	msgs := BuildMessagesForFlash(history, 2000, false)

	if len(msgs) != 2 {
		t.Fatalf("expected 2 msgs, got %d", len(msgs))
	}
	if msgs[0].Role != "user" || msgs[0].Content != "hello" {
		t.Errorf("user msg not preserved: role=%s content=%s", msgs[0].Role, msgs[0].Content)
	}
	if msgs[1].Role != "assistant" || msgs[1].Content != "hi there" {
		t.Errorf("assistant msg not preserved: role=%s content=%s", msgs[1].Role, msgs[1].Content)
	}
}

func TestBuildMessagesForFlash_ToolMsgUnderLimit(t *testing.T) {
	history := []model.Message{
		{Role: "tool", Content: `{"success":true,"data":"ok"}`, ToolCallID: strPtr("tc1")},
	}
	msgs := BuildMessagesForFlash(history, 2000, false)

	if len(msgs) != 1 {
		t.Fatalf("expected 1 msg, got %d", len(msgs))
	}
	if msgs[0].Role != "user" {
		t.Errorf("expected tool msg wrapped as user, got role=%s", msgs[0].Role)
	}
	if msgs[0].Content != `{"success":true,"data":"ok"}` {
		t.Errorf("expected content preserved, got '%s'", msgs[0].Content)
	}
}

func TestBuildMessagesForFlash_ToolMsgOverLimit(t *testing.T) {
	longData := strings.Repeat("x", 3000)
	content := `{"success":true,"data":"` + longData + `"}`
	history := []model.Message{
		{Role: "tool", Content: content},
	}
	msgs := BuildMessagesForFlash(history, 2000, false)

	if len(msgs) != 1 {
		t.Fatalf("expected 1 msg, got %d", len(msgs))
	}
	if msgs[0].Role != "user" {
		t.Errorf("expected tool msg wrapped as user, got role=%s", msgs[0].Role)
	}
	if !strings.Contains(msgs[0].Content, "已截断") {
		t.Error("expected truncation notice in tool result")
	}
	if len(msgs[0].Content) >= len(content) {
		t.Error("expected truncated content to be shorter than original")
	}
}

func TestBuildMessagesForFlash_ToolMsgMaxLenZero(t *testing.T) {
	longData := strings.Repeat("x", 3000)
	content := `{"success":true,"data":"` + longData + `"}`
	history := []model.Message{
		{Role: "tool", Content: content},
	}
	msgs := BuildMessagesForFlash(history, 0, false)

	if len(msgs) != 1 {
		t.Fatalf("expected 1 msg, got %d", len(msgs))
	}
	if msgs[0].Content != content {
		t.Error("expected no truncation when maxLen=0")
	}
}

func TestBuildMessagesForFlash_MixedMessages(t *testing.T) {
	longData := strings.Repeat("x", 3000)
	history := []model.Message{
		{Role: "user", Content: "query"},
		{Role: "assistant", Content: "thinking", ToolCallsJSON: strPtr(`[{"id":"t1","function":{"name":"write"}}]`)},
		{Role: "tool", Content: `{"success":true,"data":"` + longData + `"}`},
		{Role: "assistant", Content: "done"},
	}
	msgs := BuildMessagesForFlash(history, 2000, false)

	if len(msgs) != 4 {
		t.Fatalf("expected 4 msgs, got %d", len(msgs))
	}
	if len(msgs[1].ToolCalls) != 0 {
		t.Errorf("expected 0 tool calls when useNativeFC=false, got %d", len(msgs[1].ToolCalls))
	}
	if !strings.Contains(msgs[2].Content, "已截断") {
		t.Error("expected tool result truncated")
	}
	if msgs[3].Content != "done" {
		t.Errorf("expected 'done', got '%s'", msgs[3].Content)
	}
}

func TestBuildMessagesForFlash_AssistantWithToolCallID(t *testing.T) {
	history := []model.Message{
		{Role: "assistant", Content: "calling tool", ToolCallID: strPtr("tc-123")},
	}
	msgs := BuildMessagesForFlash(history, 2000, false)

	if msgs[0].ToolCallID != "tc-123" {
		t.Errorf("expected ToolCallID='tc-123', got '%s'", msgs[0].ToolCallID)
	}
}

// ==================== BuildMessagesForFlash with useNativeFC ====================

func TestBuildMessagesForFlash_NativeFC_AllNewFormat(t *testing.T) {
	// 全部新格式：role=tool + ToolCallID → 应保留 role=tool 和 ToolCallID
	history := []model.Message{
		{Role: "user", Content: "查天气"},
		{Role: "assistant", Content: "帮你查一下", ToolCallsJSON: strPtr(`[{"id":"call_01","function":{"name":"get_weather","arguments":"{}"}}]`)},
		{Role: "tool", Content: `{"temp":25}`, ToolCallID: strPtr("call_01")},
		{Role: "assistant", Content: "今天25度"},
	}
	msgs := BuildMessagesForFlash(history, 2000, true)

	if len(msgs) != 4 {
		t.Fatalf("expected 4 msgs, got %d", len(msgs))
	}
	// assistant 应解析出原生 tool_calls
	if len(msgs[1].ToolCalls) != 1 {
		t.Errorf("expected 1 native tool_call, got %d", len(msgs[1].ToolCalls))
	}
	if msgs[1].ToolCalls[0].ID != "call_01" {
		t.Errorf("expected tool_call ID 'call_01', got '%s'", msgs[1].ToolCalls[0].ID)
	}
	// tool 消息应保留 role=tool 和 ToolCallID
	if msgs[2].Role != "tool" {
		t.Errorf("expected role=tool, got %s", msgs[2].Role)
	}
	if msgs[2].ToolCallID != "call_01" {
		t.Errorf("expected ToolCallID='call_01', got '%s'", msgs[2].ToolCallID)
	}
	// 最终 assistant 纯文本
	if msgs[3].Content != "今天25度" {
		t.Errorf("expected '今天25度', got '%s'", msgs[3].Content)
	}
}

func TestBuildMessagesForFlash_NativeFC_AllOldFormat(t *testing.T) {
	// 全部旧格式：tool 结果伪装成 user 消息，无 ToolCallID → 应保持原样
	history := []model.Message{
		{Role: "user", Content: "查账单"},
		{Role: "assistant", Content: `{"reply":"","tool_calls":[{"tool":"bookkeeping","function":"query_bills","args":{}}]}`},
		{Role: "user", Content: `{"success":true,"data":[{"amount":100}]}`},
		{Role: "assistant", Content: `{"reply":"查到1笔","tool_calls":[]}`},
	}
	msgs := BuildMessagesForFlash(history, 2000, true)

	if len(msgs) != 4 {
		t.Fatalf("expected 4 msgs, got %d", len(msgs))
	}
	// 旧 assistant 消息的 ToolCallsJSON 可能为空（不在 JSON 的 tool_calls 里），但 BuildMessagesForFlash 会尝试解析
	// 旧 tool 结果保持 role=user
	if msgs[2].Role != "user" {
		t.Errorf("expected old tool result kept as role=user, got %s", msgs[2].Role)
	}
	if msgs[2].ToolCallID != "" {
		t.Errorf("expected no ToolCallID for old format, got '%s'", msgs[2].ToolCallID)
	}
}

func TestBuildMessagesForFlash_NativeFC_MixedFormat(t *testing.T) {
	// 混合：有新格式 tool(tool_call_id) + 旧格式 user(无 tool_call_id)
	history := []model.Message{
		{Role: "user", Content: "第一轮"},
		// 旧格式工具调用
		{Role: "assistant", Content: `{"reply":"","tool_calls":[{"tool":"note","function":"search","args":{}}]}`},
		{Role: "user", Content: `{"success":true,"data":"old result"}`, ToolCallID: nil},
		{Role: "assistant", Content: `{"reply":"旧格式回复","tool_calls":[]}`},
		// 新格式工具调用
		{Role: "user", Content: "第二轮"},
		{Role: "assistant", Content: "查到了", ToolCallsJSON: strPtr(`[{"id":"call_02","function":{"name":"bookkeeping__query_bills","arguments":"{}"}}]`)},
		{Role: "tool", Content: `{"success":true,"data":"new result"}`, ToolCallID: strPtr("call_02")},
		{Role: "assistant", Content: "新格式回复"},
	}
	msgs := BuildMessagesForFlash(history, 2000, true)

	if len(msgs) != 8 {
		t.Fatalf("expected 8 msgs, got %d", len(msgs))
	}
	// 旧 tool 结果 → role=user（无 ToolCallID）
	if msgs[2].Role != "user" {
		t.Errorf("old result: expected role=user, got %s", msgs[2].Role)
	}
	// 新 tool 结果 → role=tool（有 ToolCallID）
	if msgs[6].Role != "tool" {
		t.Errorf("new result: expected role=tool, got %s", msgs[6].Role)
	}
	if msgs[6].ToolCallID != "call_02" {
		t.Errorf("new result: expected ToolCallID='call_02', got '%s'", msgs[6].ToolCallID)
	}
	// 新 assistant 应解析出 tool_calls
	if len(msgs[5].ToolCalls) != 1 {
		t.Errorf("expected 1 native tool_call in msg[5], got %d", len(msgs[5].ToolCalls))
	}
}

func TestBuildMessagesForFlash_NativeFC_ToolMsgNoToolCallID(t *testing.T) {
	// role=tool 但 ToolCallID=nil → 即使 useNativeFC=true 也包装成 user
	history := []model.Message{
		{Role: "tool", Content: `{"data":"x"}`, ToolCallID: nil},
	}
	msgs := BuildMessagesForFlash(history, 2000, true)

	if len(msgs) != 1 {
		t.Fatalf("expected 1 msg, got %d", len(msgs))
	}
	if msgs[0].Role != "user" {
		t.Errorf("expected tool msg without ToolCallID wrapped as user, got %s", msgs[0].Role)
	}
}

func TestBuildMessagesForFlash_NativeFC_IdgenToolCallID(t *testing.T) {
	// role=tool + ToolCallID 为 纯数字 Snowflake ID（旧 idgen 格式）→ 包装成 user
	history := []model.Message{
		{Role: "tool", Content: `{"data":"y"}`, ToolCallID: strPtr("1553947499105488896")},
	}
	msgs := BuildMessagesForFlash(history, 2000, true)

	if len(msgs) != 1 {
		t.Fatalf("expected 1 msg, got %d", len(msgs))
	}
	if msgs[0].Role != "user" {
		t.Errorf("expected idgen tool msg wrapped as user, got %s", msgs[0].Role)
	}
	if msgs[0].ToolCallID != "" {
		t.Errorf("expected no ToolCallID for idgen msg, got '%s'", msgs[0].ToolCallID)
	}
}

// ==================== collectToolResults ====================

// ==================== sanitizeOrphanToolCalls ====================

func TestSanitizeOrphanToolCalls_AllPresent(t *testing.T) {
	// 所有 tool_call 都有匹配的 tool 结果 → 不应改动
	msgs := []llm.Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{
			{ID: "call_01", Function: llm.ToolCallFunction{Name: "search", Arguments: "{}"}},
			{ID: "call_02", Function: llm.ToolCallFunction{Name: "query", Arguments: "{}"}},
		}},
		{Role: "tool", ToolCallID: "call_01", Content: "result1"},
		{Role: "tool", ToolCallID: "call_02", Content: "result2"},
		{Role: "assistant", Content: "done"},
	}
	sanitizeOrphanToolCalls(msgs)
	if len(msgs[1].ToolCalls) != 2 {
		t.Errorf("expected 2 tool_calls preserved, got %d", len(msgs[1].ToolCalls))
	}
}

func TestSanitizeOrphanToolCalls_OrphanStripped(t *testing.T) {
	// 1 条 tool 结果被删除 → 孤儿 tool_call 应被剔除
	msgs := []llm.Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{
			{ID: "call_01", Function: llm.ToolCallFunction{Name: "search", Arguments: "{}"}},
			{ID: "call_02", Function: llm.ToolCallFunction{Name: "query", Arguments: "{}"}},
		}},
		{Role: "tool", ToolCallID: "call_01", Content: "result1"},
		// call_02 的 tool 结果被手动删除了
		{Role: "assistant", Content: "done"},
	}
	sanitizeOrphanToolCalls(msgs)
	if len(msgs[1].ToolCalls) != 1 {
		t.Fatalf("expected 1 tool_call after sanitize, got %d", len(msgs[1].ToolCalls))
	}
	if msgs[1].ToolCalls[0].ID != "call_01" {
		t.Errorf("expected call_01 kept, got %s", msgs[1].ToolCalls[0].ID)
	}
}

func TestSanitizeOrphanToolCalls_AllOrphan(t *testing.T) {
	// 全部 tool 结果被删除 → tool_calls 应清空
	msgs := []llm.Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{
			{ID: "call_01", Function: llm.ToolCallFunction{Name: "search", Arguments: "{}"}},
		}},
		// 没有 tool 结果
		{Role: "user", Content: "next message"},
	}
	sanitizeOrphanToolCalls(msgs)
	if len(msgs[1].ToolCalls) != 0 {
		t.Errorf("expected 0 tool_calls after sanitize, got %d", len(msgs[1].ToolCalls))
	}
}

func TestSanitizeOrphanToolCalls_NoToolCalls(t *testing.T) {
	// 纯文本 assistant → 不应该被改动
	msgs := []llm.Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello"},
	}
	sanitizeOrphanToolCalls(msgs)
	if len(msgs[1].ToolCalls) != 0 {
		t.Errorf("expected no tool_calls, got %d", len(msgs[1].ToolCalls))
	}
}

func TestCollectToolResults_ShortResult_NotTruncated(t *testing.T) {
	o := &Orchestrator{maxToolResultChars: 2000}
	parsed := &LLMResponse{
		ToolCalls: []tool.ToolCall{
			{Tool: "test", Function: "echo", Args: json.RawMessage(`{}`)},
		},
	}
	data := json.RawMessage(`{"msg":"ok"}`)
	results := []toolExecResult{{
		tc:     parsed.ToolCalls[0],
		result: &tool.Result{Success: true, Data: data},
	}}
	events := make(chan *llm.StreamEvent, 16)

	msgs := o.collectToolResults(context.Background(), parsed, results, events, "c1", "u1", nil)

	if len(msgs) != 1 {
		t.Fatalf("expected 1 msg, got %d", len(msgs))
	}
	if msgs[0].Role != "user" {
		t.Errorf("expected role=user, got %s", msgs[0].Role)
	}
	if !strings.Contains(msgs[0].Content, `"msg":"ok"`) {
		t.Errorf("expected full content preserved, got '%s'", msgs[0].Content)
	}
}

func TestCollectToolResults_LongResult_NotTruncated(t *testing.T) {
	o := &Orchestrator{maxToolResultChars: 2000}
	parsed := &LLMResponse{
		ToolCalls: []tool.ToolCall{
			{Tool: "note", Function: "write", Args: json.RawMessage(`{}`)},
		},
	}
	longStr := strings.Repeat("abcdefghij", 500) // 5000 chars
	data, _ := json.Marshal(map[string]string{"content": longStr})
	results := []toolExecResult{{
		tc:     parsed.ToolCalls[0],
		result: &tool.Result{Success: true, Data: data},
	}}
	events := make(chan *llm.StreamEvent, 16)

	msgs := o.collectToolResults(context.Background(), parsed, results, events, "c1", "u1", nil)

	if len(msgs) != 1 {
		t.Fatalf("expected 1 msg, got %d", len(msgs))
	}
	if strings.Contains(msgs[0].Content, "已截断") {
		t.Error("tool result in ReAct loop should NOT be truncated (current-turn results need full context)")
	}
	if !strings.Contains(msgs[0].Content, longStr) {
		t.Error("expected full content preserved in current-turn tool result")
	}
}

func TestCollectToolResults_MultipleResults(t *testing.T) {
	o := &Orchestrator{maxToolResultChars: 2000}
	parsed := &LLMResponse{
		ToolCalls: []tool.ToolCall{
			{Tool: "a", Function: "f1", Args: json.RawMessage(`{}`)},
			{Tool: "b", Function: "f2", Args: json.RawMessage(`{}`)},
		},
	}
	results := []toolExecResult{
		{tc: parsed.ToolCalls[0], result: &tool.Result{Success: true, Data: json.RawMessage(`"r1"`)}},
		{tc: parsed.ToolCalls[1], result: &tool.Result{Success: true, Data: json.RawMessage(`"r2"`)}},
	}
	events := make(chan *llm.StreamEvent, 16)

	msgs := o.collectToolResults(context.Background(), parsed, results, events, "c1", "u1", nil)

	if len(msgs) != 2 {
		t.Fatalf("expected 2 msgs, got %d", len(msgs))
	}
	if !strings.Contains(msgs[0].Content, "r1") {
		t.Errorf("expected r1 in first msg, got '%s'", msgs[0].Content)
	}
	if !strings.Contains(msgs[1].Content, "r2") {
		t.Errorf("expected r2 in second msg, got '%s'", msgs[1].Content)
	}
}

func TestCollectToolResults_AppendsToExistingMsgs(t *testing.T) {
	o := &Orchestrator{maxToolResultChars: 2000}
	parsed := &LLMResponse{
		ToolCalls: []tool.ToolCall{
			{Tool: "t", Function: "f", Args: json.RawMessage(`{}`)},
		},
	}
	results := []toolExecResult{{
		tc:     parsed.ToolCalls[0],
		result: &tool.Result{Success: true, Data: json.RawMessage(`"ok"`)},
	}}
	events := make(chan *llm.StreamEvent, 16)
	existing := []llm.Message{{Role: "user", Content: "hi"}}

	msgs := o.collectToolResults(context.Background(), parsed, results, events, "c1", "u1", existing)

	if len(msgs) != 2 {
		t.Fatalf("expected 2 msgs (existing + tool result), got %d", len(msgs))
	}
	if msgs[0].Content != "hi" {
		t.Errorf("expected existing msg preserved, got '%s'", msgs[0].Content)
	}
}

func TestCollectToolResults_MarshalError(t *testing.T) {
	o := &Orchestrator{maxToolResultChars: 2000}
	parsed := &LLMResponse{
		ToolCalls: []tool.ToolCall{
			{Tool: "t", Function: "f", Args: json.RawMessage(`{}`)},
		},
	}
	results := []toolExecResult{{
		tc:     parsed.ToolCalls[0],
		result: &tool.Result{Success: false, Error: "something went wrong"},
	}}
	events := make(chan *llm.StreamEvent, 16)

	msgs := o.collectToolResults(context.Background(), parsed, results, events, "c1", "u1", nil)

	if len(msgs) != 1 {
		t.Fatalf("expected 1 msg, got %d", len(msgs))
	}
	if !strings.Contains(msgs[0].Content, "something went wrong") {
		t.Errorf("expected error in content, got '%s'", msgs[0].Content)
	}
}

func TestCollectToolResults_EventsSent(t *testing.T) {
	o := &Orchestrator{maxToolResultChars: 2000}
	parsed := &LLMResponse{
		ToolCalls: []tool.ToolCall{
			{Tool: "t", Function: "f", Args: json.RawMessage(`{}`)},
		},
	}
	results := []toolExecResult{{
		tc:     parsed.ToolCalls[0],
		result: &tool.Result{Success: true, Data: json.RawMessage(`"done"`)},
	}}
	events := make(chan *llm.StreamEvent, 16)

	go func() {
		o.collectToolResults(context.Background(), parsed, results, events, "c1", "u1", nil)
		close(events)
	}()

	var toolResults []*llm.StreamEvent
	for evt := range events {
		if evt.Type == "tool_result" {
			toolResults = append(toolResults, evt)
		}
	}
	if len(toolResults) != 1 {
		t.Fatalf("expected 1 tool_result event, got %d", len(toolResults))
	}
	if !strings.Contains(toolResults[0].Content, `"done"`) {
		t.Errorf("expected full content in event, got '%s'", toolResults[0].Content)
	}
}

func TestCollectToolResults_LongResult_EventHasFullContent(t *testing.T) {
	o := &Orchestrator{maxToolResultChars: 500}
	parsed := &LLMResponse{
		ToolCalls: []tool.ToolCall{
			{Tool: "t", Function: "f", Args: json.RawMessage(`{}`)},
		},
	}
	longStr := strings.Repeat("x", 2000)
	data, _ := json.Marshal(map[string]string{"content": longStr})
	results := []toolExecResult{{
		tc:     parsed.ToolCalls[0],
		result: &tool.Result{Success: true, Data: data},
	}}
	events := make(chan *llm.StreamEvent, 16)

	var msgs []llm.Message
	go func() {
		msgs = o.collectToolResults(context.Background(), parsed, results, events, "c1", "u1", nil)
		close(events)
	}()

	var toolResults []*llm.StreamEvent
	for evt := range events {
		if evt.Type == "tool_result" {
			toolResults = append(toolResults, evt)
		}
	}
	if len(toolResults) != 1 {
		t.Fatalf("expected 1 tool_result event, got %d", len(toolResults))
	}
	if !strings.Contains(toolResults[0].Content, longStr) {
		t.Error("event should contain full content for frontend")
	}
	if !strings.Contains(msgs[0].Content, longStr) {
		t.Error("ReAct loop msg should also contain full content (no truncation in current turn)")
	}
}

// ==================== collectToolResultsNative ====================

func TestCollectToolResultsNative_RoleIsTool(t *testing.T) {
	o := &Orchestrator{maxToolResultChars: 2000}
	internalTCs := []tool.ToolCall{
		{Tool: "bookkeeping", Function: "create_bill", Args: json.RawMessage(`{"amount":100}`)},
	}
	nativeTCs := []llm.ToolCall{
		{ID: "call_03", Function: llm.ToolCallFunction{Name: "bookkeeping__create_bill", Arguments: `{"amount":100}`}},
	}
	results := []toolExecResult{{
		tc:     internalTCs[0],
		result: &tool.Result{Success: true, Data: json.RawMessage(`{"id":1}`)},
	}}
	events := make(chan *llm.StreamEvent, 16)

	msgs := o.collectToolResultsNative(context.Background(), internalTCs, nativeTCs, results, events, "c1", "u1", nil)

	if len(msgs) != 1 {
		t.Fatalf("expected 1 msg, got %d", len(msgs))
	}
	if msgs[0].Role != "tool" {
		t.Errorf("expected role=tool, got %s", msgs[0].Role)
	}
	if msgs[0].ToolCallID != "call_03" {
		t.Errorf("expected ToolCallID='call_03', got '%s'", msgs[0].ToolCallID)
	}
}

func TestCollectToolResultsNative_EventsSent(t *testing.T) {
	o := &Orchestrator{maxToolResultChars: 2000}
	internalTCs := []tool.ToolCall{
		{Tool: "t", Function: "f", Args: json.RawMessage(`{}`)},
		{Tool: "t", Function: "g", Args: json.RawMessage(`{}`)},
	}
	nativeTCs := []llm.ToolCall{
		{ID: "call_a", Function: llm.ToolCallFunction{Name: "t__f", Arguments: `{}`}},
		{ID: "call_b", Function: llm.ToolCallFunction{Name: "t__g", Arguments: `{}`}},
	}
	results := []toolExecResult{
		{tc: internalTCs[0], result: &tool.Result{Success: true, Data: json.RawMessage(`"a"`)}},
		{tc: internalTCs[1], result: &tool.Result{Success: true, Data: json.RawMessage(`"b"`)}},
	}
	events := make(chan *llm.StreamEvent, 16)

	go func() {
		o.collectToolResultsNative(context.Background(), internalTCs, nativeTCs, results, events, "c1", "u1", nil)
		close(events)
	}()

	var toolResults []*llm.StreamEvent
	for evt := range events {
		if evt.Type == "tool_result" {
			toolResults = append(toolResults, evt)
		}
	}
	if len(toolResults) != 2 {
		t.Fatalf("expected 2 tool_result events, got %d", len(toolResults))
	}
}

// ==================== nativeToolCallsToInternal ====================

func TestNativeToolCallsToInternal_Single(t *testing.T) {
	nativeTCs := []llm.ToolCall{
		{ID: "call_1", Function: llm.ToolCallFunction{Name: "bookkeeping__create_bill", Arguments: `{"amount":100}`}},
	}
	result := nativeToolCallsToInternal(nativeTCs)
	if len(result) != 1 {
		t.Fatalf("expected 1, got %d", len(result))
	}
	if result[0].Tool != "bookkeeping" {
		t.Errorf("expected tool=bookkeeping, got %s", result[0].Tool)
	}
	if result[0].Function != "create_bill" {
		t.Errorf("expected function=create_bill, got %s", result[0].Function)
	}
	if string(result[0].Args) != `{"amount":100}` {
		t.Errorf("expected args='{\"amount\":100}', got %s", string(result[0].Args))
	}
}

func TestNativeToolCallsToInternal_NoSeparator(t *testing.T) {
	// 万一 LLM 返回的函数名不含 __，退化为 tool=func=name
	nativeTCs := []llm.ToolCall{
		{ID: "call_2", Function: llm.ToolCallFunction{Name: "simpleFunc", Arguments: `{}`}},
	}
	result := nativeToolCallsToInternal(nativeTCs)
	if result[0].Tool != "simpleFunc" || result[0].Function != "simpleFunc" {
		t.Errorf("expected tool=func=simpleFunc, got tool=%s func=%s", result[0].Tool, result[0].Function)
	}
}

func TestNativeToolCallsToInternal_MultipleSeparators(t *testing.T) {
	// 函数名含多个 __，用 LastIndex 取最后一个
	nativeTCs := []llm.ToolCall{
		{ID: "call_3", Function: llm.ToolCallFunction{Name: "a__b__c", Arguments: `{}`}},
	}
	result := nativeToolCallsToInternal(nativeTCs)
	if result[0].Tool != "a__b" {
		t.Errorf("expected tool=a__b, got %s", result[0].Tool)
	}
	if result[0].Function != "c" {
		t.Errorf("expected function=c, got %s", result[0].Function)
	}
}
