package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"xhan-helper/internal/config"
)

// ===== Pipeline 拓扑排序测试 =====

func TestPipelineTopologicalSort(t *testing.T) {
	steps := []Step{
		&testStep{name: "A", requires: nil, produces: []string{"a"}},
		&testStep{name: "B", requires: []string{"a"}, produces: []string{"b"}},
		&testStep{name: "C", requires: []string{"b"}, produces: []string{"c"}},
	}
	_, err := NewPipeline(steps...)
	if err != nil {
		t.Fatal(err)
	}
}

func TestPipelineCircularDependency(t *testing.T) {
	steps := []Step{
		&testStep{name: "A", requires: []string{"b"}, produces: []string{"a"}},
		&testStep{name: "B", requires: []string{"a"}, produces: []string{"b"}},
	}
	_, err := NewPipeline(steps...)
	if err == nil {
		t.Error("expected error for circular dependency")
	}
}

func TestPipelineDuplicateStep(t *testing.T) {
	steps := []Step{
		&testStep{name: "A", produces: []string{"a"}},
		&testStep{name: "A", produces: []string{"b"}},
	}
	_, err := NewPipeline(steps...)
	if err == nil {
		t.Error("expected error for duplicate step name")
	}
}

func TestPipelineSelfLoop(t *testing.T) {
	steps := []Step{
		&testStep{name: "A", requires: []string{"a"}, produces: []string{"a"}},
	}
	_, err := NewPipeline(steps...)
	if err == nil {
		t.Error("expected error for self-loop")
	}
}

func TestPipelineDuplicateProduces(t *testing.T) {
	steps := []Step{
		&testStep{name: "A", produces: []string{"x"}},
		&testStep{name: "B", produces: []string{"x"}},
	}
	_, err := NewPipeline(steps...)
	if err == nil {
		t.Error("expected error for duplicate produces")
	}
}

func TestPipelineRun(t *testing.T) {
	executed := make([]string, 0)
	steps := []Step{
		&execStep{name: "step1", fn: func(ctx context.Context) (context.Context, error) {
			executed = append(executed, "step1")
			GetPreContext(ctx).SkipMemory = true
			return ctx, nil
		}},
		&execStep{name: "step2", fn: func(ctx context.Context) (context.Context, error) {
			executed = append(executed, "step2")
			if !GetPreContext(ctx).SkipMemory {
				t.Error("step2: expected skip_memory=true from step1")
			}
			return ctx, nil
		}},
	}
	p, err := NewPipeline(steps...)
	if err != nil {
		t.Fatalf("NewPipeline error: %v", err)
	}
	pc := &PreContext{}
	ctx := WithPreContext(context.Background(), pc)
	_, err = p.Run(ctx)
	if err != nil {
		t.Fatalf("pipeline run error: %v", err)
	}
	if len(executed) != 2 || executed[0] != "step1" || executed[1] != "step2" {
		t.Errorf("wrong execution order: %v", executed)
	}
	if !pc.SkipMemory {
		t.Error("expected skip_memory to be true")
	}
}

func TestPipelineShouldRunSkip(t *testing.T) {
	executed := make([]string, 0)
	steps := []Step{
		&execStep{name: "alwaysRun", shouldRunFn: func(ctx context.Context) bool { return true },
			fn: func(ctx context.Context) (context.Context, error) {
				executed = append(executed, "alwaysRun")
				return ctx, nil
			}},
		&execStep{name: "neverRun", shouldRunFn: func(ctx context.Context) bool { return false },
			fn: func(ctx context.Context) (context.Context, error) {
				executed = append(executed, "neverRun")
				return ctx, nil
			}},
	}
	p, err := NewPipeline(steps...)
	if err != nil {
		t.Fatalf("NewPipeline error: %v", err)
	}
	_, runErr := p.Run(context.Background())
	if runErr != nil {
		t.Fatalf("pipeline run error: %v", runErr)
	}
	if len(executed) != 1 || executed[0] != "alwaysRun" {
		t.Errorf("expected only alwaysRun, got %v", executed)
	}
}

// ===== ShortInputCheckStep 测试 =====

