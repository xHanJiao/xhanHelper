package agent

import (
	"testing"
)

func TestExtractAllVisibleText_PureText(t *testing.T) {
	// 纯文本：直接返回原文
	result := extractAllVisibleText("今天天气不错")
	if result != "今天天气不错" {
		t.Errorf("expected '今天天气不错', got '%s'", result)
	}
}

func TestExtractAllVisibleText_JSONWithReply(t *testing.T) {
	// JSON 格式：提取 reply 字段值
	result := extractAllVisibleText(`{"reply":"好的，已记账","tool_calls":[]}`)
	if result != "好的，已记账" {
		t.Errorf("expected '好的，已记账', got '%s'", result)
	}
}

func TestExtractAllVisibleText_JSONReplyWithEscape(t *testing.T) {
	// reply 中包含转义字符
	result := extractAllVisibleText(`{"reply":"你好！\"今天\"天气不错","tool_calls":[{"tool":"test","function":"run","args":{}}]}`)
	if result != `你好！"今天"天气不错` {
		t.Errorf("expected '你好！\"今天\"天气不错', got '%s'", result)
	}
}

func TestExtractAllVisibleText_JSONReplyNotYetComplete(t *testing.T) {
	// JSON 的 reply 字段还没闭合——返回已能提取的部分
	result := extractAllVisibleText(`{"reply":"好的，已`)
	if result != "好的，已" {
		t.Errorf("expected '好的，已', got '%s'", result)
	}
}

func TestExtractAllVisibleText_JSONBeforeReply(t *testing.T) {
	// JSON 已开始但 reply 字段还不在缓冲区中
	result := extractAllVisibleText(`{"tool_calls":[`)
	if result != "" {
		t.Errorf("expected '', got '%s'", result)
	}
}

func TestExtractAllVisibleText_ProgressiveStreaming(t *testing.T) {
	// 模拟分 chunk 到达的渐进流式场景
	chunks := []string{
		`{"repl`,
		`y":"`,
		`你好，`,
		`今天天气`,
		`不错"}`,
	}

	var fullText string
	lastLen := 0
	var allVisible []string

	for _, chunk := range chunks {
		fullText += chunk
		visible := extractAllVisibleText(fullText)
		if len(visible) > lastLen {
			allVisible = append(allVisible, visible[lastLen:])
			lastLen = len(visible)
		}
	}

	// 收集到的可见文本应为 "你好，今天天气不错"
	joined := ""
	for _, v := range allVisible {
		joined += v
	}
	if joined != "你好，今天天气不错" {
		t.Errorf("expected '你好，今天天气不错', got '%s' (deltas: %v)", joined, allVisible)
	}
}

func TestExtractAllVisibleText_EmptyInput(t *testing.T) {
	result := extractAllVisibleText("")
	if result != "" {
		t.Errorf("expected '', got '%s'", result)
	}
}

func TestUnescapeJSONString_Simple(t *testing.T) {
	s, complete := unescapeJSONString(`hello"`)
	if !complete {
		t.Error("expected complete=true")
	}
	if s != "hello" {
		t.Errorf("expected 'hello', got '%s'", s)
	}
}

func TestUnescapeJSONString_WithEscapes(t *testing.T) {
	s, complete := unescapeJSONString(`hello\nworld\t!\"end\\final"`)
	if !complete {
		t.Error("expected complete=true")
	}
	if s != "hello\nworld\t!\"end\\final" {
		t.Errorf("unexpected result: '%s'", s)
	}
}

func TestUnescapeJSONString_Incomplete(t *testing.T) {
	s, complete := unescapeJSONString(`hello world`)
	if complete {
		t.Error("expected complete=false (no closing quote)")
	}
	if s != "hello world" {
		t.Errorf("expected 'hello world', got '%s'", s)
	}
}
