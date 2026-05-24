package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

// ---- parseLLMResponse: 标准 JSON 解析替代字符串提取 ----

func TestParseLLMResponse_ValidJSONNoSpace(t *testing.T) {
	parsed, err := parseLLMResponse(`{"reply":"hello","tool_calls":[]}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed == nil {
		t.Fatal("expected non-nil response")
	}
	if parsed.Reply != "hello" {
		t.Errorf("expected reply='hello', got '%s'", parsed.Reply)
	}
}

func TestParseLLMResponse_ValidJSONWithSpace(t *testing.T) {
	// LLM 输出带空格的 JSON（标准 JSON 允许冒号后空格）
	parsed, err := parseLLMResponse(`{"reply": "hello", "tool_calls": []}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed == nil {
		t.Fatal("expected non-nil response")
	}
	if parsed.Reply != "hello" {
		t.Errorf("expected reply='hello', got '%s'", parsed.Reply)
	}
}

func TestParseLLMResponse_PureText(t *testing.T) {
	// 纯文本 → 返回 nil（无错误，表示不需要工具调用）
	parsed, err := parseLLMResponse("今天天气不错")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed != nil {
		t.Errorf("expected nil response for pure text, got reply='%s'", parsed.Reply)
	}
}

func TestParseLLMResponse_WithToolCalls(t *testing.T) {
	input := `{"reply":"已记账","tool_calls":[{"tool":"bookkeeping","function":"create","args":{"amount":100}}]}`
	parsed, err := parseLLMResponse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed == nil {
		t.Fatal("expected non-nil response")
	}
	if parsed.Reply != "已记账" {
		t.Errorf("expected reply='已记账', got '%s'", parsed.Reply)
	}
	if len(parsed.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(parsed.ToolCalls))
	}
	if parsed.ToolCalls[0].Tool != "bookkeeping" {
		t.Errorf("expected tool='bookkeeping', got '%s'", parsed.ToolCalls[0].Tool)
	}
}

func TestParseLLMResponse_ReplyWithEscapedQuotes(t *testing.T) {
	input := `{"reply":"He said \"hello\" to me"}`
	parsed, err := parseLLMResponse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed == nil {
		t.Fatal("expected non-nil response")
	}
	if !strings.Contains(parsed.Reply, `"hello"`) {
		t.Errorf("expected reply to contain escaped quotes, got '%s'", parsed.Reply)
	}
}

func TestParseLLMResponse_ReplyWithChinese(t *testing.T) {
	input := `{"reply":"好的，已帮你记录这笔支出","tool_calls":[]}`
	parsed, err := parseLLMResponse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed == nil {
		t.Fatal("expected non-nil response")
	}
	if parsed.Reply != "好的，已帮你记录这笔支出" {
		t.Errorf("unexpected reply: '%s'", parsed.Reply)
	}
}

func TestParseLLMResponse_ReplyWithNewlines(t *testing.T) {
	input := "{\"reply\":\"第一行\\n第二行\\n第三行\"}"
	parsed, err := parseLLMResponse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed == nil {
		t.Fatal("expected non-nil response")
	}
	if !strings.Contains(parsed.Reply, "第二行") {
		t.Errorf("expected reply to contain newline-separated text, got '%s'", parsed.Reply)
	}
}

func TestParseLLMResponse_EmptyInput(t *testing.T) {
	parsed, err := parseLLMResponse("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed != nil {
		t.Errorf("expected nil for empty input, got reply='%s'", parsed.Reply)
	}
}

func TestParseLLMResponse_WhitespaceOnly(t *testing.T) {
	parsed, err := parseLLMResponse("   \n\t  ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed != nil {
		t.Errorf("expected nil for whitespace-only, got reply='%s'", parsed.Reply)
	}
}

func TestParseLLMResponse_MarkdownFenceWrapped(t *testing.T) {
	// LLM 可能用 markdown fence 包裹 JSON
	input := "```json\n{\"reply\":\"hello\"}\n```"
	parsed, err := parseLLMResponse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed == nil {
		t.Fatal("expected non-nil response")
	}
	if parsed.Reply != "hello" {
		t.Errorf("expected reply='hello', got '%s'", parsed.Reply)
	}
}

