package agent

import (
	"testing"
)

func TestTruncateRunes(t *testing.T) {
	tests := []struct {
		name  string
		input string
		max   int
		want  string
	}{
		{"short enough", "hello", 10, "hello"},
		{"exact", "hello", 5, "hello"},
		{"truncate ascii", "hello world", 5, "hello"},
		{"truncate chinese", "你好世界这个很长", 4, "你好世界"},
		{"empty", "", 10, ""},
		{"max zero", "hello", 0, ""},
		{"mixed cn/en", "hello你好world世界", 10, "hello你好wor"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateRunes(tt.input, tt.max)
			if got != tt.want {
				t.Errorf("truncateRunes(%q, %d) = %q, want %q", tt.input, tt.max, got, tt.want)
			}
			if len([]rune(got)) > tt.max {
				t.Errorf("truncateRunes(%q, %d) returned %q with len %d > max %d",
					tt.input, tt.max, got, len([]rune(got)), tt.max)
			}
		})
	}
}
