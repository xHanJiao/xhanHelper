package agent

import (
	"context"
	"errors"
	"testing"
	"time"
)

// fakeActivity 记录调用次数，用来验证 cron 接线。
type fakeActivity struct {
	recompute   int
	cleanup     int
	cleanupDays int
	due         bool
	dueErr      error
	dueCalls    int
}

func (f *fakeActivity) Recompute() error { f.recompute++; return nil }
func (f *fakeActivity) RecomputeDue(maxAge time.Duration) (bool, error) {
	f.dueCalls++
	return f.due, f.dueErr
}
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

// TestStartActivityCatchUp_SkipsFresh 派生数据没过期时不在启动时重建
// （回归：每次部署重启都全量重建 events.db，制造一次读 IO 尖峰）。
func TestStartActivityCatchUp_SkipsFresh(t *testing.T) {
	f := &fakeActivity{due: false}
	cr := &CronRunner{activity: f}
	cr.startActivityCatchUp(context.Background())
	cr.waitActivity(t)

	if f.dueCalls != 1 {
		t.Errorf("RecomputeDue 调用 %d 次，want 1", f.dueCalls)
	}
	if f.recompute != 0 {
		t.Errorf("未过期却重算了 %d 次", f.recompute)
	}
	// 清理不受重算新鲜度闸门约束：重启节奏 < 24h 时 ticker 永远等不到。
	if f.cleanup != 1 {
		t.Errorf("未过期也应清理 1 次，实际 %d", f.cleanup)
	}
}

// TestStartActivityCatchUp_RunsWhenStale 过期时补算一次。
func TestStartActivityCatchUp_RunsWhenStale(t *testing.T) {
	f := &fakeActivity{due: true}
	cr := &CronRunner{activity: f}
	cr.startActivityCatchUp(context.Background())
	cr.waitActivity(t)

	if f.recompute != 1 {
		t.Errorf("过期应补算 1 次，实际 %d", f.recompute)
	}
	if f.cleanup != 1 {
		t.Errorf("补算应连带清理 1 次，实际 %d", f.cleanup)
	}
}

// TestStartActivityCatchUp_ErrorsSkip 判不出过期与否时不重建，只记日志。
func TestStartActivityCatchUp_ErrorsSkip(t *testing.T) {
	f := &fakeActivity{dueErr: errors.New("db broken")}
	cr := &CronRunner{activity: f}
	cr.startActivityCatchUp(context.Background())
	cr.waitActivity(t)

	if f.recompute != 0 {
		t.Errorf("RecomputeDue 出错时不该重建，实际 %d 次", f.recompute)
	}
}

// waitActivity 等启动补算的 goroutine 跑完（它结束时会把 activityRunning 置回 false）；
// 超时直接失败，别让「goroutine 没跑完」伪装成业务断言失败。
func (cr *CronRunner) waitActivity(t *testing.T) {
	t.Helper()
	for i := 0; i < 200; i++ {
		cr.mu.Lock()
		running := cr.activityRunning
		cr.mu.Unlock()
		if !running {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("启动补算 goroutine 1s 内未结束")
}
