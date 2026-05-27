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
	msgs := BuildMessagesForFlash(nil, 2000)
	if len(msgs) != 0 {
		t.Errorf("expected 0 msgs, got %d", len(msgs))
	}
}

func TestBuildMessagesForFlash_NonToolMessages(t *testing.T) {
	history := []model.Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi there"},
	}
	msgs := BuildMessagesForFlash(history, 2000)

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
	msgs := BuildMessagesForFlash(history, 2000)

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
	msgs := BuildMessagesForFlash(history, 2000)

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
	msgs := BuildMessagesForFlash(history, 0)

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
	msgs := BuildMessagesForFlash(history, 2000)

	if len(msgs) != 4 {
		t.Fatalf("expected 4 msgs, got %d", len(msgs))
	}
	if len(msgs[1].ToolCalls) != 1 {
		t.Errorf("expected 1 tool call parsed, got %d", len(msgs[1].ToolCalls))
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
	msgs := BuildMessagesForFlash(history, 2000)

	if msgs[0].ToolCallID != "tc-123" {
		t.Errorf("expected ToolCallID='tc-123', got '%s'", msgs[0].ToolCallID)
	}
}

// ==================== collectToolResults ====================

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

func TestCollectToolResults_LongResult_Truncated(t *testing.T) {
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
	if !strings.Contains(msgs[0].Content, "已截断") {
		t.Error("expected truncation notice in long tool result")
	}
	if len(msgs[0].Content) >= len(string(data)) {
		t.Error("expected truncated content shorter than original")
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

func TestCollectToolResults_TruncationOnlyInMsg_NotInEvent(t *testing.T) {
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
	if !strings.Contains(toolResults[0].Content, longStr) {
		t.Error("event should contain full (untruncated) content for frontend")
	}
}

// ==================== BuildMessagesForPro ====================

func TestBuildMessagesForPro_ToolMsgUnderLimit(t *testing.T) {
	history := []model.Message{
		{Role: "tool", Content: `{"success":true}`, ToolCallID: strPtr("tc1")},
	}
	msgs := BuildMessagesForPro(history, 2000)

	if len(msgs) != 1 {
		t.Fatalf("expected 1 msg, got %d", len(msgs))
	}
	if msgs[0].Role != "tool" {
		t.Errorf("expected role=tool, got %s", msgs[0].Role)
	}
	if msgs[0].ToolCallID != "tc1" {
		t.Errorf("expected ToolCallID='tc1', got '%s'", msgs[0].ToolCallID)
	}
}

func TestBuildMessagesForPro_ToolMsgOverLimit(t *testing.T) {
	longData := strings.Repeat("x", 3000)
	content := `{"success":true,"data":"` + longData + `"}`
	history := []model.Message{
		{Role: "tool", Content: content},
	}
	msgs := BuildMessagesForPro(history, 2000)

	if !strings.Contains(msgs[0].Content, "已截断") {
		t.Error("expected truncation in Pro tool result")
	}
}

func TestBuildMessagesForPro_MixedMessages(t *testing.T) {
	history := []model.Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "ok", ToolCallsJSON: strPtr(`[{"id":"t1","function":{"name":"search"}}]`)},
		{Role: "tool", Content: strings.Repeat("x", 3000)},
	}
	msgs := BuildMessagesForPro(history, 2000)

	if len(msgs) != 3 {
		t.Fatalf("expected 3 msgs, got %d", len(msgs))
	}
	if msgs[1].Role != "assistant" {
		t.Errorf("expected assistant role preserved")
	}
	if len(msgs[1].ToolCalls) != 1 {
		t.Errorf("expected tool calls parsed")
	}
	if msgs[2].Role != "tool" {
		t.Errorf("expected tool role preserved")
	}
	if !strings.Contains(msgs[2].Content, "已截断") {
		t.Error("expected tool result truncated")
	}
}
