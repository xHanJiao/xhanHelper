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

func TestTruncateMiddle(t *testing.T) {
	long := "这是一条非常长的笔记内容，包含很多细节信息，需要被中间截断以节省token，保留关键的开头部分和结尾的deadline信息。"
	head, tail := 10, 5
	got := TruncateMiddle(long, head, tail)
	if got != "这是一条非常长的笔记…ne信息。" {
		t.Errorf("TruncateMiddle = %q", got)
	}

	short := "短文本"
	if got := TruncateMiddle(short, 10, 5); got != short {
		t.Errorf("short should pass through unchanged, got %q", got)
	}

	exact := "12345"
	if got := TruncateMiddle(exact, 3, 2); got != exact {
		t.Errorf("exact-fit head+tail should pass through, got %q", got)
	}

	ascii := "abcdefghijklmnopqrstuvwxyz"
	if got := TruncateMiddle(ascii, 5, 5); got != "abcde…vwxyz" {
		t.Errorf("ascii truncate = %q", got)
	}
}
