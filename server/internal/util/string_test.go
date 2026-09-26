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

func TestIsValidTopicChars(t *testing.T) {
	trueCases := []string{"睡眠", "项目管理", "WKWebView", "2026计划", "AI工具", "OK", "中文abc"}
	for _, s := range trueCases {
		if !IsValidTopicChars(s) {
			t.Errorf("IsValidTopicChars(%q) = false, want true", s)
		}
	}
	falseCases := []string{"", "   ", "a/b", "求职-面试", "a b", "居住/搬家", "User 记忆", "a,b"}
	for _, s := range falseCases {
		if IsValidTopicChars(s) {
			t.Errorf("IsValidTopicChars(%q) = true, want false", s)
		}
	}
	// 收窄口径：其它语种字母与全角/阿拉伯-印度数字都不算合法。
	narrowCases := []string{"Москв", "こんにちは", "한글", "１２３", "١٢٣"}
	for _, s := range narrowCases {
		if IsValidTopicChars(s) {
			t.Errorf("IsValidTopicChars(%q) = true, want false (narrowed to 中英文数字)", s)
		}
	}
}

func TestKeepTopicChars(t *testing.T) {
	if got := KeepTopicChars("求职-面试"); got != "求职面试" {
		t.Errorf("KeepTopicChars = %q, want 求职面试", got)
	}
	if got := KeepTopicChars("a/b c"); got != "abc" {
		t.Errorf("KeepTopicChars = %q, want abc", got)
	}
	if got := KeepTopicChars("WKWebView!"); got != "WKWebView" {
		t.Errorf("KeepTopicChars = %q, want WKWebView", got)
	}
	if got := KeepTopicChars("///"); got != "" {
		t.Errorf("KeepTopicChars(all-invalid) = %q, want empty", got)
	}
	if got := KeepTopicChars("Москв"); got != "" {
		t.Errorf("KeepTopicChars(cyrillic) = %q, want empty", got)
	}
	if got := KeepTopicChars("１２3"); got != "3" {
		t.Errorf("KeepTopicChars(full-width) = %q, want 3", got)
	}
}
