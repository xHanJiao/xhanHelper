package controller

import "testing"

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
