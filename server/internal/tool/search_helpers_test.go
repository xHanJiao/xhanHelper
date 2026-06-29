package tool

import (
	"strings"
	"testing"
	"time"
)

// ==================== splitKeywords ====================

func TestSplitKeywords(t *testing.T) {
	tests := []struct {
		input    string
		expected int
		first    string
	}{
		{"", 0, ""},
		{"羽毛球", 1, "羽毛球"},
		{"羽毛球 SQL", 2, "羽毛球"},
		{"羽毛球,SQL", 2, "羽毛球"},
		{"羽毛球，SQL", 2, "羽毛球"},
		{"羽毛球、SQL", 2, "羽毛球"},
		{"羽毛球 SQL 优化", 3, "羽毛球"},
		{"羽毛球 SQL SQL", 2, "羽毛球"}, // dedup
		{"  羽毛球  SQL  ", 2, "羽毛球"},
		{"北京\"套利\"", 2, "北京"}, // strip ASCII double quotes
		{"北京'套利'", 2, "北京"},   // strip ASCII single quotes
		{"“羽毛球”", 1, "羽毛球"},   // strip curly quotes
		{"   ", 0, ""},
	}
	for _, tt := range tests {
		result := splitKeywords(tt.input)
		if len(result) != tt.expected {
			t.Errorf("splitKeywords(%q) = %d keywords, want %d: %v", tt.input, len(result), tt.expected, result)
		}
		if tt.expected > 0 && len(result) > 0 && result[0] != tt.first {
			t.Errorf("splitKeywords(%q) first = %q, want %q", tt.input, result[0], tt.first)
		}
	}
}

// ==================== findMatchedKeywords ====================

func TestFindMatchedKeywords(t *testing.T) {
	// basic
	matched := findMatchedKeywords("今天去羽毛球场打球", []string{"羽毛球", "SQL"})
	if len(matched) != 1 || matched[0] != "羽毛球" {
		t.Errorf("expected [羽毛球], got %v", matched)
	}

	// case insensitive
	matched = findMatchedKeywords("Learn SQL Optimization", []string{"sql"})
	if len(matched) != 1 || matched[0] != "sql" {
		t.Errorf("expected [sql] (case-insensitive), got %v", matched)
	}

	// no match
	matched = findMatchedKeywords("nothing here", []string{"外星人"})
	if len(matched) != 0 {
		t.Errorf("expected empty, got %v", matched)
	}

	// empty keywords
	matched = findMatchedKeywords("some content", nil)
	if matched != nil {
		t.Errorf("expected nil for nil keywords, got %v", matched)
	}

	// empty keywords slice
	matched = findMatchedKeywords("some content", []string{})
	if matched != nil {
		t.Errorf("expected nil for empty keywords, got %v", matched)
	}

	// multiple matches
	matched = findMatchedKeywords("SQL优化和sql注入", []string{"SQL", "优化"})
	if len(matched) != 2 {
		t.Errorf("expected 2 matches, got %v", matched)
	}
}

// ==================== parseTimeString ====================

func TestParseTimeString(t *testing.T) {
	tests := []struct {
		input string
		year  int
		month time.Month
		day   int
		hour  int
		min   int
	}{
		{"2026-06-15T14:30:00+08:00", 2026, 6, 15, 14, 30},
		{"2026-06-15 14:30:00", 2026, 6, 15, 14, 30},
		{"2026-06-15T14:30:05", 2026, 6, 15, 14, 30},
		{"2026/06/15 14:30:05", 2026, 6, 15, 14, 30},
		{"2026-06-15", 2026, 6, 15, 0, 0},
		{"2026/06/15", 2026, 6, 15, 0, 0},
	}
	for _, tt := range tests {
		parsed, err := parseTimeString(tt.input)
		if err != nil {
			t.Errorf("parseTimeString(%q) error: %v", tt.input, err)
			continue
		}
		// Convert to BJ time zone for comparison
		bj := parsed.In(time.FixedZone("CST", 8*3600))
		if bj.Year() != tt.year || bj.Month() != tt.month || bj.Day() != tt.day {
			t.Errorf("parseTimeString(%q) date = %s, want %d-%02d-%02d",
				tt.input, bj.Format("2006-01-02"), tt.year, tt.month, tt.day)
		}
		if bj.Hour() != tt.hour || bj.Minute() != tt.min {
			t.Errorf("parseTimeString(%q) time = %02d:%02d, want %02d:%02d",
				tt.input, bj.Hour(), bj.Minute(), tt.hour, tt.min)
		}
	}

	// invalid
	_, err := parseTimeString("not-a-date")
	if err == nil {
		t.Error("expected error for invalid date")
	}
}

// ==================== isDateOnly ====================

func TestIsDateOnly(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"2026-06-15", true},
		{"2026/06/15", true},
		{"2026-06-15T14:30:00+08:00", false},
		{"2026-06-15 14:30:00", false},
		{"not-a-date", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := isDateOnly(tt.input); got != tt.expected {
			t.Errorf("isDateOnly(%q) = %v, want %v", tt.input, got, tt.expected)
		}
	}
}