func TestShortInputCheck_SkipMemory(t *testing.T) {
	step := NewShortInputCheckStep()
	cases := []struct {
		msg        string
		expectSkip bool
	}{
		{"你好", true},
		{"嗯", true},
		{"ok", true},
		{"好的", true},
		{"帮我记账", false},
		{"今天天气怎么样？", false},
		{"How are you?", false},
		{"搜一下最近的新闻", false},
		{"算一下 1+2", false},
		{"查一下余额", false},
		{"这是什么", true},
	}

	for _, c := range cases {
		t.Run(c.msg, func(t *testing.T) {
			pc := &PreContext{RawMsg: c.msg}
			ctx := WithPreContext(context.Background(), pc)
			_, err := step.Run(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if pc.SkipMemory != c.expectSkip {
				t.Errorf("msg=%q: expected skip=%v, got skip=%v", c.msg, c.expectSkip, pc.SkipMemory)
			}
		})
	}
}

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
		{"今天天气真好", "2026-05-13天气真好"},
		{"昨天花了100元", "2026-05-12花了100元"},
		{"明天去超市", "2026-05-14去超市"},
		{"前天买的", "2026-05-11买的"},
		{"后天到货", "2026-05-15到货"},
		{"3天前买的", "2026-05-10买的"},
		{"5天后到", "2026-05-18到"},
		{"1天前", "2026-05-12"},
		{"10天后", "2026-05-23"},
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
	if pc.NormalizedMsg != "2026-05-26早上发的消息" {
		t.Errorf("early morning bug: expected '2026-05-26早上发的消息', got %q", pc.NormalizedMsg)
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
		if pc.NormalizedMsg != "2026-05-26" {
			t.Errorf("at %s: expected '2026-05-26', got %q", now.Format("15:04"), pc.NormalizedMsg)
		}
	}

	// 北京时间早上 8:00 之后 UTC 已进入同一天，理应不会触发截断偏移
	s8 := &NormalizeDatesStep{now: time.Date(2026, 5, 26, 8, 0, 0, 0, config.BJ)}
	pc8 := &PreContext{RawMsg: "今天"}
	ctx8 := WithPreContext(context.Background(), pc8)
	_, _ = s8.Run(ctx8)
	if pc8.NormalizedMsg != "2026-05-26" {
		t.Errorf("at 08:00: expected '2026-05-26', got %q", pc8.NormalizedMsg)
	}
}

