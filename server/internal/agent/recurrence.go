package agent

import (
	"encoding/json"
	"fmt"
	"time"
)

// RecurrenceRule — 循环规则
type RecurrenceRule struct {
	Type      string        `json:"type"`                 // daily | weekday | weekly | monthly
	Time      string        `json:"time"`                 // "15:04" 格式
	Days      []string      `json:"days,omitempty"`       // weekly: ["monday","wednesday"]
	MonthDays []interface{} `json:"month_days,omitempty"` // monthly: [1,15]
}

var dayToWeekday = map[string]time.Weekday{
	"sunday": time.Sunday, "monday": time.Monday, "tuesday": time.Tuesday,
	"wednesday": time.Wednesday, "thursday": time.Thursday, "friday": time.Friday,
	"saturday": time.Saturday,
}

// ParseRecurrenceRule 解析循环规则 JSON
func ParseRecurrenceRule(raw string) (*RecurrenceRule, error) {
	var r RecurrenceRule
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		return nil, err
	}
	if r.Type == "" {
		return nil, fmt.Errorf("empty type")
	}
	return &r, nil
}

// NextDeadline 计算下一次触发时间
func NextDeadline(rule *RecurrenceRule, from time.Time) (time.Time, error) {
	hour, min := 0, 0
	if rule.Time != "" {
		if _, err := fmt.Sscanf(rule.Time, "%d:%d", &hour, &min); err != nil {
			return time.Time{}, fmt.Errorf("invalid time: %s", rule.Time)
		}
	}
	todayTarget := time.Date(from.Year(), from.Month(), from.Day(), hour, min, 0, 0, from.Location())

	switch rule.Type {
	case "daily":
		return nextDaily(todayTarget, from), nil
	case "weekday":
		return nextWeekday(todayTarget, from), nil
	case "weekly":
		return nextWeekly(rule.Days, todayTarget, from)
	case "monthly":
		return nextMonthly(ParseRecurrenceDays(rule.MonthDays), todayTarget, from)
	default:
		return time.Time{}, fmt.Errorf("unknown recurrence type: %s", rule.Type)
	}
}

func nextDaily(target, from time.Time) time.Time {
	if target.After(from) {
		return target
	}
	return target.AddDate(0, 0, 1)
}

func nextWeekday(target, from time.Time) time.Time {
	t := target
	for i := 0; i < 14; i++ {
		if t.Weekday() != time.Saturday && t.Weekday() != time.Sunday && t.After(from) {
			return t
		}
		t = t.AddDate(0, 0, 1)
	}
	return target
}

func nextWeekly(days []string, target, from time.Time) (time.Time, error) {
	if len(days) == 0 {
		return time.Time{}, fmt.Errorf("weekly rule requires days")
	}
	for i := 0; i < 14; i++ {
		t := target.AddDate(0, 0, i)
		for _, d := range days {
			if wd, ok := dayToWeekday[d]; ok && t.Weekday() == wd && t.After(from) {
				return t, nil
			}
		}
	}
	return time.Time{}, fmt.Errorf("no matching weekday in range")
}

func nextMonthly(days []int, target, from time.Time) (time.Time, error) {
	if len(days) == 0 {
		return time.Time{}, fmt.Errorf("monthly rule requires days")
	}
	y, m := target.Year(), target.Month()
	for i := 0; i < 62; i++ {
		currentMonth := time.Date(y, m, 1, target.Hour(), target.Minute(), 0, 0, from.Location())
		lastDay := currentMonth.AddDate(0, 1, -1).Day()
		for _, d := range days {
			day := d
			if day > lastDay {
				day = lastDay // 月末截断
			}
			t := time.Date(y, m, day, target.Hour(), target.Minute(), 0, 0, from.Location())
			if t.After(from) {
				return t, nil
			}
		}
		m++
		if m > 12 {
			m = 1
			y++
		}
	}
	return time.Time{}, fmt.Errorf("no matching day in range")
}

// ParseRecurrenceDays 将 LLM 产出的 days 数组 ([]interface{}) 转为 int 列表
func ParseRecurrenceDays(raw []interface{}) []int {
	var result []int
	for _, v := range raw {
		switch n := v.(type) {
		case float64:
			result = append(result, int(n))
		case int:
			result = append(result, n)
		}
	}
	return result
}
