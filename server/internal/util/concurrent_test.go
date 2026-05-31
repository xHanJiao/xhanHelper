package util

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// —— 基础功能 ——

func TestLoadConcurrent_AllSucceed(t *testing.T) {
	tasks := []func(ctx context.Context) (int, error){
		func(ctx context.Context) (int, error) { return 1, nil },
		func(ctx context.Context) (int, error) { return 2, nil },
		func(ctx context.Context) (int, error) { return 3, nil },
	}

	results, err := LoadConcurrent(context.Background(), 2, tasks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	want := []int{1, 2, 3}
	for i := range want {
		if results[i] != want[i] {
			t.Errorf("results[%d] = %d, want %d", i, results[i], want[i])
		}
	}
}

func TestLoadConcurrent_EmptyTasks(t *testing.T) {
	results, err := LoadConcurrent[int](context.Background(), 5, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if results != nil {
		t.Fatalf("expected nil results, got %v", results)
	}
}

func TestLoadConcurrent_SingleTask(t *testing.T) {
	tasks := []func(ctx context.Context) (string, error){
		func(ctx context.Context) (string, error) { return "only", nil },
	}
	results, err := LoadConcurrent(context.Background(), 5, tasks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 || results[0] != "only" {
		t.Fatalf("got %v, want [\"only\"]", results)
	}
}

// —— 结果顺序：提交顺序，非完成顺序 ——

func TestLoadConcurrent_ResultsInSubmissionOrder(t *testing.T) {
	// idx=0 睡得最久，idx=4 最快——完成顺序与提交顺序相反
	tasks := make([]func(ctx context.Context) (int, error), 5)
	for i := 0; i < 5; i++ {
		idx := i
		tasks[i] = func(ctx context.Context) (int, error) {
			time.Sleep(time.Duration(5-idx) * 10 * time.Millisecond)
			return idx, nil
		}
	}

	results, err := LoadConcurrent(context.Background(), 5, tasks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for i, want := range []int{0, 1, 2, 3, 4} {
		if results[i] != want {
			t.Errorf("results[%d] = %d, want %d (must preserve submission order)", i, results[i], want)
		}
	}
}

// —— 并发度限制 ——

func TestLoadConcurrent_MaxConcurrencyEnforced(t *testing.T) {
	var current atomic.Int32
	var maxObserved atomic.Int32

	tasks := make([]func(ctx context.Context) (int, error), 30)
	for i := 0; i < 30; i++ {
		tasks[i] = func(ctx context.Context) (int, error) {
			cur := current.Add(1)
			defer current.Add(-1)

			for {
				m := maxObserved.Load()
				if cur <= m || maxObserved.CompareAndSwap(m, cur) {
					break
				}
			}

			time.Sleep(3 * time.Millisecond)
			return 0, nil
		}
	}

	_, err := LoadConcurrent(context.Background(), 4, tasks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if maxObserved.Load() > 4 {
		t.Errorf("max observed concurrency = %d, want <= 4", maxObserved.Load())
	}
	t.Logf("max observed concurrency: %d", maxObserved.Load())
}

func TestLoadConcurrent_ZeroMaxConcurrencyDefaultsToOne(t *testing.T) {
	var current atomic.Int32
	var maxObserved atomic.Int32

	tasks := make([]func(ctx context.Context) (int, error), 5)
	for i := 0; i < 5; i++ {
		tasks[i] = func(ctx context.Context) (int, error) {
			cur := current.Add(1)
			defer current.Add(-1)
			for {
				m := maxObserved.Load()
				if cur <= m || maxObserved.CompareAndSwap(m, cur) {
					break
				}
			}
			time.Sleep(5 * time.Millisecond)
			return 0, nil
		}
	}

	_, err := LoadConcurrent(context.Background(), 0, tasks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if maxObserved.Load() > 1 {
		t.Errorf("expected sequential execution (concurrency=1), got max=%d", maxObserved.Load())
	}
}

// —— 错误处理 ——

func TestLoadConcurrent_FirstErrorCancelsRemaining(t *testing.T) {
	errSentinel := errors.New("task-0-boom")
	var cancelledViaCtx atomic.Int32
	var ranAfterError atomic.Int32

	tasks := make([]func(ctx context.Context) (int, error), 10)
	tasks[0] = func(ctx context.Context) (int, error) {
		return 0, errSentinel
	}
	for i := 1; i < 10; i++ {
		tasks[i] = func(ctx context.Context) (int, error) {
			select {
			case <-ctx.Done():
				cancelledViaCtx.Add(1)
				return 0, ctx.Err()
			case <-time.After(5 * time.Second):
				ranAfterError.Add(1)
				return i, nil
			}
		}
	}

	results, err := LoadConcurrent(context.Background(), 3, tasks)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, errSentinel) {
		t.Errorf("expected errSentinel, got: %v", err)
	}
	if results != nil {
		t.Errorf("expected nil results on error, got %v", results)
	}

	time.Sleep(50 * time.Millisecond)
	t.Logf("cancelled via ctx: %d, ran after error: %d", cancelledViaCtx.Load(), ranAfterError.Load())
}

func TestLoadConcurrent_ErrorReturnsNilResults(t *testing.T) {
	tasks := []func(ctx context.Context) (int, error){
		func(ctx context.Context) (int, error) { return 0, errors.New("fail") },
	}
	results, err := LoadConcurrent(context.Background(), 1, tasks)
	if err == nil {
		t.Fatal("expected error")
	}
	if results != nil {
		t.Errorf("expected nil results on error, got %v", results)
	}
}

// —— panic 恢复 ——

func TestLoadConcurrent_PanicRecovery(t *testing.T) {
	tasks := []func(ctx context.Context) (int, error){
		func(ctx context.Context) (int, error) { return 1, nil },
		func(ctx context.Context) (int, error) {
			panic("unexpected kaboom")
		},
		func(ctx context.Context) (int, error) { return 3, nil },
	}

	results, err := LoadConcurrent(context.Background(), 2, tasks)
	if err == nil {
		t.Fatal("expected error from panic")
	}
	if !strings.Contains(err.Error(), "panic") {
		t.Errorf("error should contain 'panic', got: %v", err)
	}
	if results != nil {
		t.Errorf("expected nil results on panic, got %v", results)
	}
}

func TestLoadConcurrent_PanicDoesNotLeakSemaphore(t *testing.T) {
	// 反复触发 panic，确保信号量被正确释放
	for i := 0; i < 20; i++ {
		tasks := []func(ctx context.Context) (int, error){
			func(ctx context.Context) (int, error) { panic("boom") },
		}
		_, _ = LoadConcurrent(context.Background(), 2, tasks)
	}

	// 泄漏的话这里会永远阻塞
	tasks := []func(ctx context.Context) (int, error){
		func(ctx context.Context) (int, error) { return 99, nil },
	}
	results, err := LoadConcurrent(context.Background(), 2, tasks)
	if err != nil {
		t.Fatalf("unexpected error after panic recovery: %v", err)
	}
	if results[0] != 99 {
		t.Errorf("got %d, want 99", results[0])
	}
}

// panic 发生时已跑完的 task 结果不被采纳（error 优先级 > 已完成结果）
func TestLoadConcurrent_PanicBeforeAcquiringSemaphore(t *testing.T) {
	// task 在获取信号量前可能 panic（实际中少见，但 recover 应兜住）
	// 这里用立即 panic 模拟
	tasks := []func(ctx context.Context) (int, error){
		func(ctx context.Context) (int, error) { panic("instant") },
	}
	_, err := LoadConcurrent(context.Background(), 1, tasks)
	if err == nil {
		t.Fatal("expected error from immediate panic")
	}
}

// —— context 控制 ——

func TestLoadConcurrent_ParentCtxAlreadyCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var executed atomic.Int32
	tasks := []func(ctx context.Context) (int, error){
		func(ctx context.Context) (int, error) {
			executed.Add(1)
			return 1, nil
		},
		func(ctx context.Context) (int, error) {
			executed.Add(1)
			return 2, nil
		},
	}

	results, err := LoadConcurrent(ctx, 3, tasks)
	if err == nil {
		t.Fatal("expected cancelled context error")
	}
	if results != nil {
		t.Errorf("expected nil results, got %v", results)
	}
	t.Logf("tasks executed: %d (should be 0 or 1 due to race with cancel)", executed.Load())
}

func TestLoadConcurrent_TimeoutViaContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	tasks := make([]func(ctx context.Context) (string, error), 10)
	for i := 0; i < 10; i++ {
		idx := i
		tasks[i] = func(ctx context.Context) (string, error) {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(5 * time.Second):
				return fmt.Sprintf("task-%d", idx), nil
			}
		}
	}

	results, err := LoadConcurrent(ctx, 2, tasks)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if results != nil {
		t.Errorf("expected nil results, got %v", results)
	}
}

// —— task 检查传入的 ctx ——

func TestLoadConcurrent_TaskReceivesInternalCtx(t *testing.T) {
	// task 收到的 ctx 应是 LoadConcurrent 内部包装后的 ctx，
	// 当其他 task 报错时它应该被取消
	errSentinel := errors.New("trigger-cancel")
	var innerCtx context.Context
	var started, cancelled atomic.Bool

	tasks := []func(ctx context.Context) (int, error){
		func(ctx context.Context) (int, error) {
			// 给 task 1 留时间拿到 sem 并进入 fn
			time.Sleep(20 * time.Millisecond)
			return 0, errSentinel
		},
		func(ctx context.Context) (int, error) {
			innerCtx = ctx
			started.Store(true)
			<-ctx.Done()
			cancelled.Store(true)
			return 0, ctx.Err()
		},
	}

	_, _ = LoadConcurrent(context.Background(), 5, tasks)

	if innerCtx == nil {
		t.Fatal("task did not receive ctx")
	}
	if err := innerCtx.Err(); err == nil {
		t.Errorf("inner ctx should be cancelled after sibling task error, but is not")
	}
	t.Logf("started=%v cancelled=%v", started.Load(), cancelled.Load())
}

// —— 并发安全 ——

func TestLoadConcurrent_NoDataRace(t *testing.T) {
	// 多 task 并发写外部结构（由调用方负责正确性），
	// 但 LoadConcurrent 内部不应有竞态
	var counter atomic.Int64
	tasks := make([]func(ctx context.Context) (int64, error), 100)
	for i := 0; i < 100; i++ {
		tasks[i] = func(ctx context.Context) (int64, error) {
			return counter.Add(1), nil
		}
	}

	results, err := LoadConcurrent(context.Background(), 8, tasks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 100 {
		t.Fatalf("expected 100 results, got %d", len(results))
	}

	// 结果应该无重复（counter 原子递增）
	seen := make(map[int64]bool, 100)
	for _, v := range results {
		if seen[v] {
			t.Errorf("duplicate value %d", v)
		}
		seen[v] = true
	}
}

// —— 并发度 > 任务数 ——

func TestLoadConcurrent_ConcurrencyHigherThanTaskCount(t *testing.T) {
	tasks := []func(ctx context.Context) (int, error){
		func(ctx context.Context) (int, error) { return 10, nil },
		func(ctx context.Context) (int, error) { return 20, nil },
	}

	results, err := LoadConcurrent(context.Background(), 100, tasks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 || results[0] != 10 || results[1] != 20 {
		t.Errorf("got %v, want [10, 20]", results)
	}
}

// —— goroutine 泄漏 ——

func TestLoadConcurrent_NoGoroutineLeak(t *testing.T) {
	before := runtime.NumGoroutine()

	for round := 0; round < 30; round++ {
		tasks := make([]func(ctx context.Context) (int, error), 10)
		for i := 0; i < 10; i++ {
			idx := i
			tasks[i] = func(ctx context.Context) (int, error) {
				time.Sleep(time.Millisecond)
				return idx, nil
			}
		}
		_, _ = LoadConcurrent(context.Background(), 3, tasks)
	}

	time.Sleep(100 * time.Millisecond)

	after := runtime.NumGoroutine()
	// 允许 5 个 goroutine 波动（runtime 自身也有后台 goroutine）
	if after > before+5 {
		t.Errorf("possible goroutine leak: before=%d, after=%d", before, after)
	}
	t.Logf("goroutines: before=%d, after=%d", before, after)
}

// —— 返回后所有 goroutine 已退出 ——

func TestLoadConcurrent_AllGoroutinesDoneBeforeReturn(t *testing.T) {
	var inflight atomic.Int32

	// task-0 立刻报错，task-1/2 正在跑
	tasks := []func(ctx context.Context) (int, error){
		func(ctx context.Context) (int, error) {
			return 0, errors.New("fail")
		},
		func(ctx context.Context) (int, error) {
			inflight.Add(1)
			defer inflight.Add(-1)
			time.Sleep(50 * time.Millisecond)
			return 1, nil
		},
		func(ctx context.Context) (int, error) {
			inflight.Add(1)
			defer inflight.Add(-1)
			<-ctx.Done()
			return 0, ctx.Err()
		},
	}

	_, _ = LoadConcurrent(context.Background(), 3, tasks)

	// 函数返回后，不应有残留 goroutine
	if c := inflight.Load(); c != 0 {
		t.Errorf("goroutines still running after LoadConcurrent returned: %d", c)
	}
}
