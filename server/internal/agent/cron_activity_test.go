package agent

import (
	"context"
	"testing"
)

// fakeActivity 记录调用次数，用来验证 cron 接线。
type fakeActivity struct {
	recompute   int
	cleanup     int
	cleanupDays int
}

func (f *fakeActivity) Recompute() error { f.recompute++; return nil }
func (f *fakeActivity) CleanupExpired(days int) (int64, error) {
	f.cleanup++
	f.cleanupDays = days
	return 0, nil
}

// TestRecomputeActivity_RecomputesAndCleans 回归：每个节拍都无条件全量重算 + 清理。
// （旧实现按「版本变了才重算」闸门，版本稳定后派生层永不产出。）
func TestRecomputeActivity_RecomputesAndCleans(t *testing.T) {
	f := &fakeActivity{}
	cr := &CronRunner{activity: f}
	cr.recomputeActivity(context.Background())

	if f.recompute != 1 {
		t.Errorf("recompute = %d, want 1（每节拍都要重算）", f.recompute)
	}
	if f.cleanup != 1 {
		t.Fatalf("cleanup = %d, want 1", f.cleanup)
	}
	if f.cleanupDays != 90 {
		t.Errorf("cleanupDays = %d, want 90", f.cleanupDays)
	}
}

// TestRecomputeActivity_Disabled 未启用（nil）时两者都不跑，不 panic。
func TestRecomputeActivity_Disabled(t *testing.T) {
	cr := &CronRunner{}
	cr.recomputeActivity(context.Background())
}
