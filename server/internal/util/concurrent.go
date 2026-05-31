package util

import (
	"context"
	"fmt"
	"sync"
)

// LoadConcurrent 并发执行 tasks，限制最大并发度为 maxConcurrency。
// 返回按 task 提交顺序排列的结果切片。任一 task 返回 error 时取消其余尚在排队/执行中的 task，返回第一个 error。
// task 闭包接收的 ctx 会在父 ctx 取消或任一 task 报错时终止，task 应检查 ctx.Done() 以提前退出。
func LoadConcurrent[T any](ctx context.Context, maxConcurrency int, tasks []func(ctx context.Context) (T, error)) ([]T, error) {
	if len(tasks) == 0 {
		return nil, nil
	}
	if maxConcurrency <= 0 {
		maxConcurrency = 1
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	type result struct {
		idx int
		val T
		err error
	}

	results := make([]T, len(tasks))
	sem := make(chan struct{}, maxConcurrency)
	resultCh := make(chan result, len(tasks)) // 带缓冲，避免 goroutine 阻塞

	var wg sync.WaitGroup
	wg.Add(len(tasks))

	for i, task := range tasks {
		idx, fn := i, task
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					resultCh <- result{idx: idx, err: fmt.Errorf("panic: %v", r)}
				}
			}()

			// 获取信号量（阻塞直到有并发槽位，或 ctx 取消）
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				resultCh <- result{idx: idx, err: ctx.Err()}
				return
			}

			// select 两个 case 同时就绪时公平选择——抢到 sem 也要 double-check ctx
			if err := ctx.Err(); err != nil {
				<-sem // 释放信号量
				resultCh <- result{idx: idx, err: err}
				return
			}
			defer func() { <-sem }()

			// 执行任务（传入内部 ctx，支持取消感知）
			val, err := fn(ctx)
			resultCh <- result{idx: idx, val: val, err: err}
		}()
	}

	// 单独 goroutine 等待所有 goroutine 完成后再关闭 resultCh
	go func() {
		wg.Wait()
		close(resultCh)
	}()

	// 收集结果
	var firstErr error
	for r := range resultCh {
		if r.err != nil && firstErr == nil {
			firstErr = r.err
			cancel() // 通知其余任务停止
		}
		results[r.idx] = r.val
	}

	if firstErr != nil {
		return nil, firstErr
	}
	return results, nil
}
