package controller

import "testing"

func TestFindClosingBrace(t *testing.T) {
	tests := []struct {
		name  string
		s     string
		start int
		want  int
	}{
		{"simple", `{"a":1}`, 0, 6},
		{"nested", `{"a":{"b":2}}`, 0, 12},
		{"array inside", `{"a":[1,2]}`, 0, 10},
		{"text before", `hello {"a":1}`, 6, 12},
		{"text after", `{"a":1} bye`, 0, 6},
		{"text both sides", `hi {"a":1} bye`, 3, 9},
		{"more nesting", `{"a":{"b":{"c":3}},"d":4}`, 0, 24},
		{"more nesting with text", `hi {"a":{"b":{"c":3}},"d":4} bye`, 3, 27},
		{"no closing brace", `{"a":1`, 0, 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findClosingBrace(tt.s, tt.start)
			if got != tt.want {
				t.Errorf("findClosingBrace(%q, %d) = %d, want %d", tt.s, tt.start, got, tt.want)
			}
		})
	}
}

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
		{"json after text with reply", "好的，马上删除这笔。\n\n{\n    \"reply\": \"正在删除...\",\n    \"tool_calls\": []\n}", "好的，马上删除这笔。\n\n正在删除..."},
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
