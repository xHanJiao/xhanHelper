package pipeline

import (
	"context"
	"testing"
)

// ===== ShortInputCheckStep 测试 =====

func TestShortInputCheck_SkipRetrieval(t *testing.T) {
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
			if pc.SkipRetrieval != c.expectSkip {
				t.Errorf("msg=%q: expected skip=%v, got skip=%v", c.msg, c.expectSkip, pc.SkipRetrieval)
			}
		})
	}
}
