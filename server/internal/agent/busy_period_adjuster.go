package agent

import "time"
import "xhan-helper/internal/config"

// BusyPeriod 表示一个忙时区间，例如 09:00-12:00
type BusyPeriod struct {
	StartHour   int // 9
	StartMinute int // 0
	EndHour     int // 12
	EndMinute   int // 0
}

// BusyPeriodConfig 聚合忙时规则和时区
type BusyPeriodConfig struct {
	Periods []BusyPeriod
	Loc     *time.Location
}

// NewDefaultBusyPeriodConfig 返回默认的北京时区忙时配置
func NewDefaultBusyPeriodConfig() *BusyPeriodConfig {
	return &BusyPeriodConfig{
		Periods: []BusyPeriod{
			{9, 0, 12, 0},  // 09:00-12:00
			{14, 0, 18, 0}, // 14:00-18:00
		},
		Loc: config.BJ,
	}
}

// Adjust 将给定时间调整到非忙时，左闭右开
func (c *BusyPeriodConfig) Adjust(t time.Time) time.Time {
	loc := c.Loc
	if loc == nil {
		loc = t.Location()
	}

	for {
		minute := t.Hour()*60 + t.Minute()
		adjusted := false
		for _, p := range c.Periods {
			start := p.StartHour*60 + p.StartMinute
			end := p.EndHour*60 + p.EndMinute

			if minute >= start && minute < end {
				// 在忙时内，推迟到该忙时结束
				t = time.Date(t.Year(), t.Month(), t.Day(), p.EndHour, p.EndMinute, 0, 0, loc)
				adjusted = true
				break
			}
		}
		if !adjusted {
			return t
		}
	}
}
