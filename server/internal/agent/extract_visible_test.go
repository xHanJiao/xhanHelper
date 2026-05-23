package agent

import (
	"strings"
	"testing"
)

// ---- extractAllVisibleText tests ----

func TestExtractAllVisibleText_PureText(t *testing.T) {
	// LLM 保证返回 JSON，纯文本不会出现，返回空
	result := extractAllVisibleText("今天天气不错")
	if result != "" {
		t.Errorf("expected '', got '%s'", result)
	}
}

func TestExtractAllVisibleText_JSONWithReply(t *testing.T) {
	result := extractAllVisibleText(`{"reply":"好的，已记账","tool_calls":[]}`)
	if result != "好的，已记账" {
		t.Errorf("expected '好的，已记账', got '%s'", result)
	}
}

func TestExtractAllVisibleText_JSONReplyWithEscape(t *testing.T) {
	result := extractAllVisibleText(`{"reply":"你好！\"今天\"天气不错","tool_calls":[{"tool":"test","function":"run","args":{}}]}`)
	if result != `你好！"今天"天气不错` {
		t.Errorf("expected '你好！\"今天\"天气不错', got '%s'", result)
	}
}

func TestExtractAllVisibleText_JSONReplyNotYetComplete(t *testing.T) {
	result := extractAllVisibleText(`{"reply":"好的，已`)
	if result != "好的，已" {
		t.Errorf("expected '好的，已', got '%s'", result)
	}
}

func TestExtractAllVisibleText_JSONBeforeReply(t *testing.T) {
	result := extractAllVisibleText(`{"tool_calls":[`)
	if result != "" {
		t.Errorf("expected '', got '%s'", result)
	}
}