// TestNormalizeDatesStep_Weekdays 验证所有「上周X / 这周X / 下周X」组合
func TestNormalizeDatesStep_Weekdays(t *testing.T) {
	// 用 2026-05-13（周三）作为基点
	step := &NormalizeDatesStep{
		now: time.Date(2026, 5, 13, 12, 0, 0, 0, config.BJ),
	}

	// 上周：偏移 -7 天后找最近的周X
	// 上周一 = 2026-05-04, 上周二=05-05, 上周三=05-06, 上周四=04-30, 上周五=05-01, 上周六=05-02, 上周日=05-03
	// 这周：偏移 0，最近的周X
	// 这周一=05-11, 这周二=05-12, 这周三=05-13, 这周四=05-14, 这周五=05-15, 这周六=05-16, 这周日=05-17
	// 下周：偏移 +7 天
	// 下周一=05-18, 下周二=05-19, 下周三=05-20, 下周四=05-21, 下周五=05-22, 下周六=05-23, 下周日=05-24

	cases := []struct {
		input    string
		expected string
	}{
		{"上周一", "2026-05-04"},
		{"上周二", "2026-05-05"},
		{"上周三", "2026-05-06"},
		{"上周四", "2026-05-07"},
		{"上周五", "2026-05-08"},
		{"上周六", "2026-05-09"},
		{"上周日", "2026-05-03"},
		{"上周天", "2026-05-03"}, // "天" = Sunday alias
		{"这周一", "2026-05-11"},
		{"这周二", "2026-05-12"},
		{"这周三", "2026-05-13"},
		{"这周四", "2026-05-14"},
		{"这周五", "2026-05-15"},
		{"这周六", "2026-05-16"},
		{"这周日", "2026-05-10"},
		{"下周一", "2026-05-18"},
		{"下周二", "2026-05-19"},
		{"下周三", "2026-05-20"},
		{"下周四", "2026-05-21"},
		{"下周五", "2026-05-22"},
		{"下周六", "2026-05-23"},
		{"下周日", "2026-05-17"},
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
	if pc.NormalizedMsg != "2026-05-11开会" {
		t.Errorf("expected '2026-05-11开会', got %q", pc.NormalizedMsg)
	}

	// 上周一 = 05-04，下周一 = 05-18
	cases := []struct {
		input    string
		expected string
	}{
		{"上周一", "2026-05-04"},
		{"下周一", "2026-05-18"},
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
	expected := "2026-05-12买了东西，2026-05-14到货，2026-05-15要用"
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

// TestNormalizeDatesStep_NoPartialMatch 验证不完整的日期关键词不被错误替换
func TestNormalizeDatesStep_NoPartialMatch(t *testing.T) {
	step := &NormalizeDatesStep{
		now: time.Date(2026, 5, 13, 10, 0, 0, 0, config.BJ),
	}

	cases := []struct {
		input string
	}{
		{"今天的"}, // 应匹配"今天"
		{"昨天。"},
		{"天气今天好"}, // "今天"在中间也会被替换
	}
	for _, c := range cases {
		t.Run(c.input, func(t *testing.T) {
			pc := &PreContext{RawMsg: c.input}
			ctx := WithPreContext(context.Background(), pc)
			_, _ = step.Run(ctx)
			// 这些都应该发生替换，确认不含原始中文日期词
			for _, word := range []string{"今天", "昨天", "明天", "前天", "后天"} {
				if strings.Contains(pc.NormalizedMsg, word) {
					t.Errorf("input=%q: expected %q to be replaced, got %q", c.input, word, pc.NormalizedMsg)
				}
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
	if pc.NormalizedMsg != "2026-12-31跨年" {
		t.Errorf("23:59: expected '2026-12-31跨年', got %q", pc.NormalizedMsg)
	}

	// 北京时间 00:00，今天应该已经是新的一天
	s2 := &NormalizeDatesStep{
		now: time.Date(2026, 6, 1, 0, 0, 0, 0, config.BJ),
	}
	pc2 := &PreContext{RawMsg: "今天"}
	ctx2 := WithPreContext(context.Background(), pc2)
	_, _ = s2.Run(ctx2)
	if pc2.NormalizedMsg != "2026-06-01" {
		t.Errorf("00:00: expected '2026-06-01', got %q", pc2.NormalizedMsg)
	}

	// 跨月: 5月31日 明天 = 6月1日
	s3 := &NormalizeDatesStep{
		now: time.Date(2026, 5, 31, 10, 0, 0, 0, config.BJ),
	}
	pc3 := &PreContext{RawMsg: "明天"}
	ctx3 := WithPreContext(context.Background(), pc3)
	_, _ = s3.Run(ctx3)
	if pc3.NormalizedMsg != "2026-06-01" {
		t.Errorf("cross-month: expected '2026-06-01', got %q", pc3.NormalizedMsg)
	}

	// 跨年: 12月31日 明天 = 次年1月1日
	s4 := &NormalizeDatesStep{
		now: time.Date(2026, 12, 31, 10, 0, 0, 0, config.BJ),
	}
	pc4 := &PreContext{RawMsg: "明天"}
	ctx4 := WithPreContext(context.Background(), pc4)
	_, _ = s4.Run(ctx4)
	if pc4.NormalizedMsg != "2027-01-01" {
		t.Errorf("cross-year: expected '2027-01-01', got %q", pc4.NormalizedMsg)
	}
}

// ===== 辅助 Step 实现 =====

type testStep struct {
	name     string
	requires []string
	produces []string
}

func (s *testStep) Name() string                                     { return s.name }
func (s *testStep) Requires() []string                               { return s.requires }
func (s *testStep) Produces() []string                               { return s.produces }
func (s *testStep) ShouldRun(ctx context.Context) bool               { return true }
func (s *testStep) Run(ctx context.Context) (context.Context, error) { return ctx, nil }

type execStep struct {
	name        string
	shouldRunFn func(ctx context.Context) bool
	fn          func(ctx context.Context) (context.Context, error)
}

func (s *execStep) Name() string       { return s.name }
func (s *execStep) Requires() []string { return nil }
func (s *execStep) Produces() []string { return nil }
func (s *execStep) ShouldRun(ctx context.Context) bool {
	if s.shouldRunFn != nil {
		return s.shouldRunFn(ctx)
	}
	return true
}
func (s *execStep) Run(ctx context.Context) (context.Context, error) {
	return s.fn(ctx)
}

// ===== 工具函数测试 =====

func TestExtractJSON(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{`{"a": 1}`, `{"a": 1}`},
		{`some text {"agent_id": "x", "confidence": 0.9} more text`, `{"agent_id": "x", "confidence": 0.9}`},
		{`no json here`, ``},
		{`{"key": "value"}`, `{"key": "value"}`},
	}

	for _, c := range cases {
		result := extractJSON(c.input)
		if result != c.expected {
			t.Errorf("input=%q: expected=%q, got=%q", c.input, c.expected, result)
		}
	}
}

func TestTruncateSummary(t *testing.T) {
	if s := truncateSummary("hello", 10); s != "hello" {
		t.Errorf("expected no truncation: %q", s)
	}
	s := truncateSummary("这是一个很长的文本需要被截断", 5)
	if !strings.HasSuffix(s, "...") || len([]rune(s)) > 8 {
		t.Errorf("expected truncated with ...: %q (len=%d)", s, len([]rune(s)))
	}
}