// ==================== parseTimeParams ====================

func TestParseTimeParams(t *testing.T) {
	t.Run("both absolute and relative", func(t *testing.T) {
		_, _, err := parseTimeParams("2026-06-15T00:00:00+08:00", "", 7, 0)
		if err == nil {
			t.Error("expected error for mutually exclusive params")
		}
	})

	t.Run("invalid start_time", func(t *testing.T) {
		_, _, err := parseTimeParams("not-a-date", "", 0, 0)
		if err == nil {
			t.Error("expected error for invalid start_time")
		}
	})

	t.Run("invalid end_time", func(t *testing.T) {
		_, _, err := parseTimeParams("", "not-a-date", 0, 0)
		if err == nil {
			t.Error("expected error for invalid end_time")
		}
	})

	t.Run("last_n_days", func(t *testing.T) {
		after, before, err := parseTimeParams("", "", 7, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if after == nil {
			t.Error("expected non-nil after")
		}
		if before != nil {
			t.Error("expected nil before")
		}
		// after should be ~7 days ago
		expected := time.Now().Add(-7 * 24 * time.Hour)
		diff := after.Sub(expected)
		if diff < -time.Second || diff > time.Second {
			t.Errorf("after ≈ %v, want ≈ %v (diff=%v)", *after, expected, diff)
		}
	})

	t.Run("last_n_hours", func(t *testing.T) {
		after, before, err := parseTimeParams("", "", 0, 6)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if after == nil {
			t.Error("expected non-nil after")
		}
		if before != nil {
			t.Error("expected nil before")
		}
	})

	t.Run("absolute range", func(t *testing.T) {
		after, before, err := parseTimeParams("2026-06-15T00:00:00+08:00", "2026-06-20T00:00:00+08:00", 0, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if after == nil || before == nil {
			t.Error("expected both after and before")
		}
	})

	t.Run("date-only end_time advances 1 day", func(t *testing.T) {
		after, before, err := parseTimeParams("2026-06-27", "2026-06-27", 0, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if after == nil || before == nil {
			t.Fatal("expected both after and before")
		}
		// after should be 2026-06-27 00:00:00
		// before should be 2026-06-28 00:00:00 (pushed forward 1 day)
		if !after.Before(*before) {
			t.Errorf("after %v should be before before %v", *after, *before)
		}
		dayDiff := before.Sub(*after)
		if dayDiff != 24*time.Hour {
			t.Errorf("expected 24h between date-only start and end, got %v", dayDiff)
		}
	})

	t.Run("no params", func(t *testing.T) {
		after, before, err := parseTimeParams("", "", 0, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if after != nil || before != nil {
			t.Error("expected both nil")
		}
	})
}

// ==================== formatMsgTime ====================

func TestFormatMsgTime(t *testing.T) {
	// Time in +08:00 zone
	loc := time.FixedZone("CST", 8*3600)
	tm := time.Date(2026, 6, 15, 14, 30, 0, 0, loc)
	result := formatMsgTime(tm)
	if result != "2026-06-15T14:30:00+08:00" {
		t.Errorf("expected '2026-06-15T14:30:00+08:00', got %q", result)
	}

	// UTC time should be converted to +08:00
	utc := time.Date(2026, 6, 15, 6, 30, 0, 0, time.UTC)
	result = formatMsgTime(utc)
	if result != "2026-06-15T14:30:00+08:00" {
		t.Errorf("expected '2026-06-15T14:30:00+08:00', got %q", result)
	}
}

// ==================== acceptedTimeFormats ====================

func TestAcceptedTimeFormats(t *testing.T) {
	if len(acceptedTimeFormats) < 4 {
		t.Errorf("expected at least 4 formats, got %d", len(acceptedTimeFormats))
	}
}

// ==================== truncateAssistantContent (message-specific) ====================

func TestTruncateAssistantContent(t *testing.T) {
	t.Run("short content", func(t *testing.T) {
		content := "短消息"
		result, truncated := truncateAssistantContent(content, "msg-1")
		if truncated {
			t.Error("short content should not be truncated")
		}
		if result != content {
			t.Errorf("expected unchanged, got %q", result)
		}
	})

	t.Run("exactly 100 chars", func(t *testing.T) {
		content := strings.Repeat("测", 100)
		result, truncated := truncateAssistantContent(content, "msg-1")
		if truncated {
			t.Error("exactly 100 chars should not be truncated")
		}
		if result != content {
			t.Errorf("expected unchanged at 100 chars")
		}
	})

	t.Run("over 100 chars", func(t *testing.T) {
		content := strings.Repeat("测", 150)
		result, truncated := truncateAssistantContent(content, "msg-test")
		if !truncated {
			t.Error("content > 100 chars should be truncated")
		}
		if !strings.Contains(result, "内容已截断") {
			t.Error("truncated content should have hint")
		}
		if !strings.Contains(result, "msg-test") {
			t.Error("truncation hint should contain message ID")
		}
	})
}
