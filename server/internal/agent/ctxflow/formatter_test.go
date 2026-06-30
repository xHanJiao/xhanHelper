package ctxflow

import (
	"strings"
	"testing"
	"time"
)

func TestDateTimeBlock(t *testing.T) {
	// 2026-01-07 is a Wednesday, 14:30 UTC
	tm := time.Date(2026, 1, 7, 14, 30, 0, 0, time.UTC)

	block := DateTimeBlock(tm)

	if block.Type != "current_datetime" {
		t.Errorf("Type = %q, want %q", block.Type, "current_datetime")
	}

	// Content should contain: "2026年1月7日 星期三 14:30 UTC"
	if !strings.Contains(block.Content, "2026年1月7日") {
		t.Errorf("Content missing date: %q", block.Content)
	}
	if !strings.Contains(block.Content, "星期三") {
		t.Errorf("Content missing weekday 星期三: %q", block.Content)
	}
	if !strings.Contains(block.Content, "14:30") {
		t.Errorf("Content missing time: %q", block.Content)
	}
	if !strings.Contains(block.Content, "UTC") {
		t.Errorf("Content missing timezone: %q", block.Content)
	}

	// Verify exact format: "2006年1月2日 星期X 15:04 MST"
	expected := "2026年1月7日 星期三 14:30 UTC"
	if block.Content != expected {
		t.Errorf("Content = %q, want %q", block.Content, expected)
	}
}

func TestDateTimeBlock_AllWeekdays(t *testing.T) {
	// Week of 2026-01-01 (Thu) to 2026-01-07 (Wed), covering all 7 weekdays.
	// 2026-01-01 = Thursday, ..., 2026-01-04 = Sunday, ..., 2026-01-07 = Wednesday.
	tests := []struct {
		date    time.Time
		weekday string
	}{
		{time.Date(2026, 1, 4, 8, 0, 0, 0, time.UTC), "星期日"},
		{time.Date(2026, 1, 5, 8, 0, 0, 0, time.UTC), "星期一"},
		{time.Date(2026, 1, 6, 8, 0, 0, 0, time.UTC), "星期二"},
		{time.Date(2026, 1, 7, 8, 0, 0, 0, time.UTC), "星期三"},
		{time.Date(2026, 1, 1, 8, 0, 0, 0, time.UTC), "星期四"},
		{time.Date(2026, 1, 2, 8, 0, 0, 0, time.UTC), "星期五"},
		{time.Date(2026, 1, 3, 8, 0, 0, 0, time.UTC), "星期六"},
	}

	for _, tt := range tests {
		t.Run(tt.weekday, func(t *testing.T) {
			// Verify test data is self-consistent with Go's time.Weekday
			expectedFromGo := weekdaysCN[tt.date.Weekday()]
			if expectedFromGo != tt.weekday {
				t.Fatalf("bad test data: Go says %s, test expects %s", expectedFromGo, tt.weekday)
			}

			block := DateTimeBlock(tt.date)
			if !strings.Contains(block.Content, tt.weekday) {
				t.Errorf("DateTimeBlock(%s).Content = %q, want it to contain %q",
					tt.date.Format("2006-01-02 Monday"), block.Content, tt.weekday)
			}
		})
	}
}

func TestDateTimeBlock_WeekdayIndexMapping(t *testing.T) {
	// Verify weekdaysCN array maps correctly: Sunday=0, Monday=1, ..., Saturday=6.
	expected := [...]string{"星期日", "星期一", "星期二", "星期三", "星期四", "星期五", "星期六"}
	if weekdaysCN != expected {
		t.Errorf("weekdaysCN = %v, want %v", weekdaysCN, expected)
	}

	// Cross-check with time.Weekday for every possible value.
	for d := time.Sunday; d <= time.Saturday; d++ {
		cn := weekdaysCN[d]
		if cn == "" {
			t.Errorf("weekdaysCN[%v] is empty", d)
		}
		if !strings.HasPrefix(cn, "星期") {
			t.Errorf("weekdaysCN[%v] = %q, should start with 星期", d, cn)
		}
	}
}

func TestDateTimeBlock_EdgeCases(t *testing.T) {
	// Midnight — should still produce correct format
	tm := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	block := DateTimeBlock(tm)
	if !strings.Contains(block.Content, "00:00") {
		t.Errorf("midnight should show 00:00: %q", block.Content)
	}

	// 2026-12-31 is a Thursday
	if !strings.Contains(block.Content, "星期四") {
		t.Errorf("2026-12-31 should be 星期四: %q", block.Content)
	}

	// Leap year day — 2028-02-29 is a Tuesday
	tm = time.Date(2028, 2, 29, 12, 0, 0, 0, time.UTC)
	block = DateTimeBlock(tm)
	if !strings.Contains(block.Content, "星期二") {
		t.Errorf("2028-02-29 should be 星期二: %q", block.Content)
	}
	if !strings.Contains(block.Content, "2028年2月29日") {
		t.Errorf("leap day date missing: %q", block.Content)
	}

	// Single-digit month/day should NOT have leading zeros (Go's Format uses "1月2日")
	tm = time.Date(2026, 3, 5, 9, 5, 0, 0, time.UTC)
	block = DateTimeBlock(tm)
	if strings.Contains(block.Content, "03月") {
		t.Errorf("month should not have leading zero: %q", block.Content)
	}
	if strings.Contains(block.Content, "05日") {
		t.Errorf("day should not have leading zero: %q", block.Content)
	}
	expected := "2026年3月5日 星期四 09:05 UTC"
	if block.Content != expected {
		t.Errorf("Content = %q, want %q", block.Content, expected)
	}
}
