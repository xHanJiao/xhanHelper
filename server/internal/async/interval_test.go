package async

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestIntervalRunner_TicksPeriodically(t *testing.T) {
	var calls atomic.Int64
	r := NewIntervalRunner("test", 30*time.Millisecond, func(ctx context.Context) {
		calls.Add(1)
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		r.Start(ctx)
		close(done)
	}()
	time.Sleep(150 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Start 未在取消后退出")
	}

	if n := calls.Load(); n < 2 {
		t.Errorf("应有至少 2 次执行，实际 %d", n)
	}
}

func TestIntervalRunner_SingleFlight(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int64
	r := NewIntervalRunner("test", time.Hour, func(ctx context.Context) {
		calls.Add(1)
		close(started)
		<-release
	})

	go r.runOnce(context.Background())
	<-started
	// 第一轮仍在执行，第二轮应被跳过
	r.runOnce(context.Background())
	if n := calls.Load(); n != 1 {
		t.Errorf("重叠执行应被跳过，实际 %d 次", n)
	}
	close(release)
}

func TestIntervalRunner_RunImmediately(t *testing.T) {
	var calls atomic.Int64
	r := NewIntervalRunner("test", time.Hour, func(ctx context.Context) {
		calls.Add(1)
	}).RunImmediately()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		r.Start(ctx)
		close(done)
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	if n := calls.Load(); n != 1 {
		t.Errorf("RunImmediately 应启动即执行 1 次，实际 %d", n)
	}
}
