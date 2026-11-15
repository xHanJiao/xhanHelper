package monitor

import (
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// ===== Counter =====

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

// TokenTracker — 累计 token 统计
type TokenTracker struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
}

func (t *TokenTracker) Add(prompt, completion int) {
	atomic.AddInt64(&t.PromptTokens, int64(prompt))
	atomic.AddInt64(&t.CompletionTokens, int64(completion))
}

// ===== LatencyTimer =====

type LatencySample struct {
	Timestamp time.Time `json:"timestamp"`
	Ms        int64     `json:"ms"`
}

// LatencyTimer — 样本存储 + P99/P50/avg
type LatencyTimer struct {
	samples []LatencySample
	maxAge  time.Duration
	mu      sync.RWMutex
}

func NewLatencyTimer(maxAge time.Duration) *LatencyTimer {
	return &LatencyTimer{maxAge: maxAge}
}

func (t *LatencyTimer) Record(ms int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	t.samples = append(t.samples, LatencySample{Timestamp: now, Ms: ms})
	t.evictLocked(now)
}

func (t *LatencyTimer) evictLocked(now time.Time) {
	cutoff := now.Add(-t.maxAge)
	i := 0
	for i < len(t.samples) && t.samples[i].Timestamp.Before(cutoff) {
		i++
	}
	if i > 0 {
		t.samples = append([]LatencySample(nil), t.samples[i:]...)
	}
}

func (t *LatencyTimer) Stats() map[string]interface{} {
	t.mu.RLock()
	defer t.mu.RUnlock()

	count := len(t.samples)
	if count == 0 {
		return map[string]interface{}{
			"avg_ms": 0, "p50_ms": 0, "p99_ms": 0,
			"count": 0, "recent": []int64{},
		}
	}

	var sum int64
	vals := make([]int64, count)
	for i, s := range t.samples {
		sum += s.Ms
		vals[i] = s.Ms
	}
	sort.Slice(vals, func(i, j int) bool { return vals[i] < vals[j] })

	avg := float64(sum) / float64(count)
	p50 := vals[count*50/100]
	p99 := vals[count*99/100]

	// recent: 最近 100 个样本的毫秒值
	n := 100
	if count < n {
		n = count
	}
	recent := make([]int64, n)
	for i := 0; i < n; i++ {
		recent[i] = t.samples[count-n+i].Ms
	}

	totalBytes := count * 24 // time.Time(24 bytes) + int64(8) ≈ 32, 保守取 24+8

	return map[string]interface{}{
		"avg_ms":       round(avg),
		"p50_ms":       p50,
		"p99_ms":       p99,
		"count":        count,
		"recent":       recent,
		"memory_bytes": totalBytes,
	}
}

// ===== SnapshotFunc =====

type SnapshotFunc func() any

// ===== Monitor =====

const defaultSampleMaxAge = 7 * 24 * time.Hour

type Monitor struct {
	startedAt    time.Time
	sampleMaxAge time.Duration

	// counters
	llmCalls   map[string]*Counter
	llmCallsMu sync.RWMutex

	toolCalls   map[string]map[string]*Counter
	toolCallsMu sync.RWMutex

	// latency
	llmLats   map[string]*LatencyTimer
	llmLatsMu sync.RWMutex

	toolLats   map[string]map[string]*LatencyTimer
	toolLatsMu sync.RWMutex

	pipelineLats   map[string]*LatencyTimer
	pipelineLatsMu sync.RWMutex

	embeddingLat   *LatencyTimer
	embeddingLatMu sync.RWMutex // needed for lazily init

	requestLat   *LatencyTimer
	requestLatMu sync.RWMutex // needed for lazily init

	// tokens
	llmTokens   map[string]*TokenTracker
	llmTokensMu sync.RWMutex

	// gauge
	sseConns atomic.Int64

	// snapshots
	snapshots   map[string]SnapshotFunc
	snapshotsMu sync.RWMutex

	// panic tracking
	panicCounts   map[string]*Counter
	panicCountsMu sync.RWMutex
}

func New() *Monitor {
	return NewWithAge(defaultSampleMaxAge)
}

