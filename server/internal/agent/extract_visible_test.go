package agent

import (
	"strings"
	"testing"
)

// ---- ParseLLMResponse: 反思链路 ReactLoop 的 prompt engineering 文本解析 ----
// （主聊天走原生 FC，不再使用本函数）

func TestParseLLMResponse_ValidJSONNoSpace(t *testing.T) {
	parsed, err := ParseLLMResponse(`{"reply":"hello","tool_calls":[]}`)
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
	parsed, err := ParseLLMResponse(`{"reply": "hello", "tool_calls": []}`)
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
	parsed, err := ParseLLMResponse("今天天气不错")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed != nil {
		t.Errorf("expected nil response for pure text, got reply='%s'", parsed.Reply)
	}
}

func TestParseLLMResponse_WithToolCalls(t *testing.T) {
	input := `{"reply":"已记账","tool_calls":[{"tool":"bookkeeping","function":"create","args":{"amount":100}}]}`
	parsed, err := ParseLLMResponse(input)
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
	parsed, err := ParseLLMResponse(input)
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
	parsed, err := ParseLLMResponse(input)
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
	parsed, err := ParseLLMResponse(input)
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
	parsed, err := ParseLLMResponse("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed != nil {
		t.Errorf("expected nil for empty input, got reply='%s'", parsed.Reply)
	}
}

func TestParseLLMResponse_WhitespaceOnly(t *testing.T) {
	parsed, err := ParseLLMResponse("   \n\t  ")
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
	parsed, err := ParseLLMResponse(input)
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
	parsed, err := ParseLLMResponse(input)
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
