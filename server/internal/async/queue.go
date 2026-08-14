package async

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// Job — 一个待处理任务。Key 用于去重（同 Key 后到覆盖先到），Payload 为业务数据。
type Job[P any] struct {
	Key     string
	Payload P
}

// Config — 队列配置。零值字段会落到默认值。
type Config[P any] struct {
	FlushInterval      time.Duration // 刷盘间隔，默认 1s
	MaxBytesPerFlush   int64         // 每批字节预算（限频核心），<=0 表示不限
	MaxEntriesPerFlush int           // 每批条数上限，<=0 表示不限
	MaxQueueEntries    int           // 排队条数上限，默认 2048
	MaxQueueBytes      int64         // 排队字节上限，默认 64MB，<=0 表示不限
	MaxPayloadBytes    int64         // 单条 payload 字节上限，超过直接拒绝，<=0 表示不限
	SizeOf             func(P) int   // payload 字节估算，nil 则按 0 计（退化为纯条数限频）
}

func (c *Config[P]) withDefaults() {
	if c.FlushInterval <= 0 {
		c.FlushInterval = time.Second
	}
	if c.MaxQueueEntries <= 0 {
		c.MaxQueueEntries = 2048
	}
	if c.MaxQueueBytes <= 0 {
		c.MaxQueueBytes = 64 << 20
	}
}

// Stats — 队列运行统计快照（供监控看板）。
type Stats struct {
	Submitted       int64 `json:"submitted"`        // 累计提交
	DroppedFull     int64 `json:"dropped_full"`     // 缓冲满丢弃
	DroppedOversize int64 `json:"dropped_oversize"` // 单条超限丢弃
	Processed       int64 `json:"processed"`        // 累计处理条数
	ProcessedBytes  int64 `json:"processed_bytes"`  // 累计处理字节
	ProcessErrors   int64 `json:"process_errors"`   // 处理失败批次数
	Pending         int   `json:"pending"`          // 当前排队条数
	PendingBytes    int64 `json:"pending_bytes"`    // 当前排队字节
}

// Queue — 有界内存队列：固定间隔批量消费 + 字节预算限频 + Key 去重。
// 消费语义 at-most-once：处理器失败不重试不回队，仅计数。
// 缓冲满时丢弃新任务（丢新不阻塞），适合 best-effort 数据。
type Queue[P any] struct {
	cfg       Config[P]
	processor func(ctx context.Context, batch []Job[P]) error

	mu      sync.Mutex
	pending map[string]Job[P] // Key → Job
	order   []string          // Key 提交顺序（FIFO）
	bytes   int64             // 排队字节合计

	stats struct {
		submitted       atomic.Int64
		droppedFull     atomic.Int64
		droppedOversize atomic.Int64
		processed       atomic.Int64
		processedBytes  atomic.Int64
		processErrors   atomic.Int64
	}
	stopped atomic.Bool
}

// NewQueue 创建队列。processor 为 nil 时 panic（接线 bug 炸在明处）。
func NewQueue[P any](cfg Config[P], processor func(ctx context.Context, batch []Job[P]) error) *Queue[P] {
	cfg.withDefaults()
	if processor == nil {
		panic("[async] queue processor is nil")
	}
	return &Queue[P]{
		cfg:       cfg,
		processor: processor,
		pending:   make(map[string]Job[P]),
	}
}

