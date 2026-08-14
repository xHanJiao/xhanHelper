package async

import (
	"context"
	"sync"
	"testing"
	"time"
)

type recorder struct {
	mu    sync.Mutex
	keys  []string
	items []string
}

func (r *recorder) record(ctx context.Context, batch []Job[string]) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, j := range batch {
		r.keys = append(r.keys, j.Key)
		r.items = append(r.items, j.Payload)
	}
	return nil
}

func (r *recorder) snapshot() ([]string, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	keys := append([]string(nil), r.keys...)
	items := append([]string(nil), r.items...)
	return keys, items
}

func sizeOf(s string) int { return len(s) }

// —— 基本提交与 FIFO 刷盘 ——

func TestQueue_SubmitAndFlush(t *testing.T) {
	rec := &recorder{}
	q := NewQueue(Config[string]{}, rec.record)

	if !q.Submit("k1", "a") || !q.Submit("k2", "b") || !q.Submit("k3", "c") {
		t.Fatal("Submit 应全部成功")
	}
	q.Flush(context.Background())

	keys, items := rec.snapshot()
	if len(keys) != 3 || keys[0] != "k1" || keys[1] != "k2" || keys[2] != "k3" {
		t.Errorf("FIFO 顺序错误: %v", keys)
	}
	if items[0] != "a" || items[1] != "b" || items[2] != "c" {
		t.Errorf("payload 顺序错误: %v", items)
	}
	s := q.Stats()
	if s.Processed != 3 || s.Pending != 0 {
		t.Errorf("stats 错误: %+v", s)
	}
}

// —— 同 Key 后到覆盖先到，且保持原位置 ——

func TestQueue_ReplaceSameKey(t *testing.T) {
	rec := &recorder{}
	q := NewQueue(Config[string]{SizeOf: sizeOf}, rec.record)

	q.Submit("k1", "a")
	q.Submit("k2", "b")
	q.Submit("k1", "a2") // 覆盖 k1
	q.Flush(context.Background())

	keys, items := rec.snapshot()
	if len(keys) != 2 {
		t.Fatalf("应只有 2 条（去重后），实际 %d: %v", len(keys), keys)
	}
	if keys[0] != "k1" || items[0] != "a2" {
		t.Errorf("k1 应保留原位置且 payload 为最新: %v %v", keys, items)
	}
	if keys[1] != "k2" || items[1] != "b" {
		t.Errorf("k2 应原样保留: %v %v", keys, items)
	}
}

// —— 每批字节预算：超预算的部分留待下批 ——

func TestQueue_ByteBudgetPerFlush(t *testing.T) {
	rec := &recorder{}
	q := NewQueue(Config[string]{MaxBytesPerFlush: 10, SizeOf: sizeOf}, rec.record)

	q.Submit("k1", "012345") // 6
	q.Submit("k2", "abcdef") // 6
	q.Submit("k3", "uvwxyz") // 6
	q.Flush(context.Background())

	keys, _ := rec.snapshot()
	if len(keys) != 3 {
		t.Fatalf("Flush 应排空全部，实际 %d", len(keys))
	}
	// Flush 走 drain（不受预算限制），字节预算只约束 ticker 批次；
	// 这里验证 flushBatch 的拆批逻辑：
	rec2 := &recorder{}
	q2 := NewQueue(Config[string]{MaxBytesPerFlush: 10, SizeOf: sizeOf}, rec2.record)
	q2.Submit("k1", "012345")
	q2.Submit("k2", "abcdef")
	q2.Submit("k3", "uvwxyz")
	q2.flushBatch(context.Background(), 10)
	k1, _ := rec2.snapshot()
	if len(k1) != 1 || k1[0] != "k1" {
		t.Errorf("第一批应只有 k1（6 字节，加 k2 超 10 预算），实际 %v", k1)
	}
	q2.flushBatch(context.Background(), 10)
	k2, _ := rec2.snapshot()
	if len(k2) != 2 || k2[1] != "k2" {
		t.Errorf("第二批应补上 k2，实际 %v", k2)
	}
	q2.flushBatch(context.Background(), 10)
	k3, _ := rec2.snapshot()
	if len(k3) != 3 || k3[2] != "k3" {
		t.Errorf("第三批应补上 k3，实际 %v", k3)
	}
}

