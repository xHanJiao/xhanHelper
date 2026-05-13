package monitor

import (
	"encoding/json"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// ===== Counter =====

// Counter — 带 success rate 的计数器
type Counter struct {
	Calls  int64 `json:"calls"`
	Errors int64 `json:"errors"`
}

func (c *Counter) Inc(err bool) {
	atomic.AddInt64(&c.Calls, 1)
	if err {
		atomic.AddInt64(&c.Errors, 1)
	}
}

func (c *Counter) SuccessRate() float64 {
	calls := atomic.LoadInt64(&c.Calls)
	if calls == 0 {
		return 0
	}
	return float64(calls-atomic.LoadInt64(&c.Errors)) / float64(calls)
}

// ===== Timer =====

// Timer — 平均耗时统计
type Timer struct {
	Count int64 `json:"count"`
	SumMs int64 `json:"sum_ms"`
}

func (t *Timer) Record(ms int64) {
	atomic.AddInt64(&t.Count, 1)
	atomic.AddInt64(&t.SumMs, ms)
}

func (t *Timer) AvgMs() float64 {
	count := atomic.LoadInt64(&t.Count)
	if count == 0 {
		return 0
	}
	return float64(atomic.LoadInt64(&t.SumMs)) / float64(count)
}

// ===== Snapshot =====

// SnapshotFunc — 组件自报统计
type SnapshotFunc func() any

// ===== Monitor =====

// Monitor — 集中持有所有指标
type Monitor struct {
	startedAt time.Time

	// llm_calls: caller → Counter
	llmCalls   map[string]*Counter
	llmCallsMu sync.RWMutex

	// tool_calls: tool → function → Counter
	toolCalls   map[string]map[string]*Counter
	toolCallsMu sync.RWMutex

	// gague
	sseConns atomic.Int64

	// pipeline timers: step → Timer
	pipelineTimers   map[string]*Timer
	pipelineTimersMu sync.RWMutex

	// snapshots
	snapshots   map[string]SnapshotFunc
	snapshotsMu sync.RWMutex
}

// New 创建 Monitor
func New() *Monitor {
	return &Monitor{
		startedAt:      time.Now(),
		llmCalls:       make(map[string]*Counter),
		toolCalls:      make(map[string]map[string]*Counter),
		pipelineTimers: make(map[string]*Timer),
		snapshots:      make(map[string]SnapshotFunc),
	}
}

// ===== LLM Counter =====

func (m *Monitor) IncLLMCall(caller string, err bool) {
	m.llmCallsMu.RLock()
	c, ok := m.llmCalls[caller]
	m.llmCallsMu.RUnlock()
	if ok {
		c.Inc(err)
		return
	}
	m.llmCallsMu.Lock()
	if c, ok = m.llmCalls[caller]; ok {
		m.llmCallsMu.Unlock()
		c.Inc(err)
		return
	}
	c = &Counter{}
	c.Inc(err)
	m.llmCalls[caller] = c
	m.llmCallsMu.Unlock()
}

// ===== Tool Counter =====

func (m *Monitor) IncToolCall(tool, function string, err bool) {
	m.toolCallsMu.RLock()
	fm, ok := m.toolCalls[tool]
	m.toolCallsMu.RUnlock()
	if !ok {
		m.toolCallsMu.Lock()
		if fm, ok = m.toolCalls[tool]; !ok {
			fm = make(map[string]*Counter)
			m.toolCalls[tool] = fm
		}
		m.toolCallsMu.Unlock()
	}
	m.toolCallsMu.RLock()
	c, ok := fm[function]
	m.toolCallsMu.RUnlock()
	if ok {
		c.Inc(err)
		return
	}
	m.toolCallsMu.Lock()
	if c, ok = fm[function]; ok {
		m.toolCallsMu.Unlock()
		c.Inc(err)
		return
	}
	c = &Counter{}
	c.Inc(err)
	fm[function] = c
	m.toolCallsMu.Unlock()
}

// ===== SSE Gauge =====

func (m *Monitor) IncSSE() { m.sseConns.Add(1) }
func (m *Monitor) DecSSE() { m.sseConns.Add(-1) }

// ===== Pipeline Timer =====

func (m *Monitor) RecordPipelineStep(stepName string, elapsedMs int64) {
	m.pipelineTimersMu.RLock()
	t, ok := m.pipelineTimers[stepName]
	m.pipelineTimersMu.RUnlock()
	if ok {
		t.Record(elapsedMs)
		return
	}
	m.pipelineTimersMu.Lock()
	if t, ok = m.pipelineTimers[stepName]; ok {
		m.pipelineTimersMu.Unlock()
		t.Record(elapsedMs)
		return
	}
	t = &Timer{}
	t.Record(elapsedMs)
	m.pipelineTimers[stepName] = t
	m.pipelineTimersMu.Unlock()
}

// ===== Snapshot =====

func (m *Monitor) Register(name string, fn SnapshotFunc) {
	m.snapshotsMu.Lock()
	m.snapshots[name] = fn
	m.snapshotsMu.Unlock()
}

// ===== Snapshot =====

// Snapshot 生成健康检查的完整 JSON
func (m *Monitor) Snapshot() map[string]interface{} {
	result := map[string]interface{}{
		"uptime_seconds": int(time.Since(m.startedAt).Seconds()),
		"goroutines":     runtime.NumGoroutine(),
		"memory": map[string]interface{}{
			"heap_alloc_mb": roundMB(memStats().HeapAlloc),
			"os_rss_mb":     getRSSMB(),
		},
		"counters":  m.countersSnapshot(),
		"pipeline":  m.pipelineSnapshot(),
		"snapshots": m.registeredSnapshots(),
	}
	return result
}

func (m *Monitor) countersSnapshot() map[string]interface{} {
	c := map[string]interface{}{}

	m.llmCallsMu.RLock()
	llm := map[string]interface{}{}
	for caller, cnt := range m.llmCalls {
		llm[caller] = map[string]interface{}{
			"calls":        atomic.LoadInt64(&cnt.Calls),
			"errors":       atomic.LoadInt64(&cnt.Errors),
			"success_rate": round(cnt.SuccessRate()),
		}
	}
	m.llmCallsMu.RUnlock()
	c["llm_calls"] = llm

	m.toolCallsMu.RLock()
	tools := map[string]interface{}{}
	for tool, fm := range m.toolCalls {
		funcs := map[string]interface{}{}
		for fn, cnt := range fm {
			funcs[fn] = map[string]interface{}{
				"calls":  atomic.LoadInt64(&cnt.Calls),
				"errors": atomic.LoadInt64(&cnt.Errors),
			}
		}
		tools[tool] = funcs
	}
	m.toolCallsMu.RUnlock()
	c["tool_calls"] = tools
	c["sse_connections"] = m.sseConns.Load()

	return c
}

func (m *Monitor) pipelineSnapshot() map[string]interface{} {
	m.pipelineTimersMu.RLock()
	defer m.pipelineTimersMu.RUnlock()
	p := map[string]interface{}{}
	for step, t := range m.pipelineTimers {
		p[step] = map[string]interface{}{
			"avg_ms": round(t.AvgMs()),
			"count":  atomic.LoadInt64(&t.Count),
		}
	}
	return p
}

func (m *Monitor) registeredSnapshots() map[string]interface{} {
	m.snapshotsMu.RLock()
	defer m.snapshotsMu.RUnlock()
	s := map[string]interface{}{}
	for name, fn := range m.snapshots {
		s[name] = fn()
	}
	return s
}

// ===== JSON =====

func (m *Monitor) SnapshotJSON() ([]byte, error) {
	return json.Marshal(m.Snapshot())
}

// ===== helpers =====

func round(f float64) float64 {
	return float64(int(f*1000)) / 1000
}

var memStats = func() *runtime.MemStats {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return &m
}

func roundMB(bytes uint64) float64 {
	return round(float64(bytes) / 1024 / 1024)
}

// ===== Global =====

var global = New()

// IncLLMCall — 全局快捷方法
func IncLLMCall(caller string, err bool) { global.IncLLMCall(caller, err) }

// IncToolCall — 全局快捷方法
func IncToolCall(tool, function string, err bool) { global.IncToolCall(tool, function, err) }

// IncSSE / DecSSE — 全局快捷方法
func IncSSE() { global.IncSSE() }
func DecSSE() { global.DecSSE() }

// RecordPipelineStep — 全局快捷方法
func RecordPipelineStep(step string, elapsedMs int64) { global.RecordPipelineStep(step, elapsedMs) }

// Register — 全局快捷方法
func Register(name string, fn SnapshotFunc) { global.Register(name, fn) }

// Global 返回全局 Monitor 实例
func Global() *Monitor { return global }