func TestParseLLMResponse_OnlyToolCallsNoReply(t *testing.T) {
	// 极少数情况：LLM 可能只返回工具调用不返回 reply
	input := `{"tool_calls":[{"tool":"note","function":"search","args":{"query":"test"}}]}`
	parsed, err := parseLLMResponse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed == nil {
		t.Fatal("expected non-nil response for tool-only JSON")
	}
	if len(parsed.ToolCalls) != 1 {
		t.Errorf("expected 1 tool call, got %d", len(parsed.ToolCalls))
	}
}

// ---- extractCleanText: 从 LLM 输出提取干净文本 ----

func TestExtractCleanText_JSONWithReply(t *testing.T) {
	result := extractCleanText(`{"reply":"今天天气不错"}`)
	if result != "今天天气不错" {
		t.Errorf("expected '今天天气不错', got '%s'", result)
	}
}

func TestExtractCleanText_JSONWithReplyAndToolCalls(t *testing.T) {
	result := extractCleanText(`{"reply":"已记账","tool_calls":[{"tool":"bookkeeping","function":"create","args":{"amount":100}}]}`)
	if result != "已记账" {
		t.Errorf("expected '已记账', got '%s'", result)
	}
}

func TestExtractCleanText_JSONWithPrefixBeforeBrace(t *testing.T) {
	// 严格 JSON：前导文本 + JSON → parseLLMResponse 返回 prefix error → 返回空
	result := extractCleanText("好的，我来查看\n{\"reply\":\"正在查询...\",\"tool_calls\":[{\"tool\":\"agent\",\"function\":\"list_agents\",\"args\":{}}]}")
	if result != "" {
		t.Errorf("expected empty for prefix text (strict mode), got '%s'", result)
	}
}

func TestExtractCleanText_PureText(t *testing.T) {
	// 纯文本容错接受，返回全文
	result := extractCleanText("今天天气不错，适合出去走走")
	if result != "今天天气不错，适合出去走走" {
		t.Errorf("expected full text, got '%s'", result)
	}
}

func TestExtractCleanText_InvalidJSON(t *testing.T) {
	// 严格 JSON 模式：非法 JSON 返回空，由 handleFlashResponse 触发重试
	result := extractCleanText("这不是 JSON {{{ broken")
	if result != "" {
		t.Errorf("expected empty string for invalid JSON (strict mode), got '%s'", result)
	}
}

// ---- maybeInjectCreatedAt ----

func TestMaybeInjectCreatedAt_ValidJSONWithReply(t *testing.T) {
	result := maybeInjectCreatedAt(`{"reply":"hello","tool_calls":[]}`)
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(result), &m); err != nil {
		t.Fatalf("result should be valid JSON: %v", err)
	}
	if m["reply"] != "hello" {
		t.Errorf("reply should be preserved, got %v", m["reply"])
	}
	if _, ok := m["created_at"]; !ok {
		t.Error("created_at field should be injected")
	}
}

func TestMaybeInjectCreatedAt_NoReplyKey(t *testing.T) {
	// 不含 reply 字段的 JSON → 原样返回
	result := maybeInjectCreatedAt(`{"tool_calls":[]}`)
	if result != `{"tool_calls":[]}` {
		t.Errorf("expected unchanged, got '%s'", result)
	}
}

func TestMaybeInjectCreatedAt_InvalidJSON(t *testing.T) {
	// 非法 JSON → 原样返回
	result := maybeInjectCreatedAt(`{broken`)
	if result != `{broken` {
		t.Errorf("expected unchanged, got '%s'", result)
	}
}

func TestMaybeInjectCreatedAt_PureText(t *testing.T) {
	// 纯文本 → 原样返回
	result := maybeInjectCreatedAt("今天天气不错")
	if result != "今天天气不错" {
		t.Errorf("expected unchanged, got '%s'", result)
	}
}

func TestMaybeInjectCreatedAt_PreservesToolCalls(t *testing.T) {
	raw := `{"reply":"ok","tool_calls":[{"tool":"note","function":"record","args":{"topic":"t"}}]}`
	result := maybeInjectCreatedAt(raw)
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(result), &m); err != nil {
		t.Fatalf("result should be valid JSON: %v", err)
	}
	if m["reply"] != "ok" {
		t.Errorf("reply lost: %v", m["reply"])
	}
	tc, ok := m["tool_calls"]
	if !ok {
		t.Error("tool_calls should be preserved")
	}
	_ = tc
	if _, ok := m["created_at"]; !ok {
		t.Error("created_at should be injected")
	}
}

func TestExtractCleanText_EmptyInput(t *testing.T) {
	result := extractCleanText("")
	if result != "" {
		t.Errorf("expected empty string, got '%s'", result)
	}
}
