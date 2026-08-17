package agent

import (
	"testing"
	"time"
)

func TestBusyPeriodConfig_Adjust(t *testing.T) {
	cfg := &BusyPeriodConfig{
		Periods: []BusyPeriod{
			{9, 0, 12, 0},
			{14, 0, 18, 0},
		},
		Loc: time.UTC, // 测试时用 UTC 避免依赖 config.BJ
	}

	tests := []struct {
		name     string
		input    time.Time
		expected time.Time
	}{
		{"闲时不变", time.Date(2026, 8, 17, 8, 0, 0, 0, time.UTC), time.Date(2026, 8, 17, 8, 0, 0, 0, time.UTC)},
		{"上午忙时边界前", time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC), time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)},
		{"上午忙时内", time.Date(2026, 8, 17, 10, 30, 0, 0, time.UTC), time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)},
		{"中午闲时", time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC), time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)},
		{"下午忙时边界", time.Date(2026, 8, 17, 14, 0, 0, 0, time.UTC), time.Date(2026, 8, 17, 18, 0, 0, 0, time.UTC)},
		{"下午忙时内", time.Date(2026, 8, 17, 16, 45, 0, 0, time.UTC), time.Date(2026, 8, 17, 18, 0, 0, 0, time.UTC)},
		{"晚上闲时", time.Date(2026, 8, 17, 19, 0, 0, 0, time.UTC), time.Date(2026, 8, 17, 19, 0, 0, 0, time.UTC)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cfg.Adjust(tt.input)
			if !got.Equal(tt.expected) {
				t.Errorf("Adjust(%v) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}
