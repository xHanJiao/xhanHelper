package agent

import (
	"context"
	"testing"
	"time"
)

// TestCronRunnerStart_DisabledLoops 回归：可选循环关闭时 Start 必须正常跑起来，
// 不能对 nil timer 指针做字段访问（曾导致 select 进入即 panic）。
func TestCronRunnerStart_DisabledLoops(t *testing.T) {
	cr := &CronRunner{
		discoverTimes:    []string{time.Now().Add(-2 * time.Minute).Format("15:04")}, // 下一个触发点在明天，测试窗口内不会触发
		archiveAfterDays: 0,                                                          // 关闭归档
		minActiveDays:    3,
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		cr.Start(ctx)
	}()

	time.Sleep(200 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// Start 正常退出（ctx 取消），无 panic
	case <-time.After(2 * time.Second):
		t.Fatal("CronRunner.Start 未在 ctx 取消后退出")
	}

	status := cr.GetHookStatus()
	if status.ArchiveRemaining != "disabled" {
		t.Fatalf("expected disabled archive hook, got %s", status.ArchiveRemaining)
	}
}
