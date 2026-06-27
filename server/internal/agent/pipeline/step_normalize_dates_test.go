package pipeline

import (
	"context"
	"strings"
	"testing"
	"time"

	"xhan-helper/internal/config"
)

// ===== NormalizeDatesStep 测试 =====

// TestNormalizeDatesStep_Basic 验证所有相对日期替换（非边缘时间，10:00 北京时间）
func TestNormalizeDatesStep_Basic(t *testing.T) {
	step := &NormalizeDatesStep{
		now: time.Date(2026, 5, 13, 10, 0, 0, 0, config.BJ),
	}

	cases := []struct {
		input    string
		expected string
	}{
		{"今天天气真好", "今天（2026-05-13）天气真好"},
		{"昨天花了100元", "昨天（2026-05-12）花了100元"},
		{"明天去超市", "明天（2026-05-14）去超市"},
		{"前天买的", "前天（2026-05-11）买的"},
		{"后天到货", "后天（2026-05-15）到货"},
		{"3天前买的", "3天前（2026-05-10）买的"},
		{"5天后到", "5天后（2026-05-18）到"},
		{"1天前", "1天前（2026-05-12）"},
		{"10天后", "10天后（2026-05-23）"},
	}

	for _, c := range cases {
		t.Run(c.input, func(t *testing.T) {
			pc := &PreContext{RawMsg: c.input}
			ctx := WithPreContext(context.Background(), pc)
			_, err := step.Run(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if pc.NormalizedMsg != c.expected {
				t.Errorf("input=%q: expected=%q, got=%q", c.input, c.expected, pc.NormalizedMsg)
			}
		})
	}
}

// TestNormalizeDatesStep_EarlyMorning 复现原始 bug：北京时间早上 0:00-7:59 时 UTC 还在前一天，
// Truncate 在 UTC 午夜截断会错误地把"今天"识别为前一天。
func TestNormalizeDatesStep_EarlyMorning(t *testing.T) {
	// 模拟用户报告的精确场景: 2026-05-26 07:00 北京时间 → UTC 2026-05-25 23:00
	step := &NormalizeDatesStep{
		now: time.Date(2026, 5, 26, 7, 0, 0, 0, config.BJ),
	}

	pc := &PreContext{RawMsg: "今天早上发的消息"}
	ctx := WithPreContext(context.Background(), pc)
	_, err := step.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if pc.NormalizedMsg != "今天（2026-05-26）早上发的消息" {
		t.Errorf("early morning bug: expected '今天（2026-05-26）早上发的消息', got %q", pc.NormalizedMsg)
	}

	// 补充 edge case: 北京时间凌晨 0 点-7 点各临界值
	earlyTimes := []time.Time{
		time.Date(2026, 5, 26, 0, 0, 0, 0, config.BJ),
		time.Date(2026, 5, 26, 0, 30, 0, 0, config.BJ),
		time.Date(2026, 5, 26, 3, 0, 0, 0, config.BJ),
		time.Date(2026, 5, 26, 7, 59, 0, 0, config.BJ),
	}
	for _, now := range earlyTimes {
		s := &NormalizeDatesStep{now: now}
		pc := &PreContext{RawMsg: "今天"}
		ctx := WithPreContext(context.Background(), pc)
		_, err := s.Run(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if pc.NormalizedMsg != "今天（2026-05-26）" {
			t.Errorf("at %s: expected '今天（2026-05-26）', got %q", now.Format("15:04"), pc.NormalizedMsg)
		}
	}

	// 北京时间早上 8:00 之后 UTC 已进入同一天，理应不会触发截断偏移
	s8 := &NormalizeDatesStep{now: time.Date(2026, 5, 26, 8, 0, 0, 0, config.BJ)}
	pc8 := &PreContext{RawMsg: "今天"}
	ctx8 := WithPreContext(context.Background(), pc8)
	_, _ = s8.Run(ctx8)
	if pc8.NormalizedMsg != "今天（2026-05-26）" {
		t.Errorf("at 08:00: expected '今天（2026-05-26）', got %q", pc8.NormalizedMsg)
	}
}

// TestNormalizeDatesStep_Weekdays 验证所有「上周X / 这周X / 下周X」组合
func TestNormalizeDatesStep_Weekdays(t *testing.T) {
	// 用 2026-05-13（周三）作为基点
	step := &NormalizeDatesStep{
		now: time.Date(2026, 5, 13, 12, 0, 0, 0, config.BJ),
	}

	cases := []struct {
		input    string
		expected string // 原词保留，日期以括号标注
	}{
		{"上周一", "上周一（2026-05-04）"},
		{"上周二", "上周二（2026-05-05）"},
		{"上周三", "上周三（2026-05-06）"},
		{"上周四", "上周四（2026-05-07）"},
		{"上周五", "上周五（2026-05-08）"},
		{"上周六", "上周六（2026-05-09）"},
		{"上周日", "上周日（2026-05-03）"},
		{"上周天", "上周天（2026-05-03）"}, // "天" = Sunday alias
		{"这周一", "这周一（2026-05-11）"},
		{"这周二", "这周二（2026-05-12）"},
		{"这周三", "这周三（2026-05-13）"},
		{"这周四", "这周四（2026-05-14）"},
		{"这周五", "这周五（2026-05-15）"},
		{"这周六", "这周六（2026-05-16）"},
		{"这周日", "这周日（2026-05-10）"},
		{"下周一", "下周一（2026-05-18）"},
		{"下周二", "下周二（2026-05-19）"},
		{"下周三", "下周三（2026-05-20）"},
		{"下周四", "下周四（2026-05-21）"},
		{"下周五", "下周五（2026-05-22）"},
		{"下周六", "下周六（2026-05-23）"},
		{"下周日", "下周日（2026-05-17）"},
	}

	for _, c := range cases {
		t.Run(c.input, func(t *testing.T) {
			pc := &PreContext{RawMsg: "星期测试" + c.input}
			ctx := WithPreContext(context.Background(), pc)
			_, err := step.Run(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(pc.NormalizedMsg, c.expected) {
				t.Errorf("input=%q: expected to contain %q, got %q", c.input, c.expected, pc.NormalizedMsg)
			}
		})
	}
}

// TestNormalizeDatesStep_WeekdayOnTargetDay 验证「今天就是目标周X」这周不会回退到上周
func TestNormalizeDatesStep_WeekdayOnTargetDay(t *testing.T) {
	// 2026-05-11 是周一，这周一应该等于今天
	step := &NormalizeDatesStep{
		now: time.Date(2026, 5, 11, 10, 0, 0, 0, config.BJ),
	}

	pc := &PreContext{RawMsg: "这周一开会"}
	ctx := WithPreContext(context.Background(), pc)
	_, err := step.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if pc.NormalizedMsg != "这周一（2026-05-11）开会" {
		t.Errorf("expected '这周一（2026-05-11）开会', got %q", pc.NormalizedMsg)
	}

	// 上周一 = 05-04，下周一 = 05-18
	cases := []struct {
		input    string
		expected string
	}{
		{"上周一", "上周一（2026-05-04）"},
		{"下周一", "下周一（2026-05-18）"},
	}
	for _, c := range cases {
		t.Run(c.input, func(t *testing.T) {
			pc := &PreContext{RawMsg: c.input}
			ctx := WithPreContext(context.Background(), pc)
			_, _ = step.Run(ctx)
			if pc.NormalizedMsg != c.expected {
				t.Errorf("input=%q: expected=%q, got=%q", c.input, c.expected, pc.NormalizedMsg)
			}
		})
	}
}

// TestNormalizeDatesStep_MultipleInMessage 验证一条消息中多个日期同时替换
func TestNormalizeDatesStep_MultipleInMessage(t *testing.T) {
	step := &NormalizeDatesStep{
		now: time.Date(2026, 5, 13, 10, 0, 0, 0, config.BJ),
	}

	pc := &PreContext{RawMsg: "昨天买了东西，明天到货，这周五要用"}
	ctx := WithPreContext(context.Background(), pc)
	_, err := step.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	expected := "昨天（2026-05-12）买了东西，明天（2026-05-14）到货，这周五（2026-05-15）要用"
	if pc.NormalizedMsg != expected {
		t.Errorf("expected %q, got %q", expected, pc.NormalizedMsg)
	}
}

// TestNormalizeDatesStep_NoChange 验证没有日期关键词的消息原样返回
func TestNormalizeDatesStep_NoChange(t *testing.T) {
	step := &NormalizeDatesStep{
		now: time.Date(2026, 5, 13, 10, 0, 0, 0, config.BJ),
	}

	messages := []string{
		"这条消息没有日期",
		"hello world",
		"帮我记一笔账",
		"", // 空消息
	}
	for _, msg := range messages {
		t.Run(msg, func(t *testing.T) {
			pc := &PreContext{RawMsg: msg}
			ctx := WithPreContext(context.Background(), pc)
			_, _ = step.Run(ctx)
			if pc.NormalizedMsg != msg {
				t.Errorf("expected no change: got %q", pc.NormalizedMsg)
			}
		})
	}
}

// TestNormalizeDatesStep_NoPartialMatch 验证中文日期词都被替换为「原词（日期）」格式
func TestNormalizeDatesStep_NoPartialMatch(t *testing.T) {
	step := &NormalizeDatesStep{
		now: time.Date(2026, 5, 13, 10, 0, 0, 0, config.BJ),
	}

	cases := []struct {
		input    string
		expected string
	}{
		{"今天的", "今天（2026-05-13）的"},
		{"昨天。", "昨天（2026-05-12）。"},
		{"天气今天好", "天气今天（2026-05-13）好"},
	}
	for _, c := range cases {
		t.Run(c.input, func(t *testing.T) {
			pc := &PreContext{RawMsg: c.input}
			ctx := WithPreContext(context.Background(), pc)
			_, _ = step.Run(ctx)
			if pc.NormalizedMsg != c.expected {
				t.Errorf("input=%q: expected=%q, got=%q", c.input, c.expected, pc.NormalizedMsg)
			}
		})
	}
}

// TestNormalizeDatesStep_MidnightBoundary 验证跨天边界时的正确性
func TestNormalizeDatesStep_MidnightBoundary(t *testing.T) {
	// 北京时间 23:59，今天应该仍是当天
	s := &NormalizeDatesStep{
		now: time.Date(2026, 12, 31, 23, 59, 0, 0, config.BJ),
	}
	pc := &PreContext{RawMsg: "今天跨年"}
	ctx := WithPreContext(context.Background(), pc)
	_, _ = s.Run(ctx)
	if pc.NormalizedMsg != "今天（2026-12-31）跨年" {
		t.Errorf("23:59: expected '今天（2026-12-31）跨年', got %q", pc.NormalizedMsg)
	}

	// 北京时间 00:00，今天应该已经是新的一天
	s2 := &NormalizeDatesStep{
		now: time.Date(2026, 6, 1, 0, 0, 0, 0, config.BJ),
	}
	pc2 := &PreContext{RawMsg: "今天"}
	ctx2 := WithPreContext(context.Background(), pc2)
	_, _ = s2.Run(ctx2)
	if pc2.NormalizedMsg != "今天（2026-06-01）" {
		t.Errorf("00:00: expected '今天（2026-06-01）', got %q", pc2.NormalizedMsg)
	}

	// 跨月: 5月31日 明天 = 6月1日
	s3 := &NormalizeDatesStep{
		now: time.Date(2026, 5, 31, 10, 0, 0, 0, config.BJ),
	}
	pc3 := &PreContext{RawMsg: "明天"}
	ctx3 := WithPreContext(context.Background(), pc3)
	_, _ = s3.Run(ctx3)
	if pc3.NormalizedMsg != "明天（2026-06-01）" {
		t.Errorf("cross-month: expected '明天（2026-06-01）', got %q", pc3.NormalizedMsg)
	}

	// 跨年: 12月31日 明天 = 次年1月1日
	s4 := &NormalizeDatesStep{
		now: time.Date(2026, 12, 31, 10, 0, 0, 0, config.BJ),
	}
	pc4 := &PreContext{RawMsg: "明天"}
	ctx4 := WithPreContext(context.Background(), pc4)
	_, _ = s4.Run(ctx4)
	if pc4.NormalizedMsg != "明天（2027-01-01）" {
		t.Errorf("cross-year: expected '明天（2027-01-01）', got %q", pc4.NormalizedMsg)
	}
}