func TestExtractAllVisibleText_ProgressiveStreaming(t *testing.T) {
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

// ---- 修复验证：reply 内容含未转义 JSON 数组 ----

func TestExtractAllVisibleText_UnescapedArrayInReply(t *testing.T) {
	// 模拟 LLM 输出 reply 内容含 persons: ["alice"]，未正确转义引号
	// 修复后 unescapeJSONString 不应对 ["alice" 的 " 截断
	input := `{"reply":"在笔记里加 persons 字段：\n\npersons: ["alice", "bob"]"}`
	result := extractAllVisibleText(input)
	if !strings.Contains(result, "alice") {
		t.Errorf("reply should contain 'alice' but was truncated, got: '%s'", result)
	}
	if !strings.Contains(result, "bob") {
		t.Errorf("reply should contain 'bob' but was truncated, got: '%s'", result)
	}
	if !strings.Contains(result, "persons: [") {
		t.Errorf("reply should contain 'persons: [' but was truncated, got: '%s'", result)
	}
}

func TestExtractAllVisibleText_UnescapedNestedObjectInReply(t *testing.T) {
	// reply 内容含未转义的 JSON 对象
	input := `{"reply":"配置如下：\n\n{"key": "value", "enabled": true}"}`
	result := extractAllVisibleText(input)
	if !strings.Contains(result, `"key"`) {
		t.Errorf("reply should contain nested object, got: '%s'", result)
	}
	if !strings.Contains(result, "enabled") {
		t.Errorf("reply should contain 'enabled', got: '%s'", result)
	}
}

func TestExtractAllVisibleText_UnescapedSingleQuoteInReply(t *testing.T) {
	// reply 内容含单个未转义引号（如英文缩写）
	input := `{"reply":"He said \"hello\" to me, and I replied \"hi\""}`
	result := extractAllVisibleText(input)
	if !strings.Contains(result, `hello`) {
		t.Errorf("reply should contain 'hello', got: '%s'", result)
	}
	if !strings.Contains(result, `hi`) {
		t.Errorf("reply should contain 'hi', got: '%s'", result)
	}
}

func TestExtractAllVisibleText_StreamingWithUnescapedArray(t *testing.T) {
	// 渐进式流式到达，reply 内容含未转义数组
	chunks := []string{
		`{"reply":"好，第一步：定义人物库\n\n`,
		`在笔记里加一个 persons 字段：\n\n`,
		`persons: ["alice", "bob", "charlie"]`,
		`"}`,
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

	joined := ""
	for _, v := range allVisible {
		joined += v
	}
	if !strings.Contains(joined, "alice") {
		t.Errorf("streaming reply should contain 'alice', got: '%s'", joined)
	}
	if !strings.Contains(joined, "charlie") {
		t.Errorf("streaming reply should contain 'charlie', got: '%s'", joined)
	}
}

func TestExtractAllVisibleText_ReplyFollowedByComma(t *testing.T) {
	// reply 不是最后一个字段，闭合引号后跟 ,"tool_calls"
	input := `{"reply":"简短回复","tool_calls":[{"tool":"test","function":"run","args":{}}]}`
	result := extractAllVisibleText(input)
	if result != "简短回复" {
		t.Errorf("expected '简短回复', got '%s'", result)
	}
}

func TestExtractAllVisibleText_ReplyWithColonBracket(t *testing.T) {
	// 用户报告的精确场景：reply 内容以 : [ 结尾，后面应跟值
	input := `{"reply":"加一个字段：\n\npersons: ["alice"]"}`
	result := extractAllVisibleText(input)
	if !strings.Contains(result, "alice") {
		t.Errorf("reply should contain 'alice', got: '%s'", result)
	}
}

// ---- unescapeJSONString 详细测试 ----

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

func TestUnescapeJSONString_CloseWithBrace(t *testing.T) {
	// 闭合引号后紧跟 }（reply 为最后一个字段）
	s, complete := unescapeJSONString(`hello"}`)
	if !complete {
		t.Error("expected complete=true")
	}
	if s != "hello" {
		t.Errorf("expected 'hello', got '%s'", s)
	}
}

func TestUnescapeJSONString_CloseWithComma(t *testing.T) {
	// 闭合引号后紧跟 ,（reply 后有其他字段）
	s, complete := unescapeJSONString(`hello","tool_calls`)
	if !complete {
		t.Error("expected complete=true")
	}
	if s != "hello" {
		t.Errorf("expected 'hello', got '%s'", s)
	}
}

func TestUnescapeJSONString_CloseWithWhitespaceBeforeComma(t *testing.T) {
	// 闭合引号后有空白再跟 ,
	s, complete := unescapeJSONString(`hello"   ,"tool_calls`)
	if !complete {
		t.Error("expected complete=true")
	}
	if s != "hello" {
		t.Errorf("expected 'hello', got '%s'", s)
	}
}

func TestUnescapeJSONString_TolerateUnescapedQuote(t *testing.T) {
	// 未转义引号后跟的不是 , 或 }，当作普通字符
	s, complete := unescapeJSONString(`list: ["a", "b"]"}`)
	if !complete {
		t.Error("expected complete=true")
	}
	if s != `list: ["a", "b"]` {
		t.Errorf("expected 'list: [\"a\", \"b\"]', got '%s'", s)
	}
}

func TestUnescapeJSONString_TolerateMultipleUnescapedQuotes(t *testing.T) {
	// 多个未转义引号，只有最后一个是结构闭合
	s, complete := unescapeJSONString(`["a", "b", "c"]"}`)
	if !complete {
		t.Error("expected complete=true")
	}
	if s != `["a", "b", "c"]` {
		t.Errorf("expected '[\"a\", \"b\", \"c\"]', got '%s'", s)
	}
}

func TestUnescapeJSONString_TolerateNestedBraceInContent(t *testing.T) {
	// 内容含 {}，但闭合引号后的 } 才是结构结束
	s, complete := unescapeJSONString(`func(){}"}`)
	if !complete {
		t.Error("expected complete=true")
	}
	if s != `func(){}` {
		t.Errorf("expected 'func(){}', got '%s'", s)
	}
}

func TestUnescapeJSONString_MixedEscapedAndUnescaped(t *testing.T) {
	// 混用：部分正确转义、部分未转义
	s, complete := unescapeJSONString(`He said \"hello\", then ["a", "b"]"}`)
	if !complete {
		t.Error("expected complete=true")
	}
	if !strings.Contains(s, `"a"`) {
		t.Errorf("expected to contain '\"a\"', got '%s'", s)
	}
	if !strings.Contains(s, `hello`) {
		t.Errorf("expected to contain 'hello', got '%s'", s)
	}
}

func TestUnescapeJSONString_EmptyString(t *testing.T) {
	s, complete := unescapeJSONString(`"`)
	if !complete {
		t.Error("expected complete=true")
	}
	if s != "" {
		t.Errorf("expected '', got '%s'", s)
	}
}