// Submit 非阻塞提交。缓冲满/单条超限/已停止时返回 false 并计数丢弃。
func (q *Queue[P]) Submit(key string, payload P) bool {
	if q.stopped.Load() {
		return false
	}
	size := q.payloadSize(payload)
	if q.cfg.MaxPayloadBytes > 0 && int64(size) > q.cfg.MaxPayloadBytes {
		q.stats.droppedOversize.Add(1)
		return false
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	// 同 Key 已排队：覆盖 payload、保持原位置，字节差额修正
	if prev, ok := q.pending[key]; ok {
		q.bytes += int64(size - q.payloadSize(prev.Payload))
		q.pending[key] = Job[P]{Key: key, Payload: payload}
		return true
	}

	if len(q.pending) >= q.cfg.MaxQueueEntries {
		q.stats.droppedFull.Add(1)
		return false
	}
	if q.cfg.MaxQueueBytes > 0 && q.bytes+int64(size) > q.cfg.MaxQueueBytes {
		q.stats.droppedFull.Add(1)
		return false
	}
	q.pending[key] = Job[P]{Key: key, Payload: payload}
	q.order = append(q.order, key)
	q.bytes += int64(size)
	q.stats.submitted.Add(1)
	return true
}

// Start 阻塞运行刷盘循环（对齐 CronRunner 风格，用 goroutine 调用）。
// ctx 取消后排空剩余任务（不受字节预算限制，整体 5s 超时兜底）。
func (q *Queue[P]) Start(ctx context.Context) {
	ticker := time.NewTicker(q.cfg.FlushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			q.drainWithTimeout()
			q.stopped.Store(true)
			return
		case <-ticker.C:
			q.flushBatch(ctx, q.cfg.MaxBytesPerFlush)
		}
	}
}

// Flush 同步排空当前所有排队任务（测试与显式停机用）。
func (q *Queue[P]) Flush(ctx context.Context) {
	q.drain(ctx)
}

// Stats 返回统计快照。
func (q *Queue[P]) Stats() Stats {
	q.mu.Lock()
	pending, pendingBytes := len(q.pending), q.bytes
	q.mu.Unlock()
	return Stats{
		Submitted:       q.stats.submitted.Load(),
		DroppedFull:     q.stats.droppedFull.Load(),
		DroppedOversize: q.stats.droppedOversize.Load(),
		Processed:       q.stats.processed.Load(),
		ProcessedBytes:  q.stats.processedBytes.Load(),
		ProcessErrors:   q.stats.processErrors.Load(),
		Pending:         pending,
		PendingBytes:    pendingBytes,
	}
}

func (q *Queue[P]) payloadSize(p P) int {
	if q.cfg.SizeOf == nil {
		return 0
	}
	return q.cfg.SizeOf(p)
}

// flushBatch 按字节/条数预算从队头取一批并处理。
func (q *Queue[P]) flushBatch(ctx context.Context, limitBytes int64) {
	batch := q.detachBatch(limitBytes)
	if len(batch) == 0 {
		return
	}
	q.countProcessed(batch)
	if err := q.processor(ctx, batch); err != nil {
		q.stats.processErrors.Add(1)
	}
}

// detachBatch 从队头取出累计字节 <= limitBytes 的一批；至少取 1 条
// （单条超预算也处理，防止饥饿）。
func (q *Queue[P]) detachBatch(limitBytes int64) []Job[P] {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.order) == 0 {
		return nil
	}

	take := 0
	var batchBytes int64
	for ; take < len(q.order); take++ {
		key := q.order[take]
		if q.cfg.MaxEntriesPerFlush > 0 && take >= q.cfg.MaxEntriesPerFlush {
			break
		}
		itemSize := int64(q.payloadSize(q.pending[key].Payload))
		if take > 0 && limitBytes > 0 && batchBytes+itemSize > limitBytes {
			break
		}
		batchBytes += itemSize
	}

	batch := make([]Job[P], 0, take)
	for _, key := range q.order[:take] {
		batch = append(batch, q.pending[key])
		delete(q.pending, key)
	}
	q.order = q.order[take:]
	q.bytes -= batchBytes
	return batch
}

// countProcessed 统计已消费批次的条数与字节。
func (q *Queue[P]) countProcessed(batch []Job[P]) {
	q.stats.processed.Add(int64(len(batch)))
	for _, j := range batch {
		q.stats.processedBytes.Add(int64(q.payloadSize(j.Payload)))
	}
}

// drainWithTimeout 停机排空：不受字节预算限制，整体 5s 超时兜底。
func (q *Queue[P]) drainWithTimeout() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	q.drain(ctx)
}

// drain 排空所有排队任务。ctx 被取消时提前返回，剩余任务丢弃。
func (q *Queue[P]) drain(ctx context.Context) {
	for {
		batch := q.detachBatch(0)
		if len(batch) == 0 {
			return
		}
		q.countProcessed(batch)
		if err := q.processor(ctx, batch); err != nil {
			q.stats.processErrors.Add(1)
		}
		if ctx.Err() != nil {
			return
		}
	}
}
