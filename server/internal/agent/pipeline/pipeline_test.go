package pipeline

import (
	"context"
	"testing"
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
