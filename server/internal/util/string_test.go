package util

import "testing"

func TestTruncateContent(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		maxLen int
		want   string
	}{
		{"short", "hello", 100, "hello"},
		{"equal", "abcde", 5, "abcde"},
		{"long ascii", "hello world, this is a long message", 11, "hello world..."},
		{"chinese short", "你好世界", 10, "你好世界"},
		{"chinese long", "这是一条很长的中文消息需要被截断处理", 10, "这是一条很长的中文消..."},
		{"empty", "", 10, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TruncateContent(tt.input, tt.maxLen)
			if got != tt.want {
				t.Errorf("TruncateContent(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
			}
		})
	}
}
