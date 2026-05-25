package pipeline

import (
	"strings"
	"testing"
)

// ===== 工具函数测试 =====

func TestExtractJSON(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{`{"a": 1}`, `{"a": 1}`},
		{`some text {"agent_id": "x", "confidence": 0.9} more text`, `{"agent_id": "x", "confidence": 0.9}`},
		{`no json here`, ``},
		{`{"key": "value"}`, `{"key": "value"}`},
	}

	for _, c := range cases {
		result := extractJSON(c.input)
		if result != c.expected {
			t.Errorf("input=%q: expected=%q, got=%q", c.input, c.expected, result)
		}
	}
}

func TestTruncateSummary(t *testing.T) {
	if s := truncateSummary("hello", 10); s != "hello" {
		t.Errorf("expected no truncation: %q", s)
	}
	s := truncateSummary("这是一个很长的文本需要被截断", 5)
	if !strings.HasSuffix(s, "...") || len([]rune(s)) > 8 {
		t.Errorf("expected truncated with ...: %q (len=%d)", s, len([]rune(s)))
	}
}
