# 可观测性 / Monitor 模块设计

> 2026-05-14 | 更新 2026-05-15（P99 + 时序图）

## 概述

为管理员提供 `/api/admin/health` 端点 + Web 管理页面，集中暴露服务运行状态、LLM/工具调用统计、调用耗时、向量存储规模、内存占用等信息。

核心原则：
- 调用方侵入 ≤ 2 行
- 所有逻辑收在 `internal/monitor/`，不散落各处
- 不持久化，重启归零（个人助手场景够用）
- 单端点 `/api/admin/health` 全量返回
- Snapshot 函数签名为 `func() any`

## 目录结构

```
internal/monitor/
├── monitor.go       ← 主体：Counter + LatencyTimer + Gauge + Snapshot 注册表
├── handler.go       ← HTTP handler: GET /api/admin/health
├── middleware.go    ← 管理员鉴权
└── llm_wrapper.go   ← LLMClient 装饰器
```

## 指标清单

### 计数器（Counter）

| 指标 | Labels | 上报点 |
|------|--------|--------|
| `llm_calls` | caller, success | MonitoredLLMClient |
| `tool_calls` | tool, function, success | LoggingWrapper |

### 耗时（LatencyTimer — 样本 + P99/P50/avg）

| 指标 | Labels | 上报点 |
|------|--------|--------|
| `llm_latency` | caller | orchestrator done 事件 |
| `pipeline_latency` | step | pipeline.Run |
| `tool_latency` | tool, function | LoggingWrapper |
| `embedding_latency` | — | embedder.Embed |

### 快照（Gauge / 实时计算）

| 指标 | 来源 |
|------|------|
| `llm_tokens` | orchestrator done 事件（prompt + completion） |
| `sse_connections` | chat handler |
| uptime_seconds | main.go 记录 startTime |
| goroutines | `runtime.NumGoroutine()` |
| heap_alloc_mb | `runtime.ReadMemStats()` |
| os_rss_mb | OS 级 RSS（Linux 读 `/proc/self/status`，macOS 返回 -1） |
| vector_entries | vectorStore.Stats() |
| active_conversations | convRepo.CountActive(ctx, 1h) |
| mcp_servers | mcpManager.Stats() |

## 核心设计

### LatencyTimer — 样本存储

```go
type LatencySample struct {
    Timestamp time.Time
    Ms        int64
}

type LatencyTimer struct {
    samples  []LatencySample  // 环形缓冲
    maxAge   time.Duration    // 默认 7 天
    mu       sync.RWMutex
}

func (t *LatencyTimer) Record(ms int64)
func (t *LatencyTimer) Stats() (avg, p50, p99 float64, count int, recent []int64)
```

- 驱逐：`Record` 时自动丢弃超过 `maxAge` 的样本
- 内存监控：`VectorStore.Stats()` 同样的方式，样本占的字节数也暴露

### 侵入点

**LLM 耗时 + token**（orchestrator done 事件，1 行）：

```go
monitor.RecordLLMCall(caller, elapsedMs, usage.PromptTokens, usage.CompletionTokens)
```

**工具耗时**（LoggingWrapper，复用已有 elapsed）：

```go
monitor.IncToolCall(w.Name(), funcName, !result.Success, elapsed)
```

**Embedding 耗时**（embedder.Embed 内 2 行）：

```go
start := time.Now()
// ... embedding call ...
monitor.RecordEmbedding(time.Since(start).Milliseconds())
```

### API 响应格式（节选）

```json
{
  "llm_latency": {
    "orchestrator": {
      "avg_ms": 3120, "p50_ms": 2800, "p99_ms": 8500,
      "count": 128,
      "recent": [3100, 2900, 3500, ...]
    }
  },
  "llm_tokens": {
    "orchestrator": {"prompt": 54000, "completion": 12500}
  },
  "pipeline_latency": {
    "autoRoute": {"avg_ms": 1520, "p50_ms": 1400, "p99_ms": 3000, "count": 156, "recent": [...]}
  },
  "tool_latency": {
    "hotnews": {
      "invoke": {"avg_ms": 2100, "p50_ms": 2000, "p99_ms": 5000, "count": 5, "recent": [...]}
    }
  }
}
```

## 前端管理页面

### 布局（更新）

```
┌──────────────────────────────────────────────┐
│  运行时间 / goroutines / 内存 / SSE  （卡片行）│
├──────────────────────────────────────────────┤
│  LLM 调用耗时 (时序折线图, 最近 100 点)        │
│  avg / P50 / P99 汇总                        │
├──────────────────────────────────────────────┤
│  管线耗时 (横向条形图)                         │
├──────────────────────────────────────────────┤
│  工具调用耗时 TOP (时序折线图)                  │
├──────────────────────────────────────────────┤
│  LLM 调用次数 (柱状图)                         │
├──────────────────────────────────────────────┤
│  MCP 状态 / 向量存储 / 活跃会话                 │
└──────────────────────────────────────────────┘
```

### 图表类型

| 数据 | 图表 |
|------|------|
| LLM/工具耗时 | 折线图（`recent` 数组，X=时间 Y=ms） |
| 管线耗时 avg | 横向条形图 |
| LLM 调用次数 | 堆叠柱状图（成功/失败） |

## 配置

```yaml
server:
  admin_users: ["uuid-here"]      # 管理员用户 ID

monitor:
  sample_max_age_hours: 168       # 样本保留时长，默认 7 天 = 168 小时
```

## 不确定的项

- **OS RSS 跨平台**：macOS 没有 `/proc/self/status`，返回值填 `-1`。
- **持久化**：现阶段不做。Counter + LatencyTimer 全内存，重启归零。