// —— 单条超过预算：至少取 1 条，不饥饿 ——

func TestQueue_SingleItemExceedsBudget(t *testing.T) {
	rec := &recorder{}
	q := NewQueue(Config[string]{MaxBytesPerFlush: 3, SizeOf: sizeOf}, rec.record)

	q.Submit("k1", "toolarge") // 8 > 3
	q.Submit("k2", "b")
	q.flushBatch(context.Background(), 3)

	keys, _ := rec.snapshot()
	if len(keys) != 1 || keys[0] != "k1" {
		t.Fatalf("超预算单条也应被处理: %v", keys)
	}
}

// —— 条数上限丢弃新任务 ——

func TestQueue_OverflowDropNew(t *testing.T) {
	rec := &recorder{}
	q := NewQueue(Config[string]{MaxQueueEntries: 2}, rec.record)

	if !q.Submit("k1", "a") || !q.Submit("k2", "b") {
		t.Fatal("前两条应成功")
	}
	if q.Submit("k3", "c") {
		t.Fatal("第三条应被丢弃（满）")
	}
	s := q.Stats()
	if s.DroppedFull != 1 || s.Pending != 2 {
		t.Errorf("stats 错误: %+v", s)
	}
	// 同 Key 覆盖不受条数限制影响
	if !q.Submit("k1", "a2") {
		t.Fatal("同 Key 覆盖应成功")
	}
}

// —— 排队字节上限 ——

func TestQueue_QueueBytesLimit(t *testing.T) {
	rec := &recorder{}
	q := NewQueue(Config[string]{MaxQueueBytes: 10, SizeOf: sizeOf}, rec.record)

	if !q.Submit("k1", "012345") {
		t.Fatal("k1 应成功")
	}
	if q.Submit("k2", "abcdef") {
		t.Fatal("k2 应被丢弃（6+6 > 10）")
	}
	// 覆盖时字节差额修正：k1 换成更小的
	if !q.Submit("k1", "ab") {
		t.Fatal("覆盖 k1 应成功")
	}
	q.Flush(context.Background())
	keys, items := rec.snapshot()
	if len(keys) != 1 || items[0] != "ab" {
		t.Errorf("应只剩 k1=ab: %v %v", keys, items)
	}
}

// —— 单条超限拒绝 ——

func TestQueue_OversizeDrop(t *testing.T) {
	rec := &recorder{}
	q := NewQueue(Config[string]{MaxPayloadBytes: 5, SizeOf: sizeOf}, rec.record)

	if q.Submit("k1", "toolong") {
		t.Fatal("超限应被拒绝")
	}
	s := q.Stats()
	if s.DroppedOversize != 1 {
		t.Errorf("stats 错误: %+v", s)
	}
	if !q.Submit("k2", "ab") {
		t.Fatal("正常大小应成功")
	}
}

// —— Start 取消后排空 ——

func TestQueue_StartDrainsOnCancel(t *testing.T) {
	rec := &recorder{}
	q := NewQueue(Config[string]{FlushInterval: 20 * time.Millisecond}, rec.record)

	q.Submit("k1", "a")
	q.Submit("k2", "b")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		q.Start(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Start 未在取消后退出")
	}

	keys, _ := rec.snapshot()
	if len(keys) != 2 {
		t.Fatalf("取消后应排空剩余任务，实际 %v", keys)
	}
	if q.Submit("k3", "c") {
		t.Fatal("停止后 Submit 应返回 false")
	}
}

// —— 处理器报错只计数不回队 ——

func TestQueue_ProcessorErrorCountsOnce(t *testing.T) {
	attempts := 0
	q := NewQueue(Config[string]{}, func(ctx context.Context, batch []Job[string]) error {
		attempts++
		return context.DeadlineExceeded
	})
	q.Submit("k1", "a")
	q.Flush(context.Background())

	if attempts != 1 {
		t.Errorf("处理器应只调用 1 次，实际 %d", attempts)
	}
	s := q.Stats()
	if s.ProcessErrors != 1 || s.Processed != 1 {
		t.Errorf("stats 错误: %+v", s)
	}
}
