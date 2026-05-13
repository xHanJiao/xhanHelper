package agent

import (
	"context"
	"strings"
	"testing"
	"time"
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

func TestNormalizeDatesStep(t *testing.T) {
	step := &NormalizeDatesStep{
		now: time.Date(2026, 5, 13, 10, 0, 0, 0, time.Local),
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
		{"上周一开会", "2026-05-04开会"},
		{"这周三聚会", "2026-05-13聚会"},
		{"下周五出差", "2026-05-22出差"},
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

func TestNormalizeDatesStep_NoChange(t *testing.T) {
	step := &NormalizeDatesStep{
		now: time.Date(2026, 5, 13, 10, 0, 0, 0, time.Local),
	}
	msg := "这条消息没有日期"
	pc := &PreContext{RawMsg: msg}
	ctx := WithPreContext(context.Background(), pc)
	_, _ = step.Run(ctx)
	if pc.NormalizedMsg != msg {
		t.Errorf("expected no change for msg without dates: got %q", pc.NormalizedMsg)
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
