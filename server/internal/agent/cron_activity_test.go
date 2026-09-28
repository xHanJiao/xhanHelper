package agent

import (
	"context"
	"testing"
)

// fakeActivity 记录调用次数，用来验证 cron 接线。
type fakeActivity struct {
	need        bool
	recompute   int
	cleanup     int
	cleanupDays int
}

func (f *fakeActivity) Recompute() error { f.recompute++; return nil }
func (f *fakeActivity) NeedsRecompute() (bool, error) {
	return f.need, nil
}
func (f *fakeActivity) CleanupExpired(days int) (int64, error) {
	f.cleanup++
	f.cleanupDays = days
	return 0, nil
}

// TestRecomputeActivity_CleanupRunsWithoutRecompute 回归：即使版本未变（NeedsRecompute=false，
// Recompute 早退），每日节拍仍必须执行清理——否则 90 天保留形同虚设。
func TestRecomputeActivity_CleanupRunsWithoutRecompute(t *testing.T) {
	f := &fakeActivity{need: false}
	cr := &CronRunner{activity: f}
	cr.recomputeActivity(context.Background())

	if f.recompute != 0 {
		t.Errorf("recompute = %d, want 0（版本未变不该重算）", f.recompute)
	}
	if f.cleanup != 1 {
		t.Fatalf("cleanup = %d, want 1（清理必须独立于重算判定）", f.cleanup)
	}
	if f.cleanupDays != 90 {
		t.Errorf("cleanupDays = %d, want 90", f.cleanupDays)
	}
}

// TestRecomputeActivity_CleanupRunsWithRecompute 版本变了：重算与清理都跑。
func TestRecomputeActivity_CleanupRunsWithRecompute(t *testing.T) {
	f := &fakeActivity{need: true}
	cr := &CronRunner{activity: f}
	cr.recomputeActivity(context.Background())

	if f.recompute != 1 || f.cleanup != 1 {
		t.Errorf("recompute=%d cleanup=%d, want 1/1", f.recompute, f.cleanup)
	}
}

// TestRecomputeActivity_Disabled 未启用（nil）时两者都不跑，不 panic。
func TestRecomputeActivity_Disabled(t *testing.T) {
	cr := &CronRunner{}
	cr.recomputeActivity(context.Background())
}