func NewWithAge(maxAge time.Duration) *Monitor {
	return &Monitor{
		startedAt:    time.Now(),
		sampleMaxAge: maxAge,
		llmCalls:     make(map[string]*Counter),
		toolCalls:    make(map[string]map[string]*Counter),
		llmLats:      make(map[string]*LatencyTimer),
		toolLats:     make(map[string]map[string]*LatencyTimer),
		pipelineLats: make(map[string]*LatencyTimer),
		llmTokens:    make(map[string]*TokenTracker),
		snapshots:    make(map[string]SnapshotFunc),
		panicCounts:  make(map[string]*Counter),
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

// ===== Pipeline Latency =====

func (m *Monitor) RecordPipelineStep(stepName string, elapsedMs int64) {
	m.pipelineLatsMu.RLock()
	t, ok := m.pipelineLats[stepName]
	m.pipelineLatsMu.RUnlock()
	if ok {
		t.Record(elapsedMs)
		return
	}
	m.pipelineLatsMu.Lock()
	if t, ok = m.pipelineLats[stepName]; ok {
		m.pipelineLatsMu.Unlock()
		t.Record(elapsedMs)
		return
	}
	t = NewLatencyTimer(m.sampleMaxAge)
	t.Record(elapsedMs)
	m.pipelineLats[stepName] = t
	m.pipelineLatsMu.Unlock()
}

// ===== LLM Latency + Tokens =====

func (m *Monitor) RecordLLMCall(caller string, elapsedMs int64, promptTokens, completionTokens int) {
	m.llmLatsMu.RLock()
	t, ok := m.llmLats[caller]
	m.llmLatsMu.RUnlock()
	if ok {
		t.Record(elapsedMs)
	} else {
		m.llmLatsMu.Lock()
		if t, ok = m.llmLats[caller]; ok {
			m.llmLatsMu.Unlock()
			t.Record(elapsedMs)
		} else {
			t = NewLatencyTimer(m.sampleMaxAge)
			t.Record(elapsedMs)
			m.llmLats[caller] = t
			m.llmLatsMu.Unlock()
		}
	}

	m.llmTokensMu.RLock()
	tk, ok := m.llmTokens[caller]
	m.llmTokensMu.RUnlock()
	if ok {
		tk.Add(promptTokens, completionTokens)
		return
	}
	m.llmTokensMu.Lock()
	if tk, ok = m.llmTokens[caller]; ok {
		m.llmTokensMu.Unlock()
		tk.Add(promptTokens, completionTokens)
		return
	}
	tk = &TokenTracker{}
	tk.Add(promptTokens, completionTokens)
	m.llmTokens[caller] = tk
	m.llmTokensMu.Unlock()
}

// ===== Tool Latency =====

func (m *Monitor) IncToolCallWithLatency(tool, function string, err bool, elapsedMs int64) {
	m.IncToolCall(tool, function, err)
	m.toolLatsMu.RLock()
	fm, ok := m.toolLats[tool]
	m.toolLatsMu.RUnlock()
	if !ok {
		m.toolLatsMu.Lock()
		if fm, ok = m.toolLats[tool]; !ok {
			fm = make(map[string]*LatencyTimer)
			m.toolLats[tool] = fm
		}
		m.toolLatsMu.Unlock()
	}
	m.toolLatsMu.RLock()
	t, ok := fm[function]
	m.toolLatsMu.RUnlock()
	if ok {
		t.Record(elapsedMs)
		return
	}
	m.toolLatsMu.Lock()
	if t, ok = fm[function]; ok {
		m.toolLatsMu.Unlock()
		t.Record(elapsedMs)
		return
	}
	t = NewLatencyTimer(m.sampleMaxAge)
	t.Record(elapsedMs)
	fm[function] = t
	m.toolLatsMu.Unlock()
}

// ===== Embedding Latency =====

func (m *Monitor) RecordEmbedding(elapsedMs int64) {
	m.embeddingLatMu.Lock()
	if m.embeddingLat == nil {
		m.embeddingLat = NewLatencyTimer(m.sampleMaxAge)
	}
	m.embeddingLatMu.Unlock()
	m.embeddingLat.Record(elapsedMs)
}

// ===== Request Latency =====
// 端到端请求耗时：从 runReActLoop 开始到 done 事件发出（涵盖检索 + 多轮 ReAct + 工具执行 + 检验）

func (m *Monitor) RecordRequestLatency(elapsedMs int64) {
	m.requestLatMu.Lock()
	if m.requestLat == nil {
		m.requestLat = NewLatencyTimer(m.sampleMaxAge)
	}
	m.requestLatMu.Unlock()
	m.requestLat.Record(elapsedMs)
}

// ===== Snapshot =====

func (m *Monitor) RecordPanic(location string) {
	m.panicCountsMu.RLock()
	c, ok := m.panicCounts[location]
	m.panicCountsMu.RUnlock()
	if ok {
		c.Inc(true)
		return
	}
	m.panicCountsMu.Lock()
	if c, ok = m.panicCounts[location]; ok {
		m.panicCountsMu.Unlock()
		c.Inc(true)
		return
	}
	c = &Counter{}
	c.Inc(true)
	m.panicCounts[location] = c
	m.panicCountsMu.Unlock()
}

func (m *Monitor) Register(name string, fn SnapshotFunc) {
	m.snapshotsMu.Lock()
	m.snapshots[name] = fn
	m.snapshotsMu.Unlock()
}

func (m *Monitor) Snapshot() map[string]interface{} {
	return map[string]interface{}{
		"uptime_seconds": int(time.Since(m.startedAt).Seconds()),
		"goroutines":     runtime.NumGoroutine(),
		"memory": map[string]interface{}{
			"heap_alloc_mb": roundMB(memStats().HeapAlloc),
			"os_rss_mb":     getRSSMB(),
		},
		"counters":  m.countersSnapshot(),
		"latency":   m.latencySnapshot(),
		"snapshots": m.registeredSnapshots(),
	}
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

	m.llmTokensMu.RLock()
	tokens := map[string]interface{}{}
	for caller, tk := range m.llmTokens {
		tokens[caller] = map[string]interface{}{
			"prompt_tokens":     atomic.LoadInt64(&tk.PromptTokens),
			"completion_tokens": atomic.LoadInt64(&tk.CompletionTokens),
		}
	}
	m.llmTokensMu.RUnlock()
	c["llm_tokens"] = tokens

	m.panicCountsMu.RLock()
	panics := map[string]interface{}{}
	for loc, cnt := range m.panicCounts {
		panics[loc] = map[string]interface{}{
			"calls":  atomic.LoadInt64(&cnt.Calls),
			"errors": atomic.LoadInt64(&cnt.Errors),
		}
	}
	m.panicCountsMu.RUnlock()
	c["panic_counts"] = panics

	return c
}

func (m *Monitor) latencySnapshot() map[string]interface{} {
	l := map[string]interface{}{}

	m.llmLatsMu.RLock()
	llm := map[string]interface{}{}
	for caller, t := range m.llmLats {
		llm[caller] = t.Stats()
	}
	m.llmLatsMu.RUnlock()
	l["llm"] = llm

	m.pipelineLatsMu.RLock()
	pipe := map[string]interface{}{}
	for step, t := range m.pipelineLats {
		pipe[step] = t.Stats()
	}
	m.pipelineLatsMu.RUnlock()
	l["pipeline"] = pipe

	m.toolLatsMu.RLock()
	tools := map[string]interface{}{}
	for tool, fm := range m.toolLats {
		funcs := map[string]interface{}{}
		for fn, t := range fm {
			funcs[fn] = t.Stats()
		}
		tools[tool] = funcs
	}
	m.toolLatsMu.RUnlock()
	l["tool"] = tools

	m.embeddingLatMu.RLock()
	if m.embeddingLat != nil {
		l["embedding"] = m.embeddingLat.Stats()
	}
	m.embeddingLatMu.RUnlock()

	m.requestLatMu.RLock()
	if m.requestLat != nil {
		l["request"] = m.requestLat.Stats()
	}
	m.requestLatMu.RUnlock()

	return l
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

func IncLLMCall(caller string, err bool)              { global.IncLLMCall(caller, err) }
func IncToolCall(tool, function string, err bool)     { global.IncToolCall(tool, function, err) }
func IncSSE()                                         { global.IncSSE() }
func DecSSE()                                         { global.DecSSE() }
func RecordPipelineStep(step string, elapsedMs int64) { global.RecordPipelineStep(step, elapsedMs) }
func RecordLLMCall(caller string, elapsedMs int64, pt, ct int) {
	global.RecordLLMCall(caller, elapsedMs, pt, ct)
}
func IncToolCallWithLatency(tool, fn string, err bool, ms int64) {
	global.IncToolCallWithLatency(tool, fn, err, ms)
}
func RecordEmbedding(ms int64)              { global.RecordEmbedding(ms) }
func RecordRequestLatency(ms int64)         { global.RecordRequestLatency(ms) }
func Register(name string, fn SnapshotFunc) { global.Register(name, fn) }
func RecordPanic(location string)           { global.RecordPanic(location) }
func Global() *Monitor                      { return global }
