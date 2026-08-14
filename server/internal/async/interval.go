package async

import (
	"context"
	"log"
	"sync/atomic"
	"time"
)

// IntervalRunner — 周期任务调度器：ticker 驱动 + single-flight 防重叠
// （上一轮未完成则跳过本轮）。fn 内部自行处理错误与超时。
type IntervalRunner struct {
	name           string
	interval       time.Duration
	fn             func(ctx context.Context)
	runImmediately bool
	running        atomic.Bool
}

// NewIntervalRunner 创建周期任务。fn 为 nil 时 panic（接线 bug 炸在明处）。
func NewIntervalRunner(name string, interval time.Duration, fn func(ctx context.Context)) *IntervalRunner {
	if interval <= 0 {
		interval = time.Hour
	}
	if fn == nil {
		panic("[async] interval runner fn is nil")
	}
	return &IntervalRunner{name: name, interval: interval, fn: fn}
}

// RunImmediately 设置启动后先执行一轮（用于启动时捞回停机期间积压的旧数据）。
func (r *IntervalRunner) RunImmediately() *IntervalRunner {
	r.runImmediately = true
	return r
}

// Start 阻塞运行（对齐 CronRunner 风格，用 goroutine 调用）；ctx 取消后返回。
func (r *IntervalRunner) Start(ctx context.Context) {
	if r.runImmediately {
		r.runOnce(ctx)
	}
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.runOnce(ctx)
		}
	}
}

func (r *IntervalRunner) runOnce(ctx context.Context) {
	if !r.running.CompareAndSwap(false, true) {
		log.Printf("[async] %s skip: previous run still in progress", r.name)
		return
	}
	defer r.running.Store(false)
	log.Printf("[async] %s run start", r.name)
	r.fn(ctx)
	log.Printf("[async] %s run done", r.name)
}
