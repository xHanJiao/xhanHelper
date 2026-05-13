# 可观测性 / Monitor 模块设计

> 2026-05-14

## 概述

为管理员提供 `/api/admin/health` 端点 + Web 管理页面，集中暴露服务运行状态、LLM/工具调用统计、向量存储规模、内存占用等信息。

核心原则：
- 调用方侵入 ≤ 1 行（计数器）或零侵入（Snapshot 在 main.go 注册、LLM 装饰器包一层）
- 所有逻辑收在 `internal/monitor/`，不散落各处
- 不持久化，重启归零（个人助手场景够用）
- 单端点 `/api/admin/health` 全量返回
- Snapshot 函数签名为 `func() any`，需要 IO 的自己 `context.Background()`

## 目录结构

```
internal/monitor/
├── monitor.go       ← 主体：Counter + Gauge + Snapshot 注册表
├── handler.go       ← HTTP handler: GET /api/admin/health
├── middleware.go    ← 管理员鉴权
└── llm_wrapper.go   ← LLMClient 装饰器
```

## 指标清单

### 计数器（Counter / Gauge）

| 指标 | 类型 | Labels | 上报点 |
|------|------|--------|--------|
| `llm_calls` | Counter | caller, success | MonitoredLLMClient（装饰器） |
| `tool_calls` | Counter | tool, function, success | LoggingWrapper |
| `sse_connections` | Gauge | — | chat handler |
| `pipeline_step_ms` | Counter(avg) | step_name | pipeline.Run |

### 快照（Snapshot — 查询时实时计算，`func() any`）

| 指标 | 来源 |
|------|------|
| uptime_seconds | main.go 记录 startTime |
| goroutines | `runtime.NumGoroutine()` |
| heap_alloc_mb | `runtime.ReadMemStats()` |
| os_rss_mb | OS 级 RSS（macOS 取不到时填 -1） |
| vector_entries | vectorStore.Stats() |
| active_conversations | convRepo.CountActive(context.Background(), 1h) |
| mcp_servers | mcpManager.Stats() |

### 不做（TODO 后续）

| 指标 | 原因 |
|------|------|
| llm_tokens | 需要双倍 channel 复杂度包装流式事件，收益不高 |

## 核心设计

### 侵入极简 — 调用方视角

**LLM 调用**（零侵入，装饰器包一层）：

```go
// main.go — 包一层，调用方无感知
monitoredLLM := monitor.WrapLLMClient(llmClient, "orchestrator")
extractorLLM  := monitor.WrapLLMClient(llmClient, "extractor")
autoRouteLLM  := monitor.WrapLLMClient(llmClient, "autoRoute")
```

成功判定：`Chat()/ChatSync()` 返回 err == nil 即成功（不追踪流式中断）。

**工具调用**（1 行，在已有 LoggingWrapper 里）：

```go
monitor.IncToolCall(w.inner.Name(), funcName, result.Success)
```

**SSE 连接**（2 行，在 chat handler）：

```go
monitor.IncSSE()
defer monitor.DecSSE()
```

**管线耗时**（1 行，在 pipeline.go Run 方法）：

```go
monitor.RecordPipelineStep(s.Name(), elapsed)
```

### 组件自治 — Snapshot 注册

```go
type SnapshotFunc func() any

monitor.Register("vector_store", func() any { return vectorStore.Stats() })
monitor.Register("mcp", func() any { return mcpManager.Stats() })
monitor.Register("active_conversations", func() any { return convRepo.CountActive(context.Background(), time.Hour) })
```

Snapshot 内部自己创建 ctx，不从 Register 捕获。

### API 响应格式

```
GET /api/admin/health
Authorization: Bearer <admin-token>

{
  "uptime_seconds": 3721,
  "goroutines": 42,
  "memory": {
    "heap_alloc_mb": 12.5,
    "os_rss_mb": 96.4
  },
  "counters": {
    "llm_calls": {
      "orchestrator":  {"calls": 120, "errors": 3, "success_rate": 0.975},
      "extractor":     {"calls": 8,   "errors": 0, "success_rate": 1.0},
      "autoRoute":     {"calls": 45,  "errors": 0, "success_rate": 1.0}
    },
    "tool_calls": {
      "bookkeeping": {
        "create": {"calls": 20, "success": 18},
        "query":  {"calls": 15, "success": 15}
      },
      "hotnews": {
        "invoke": {"calls": 5, "success": 3}
      }
    },
    "sse_connections": 1
  },
  "pipeline": {
    "shortInputCheck": {"avg_ms": 0,   "count": 156},
    "normalizeDates":  {"avg_ms": 0,   "count": 156},
    "autoRoute":       {"avg_ms": 1520,"count": 156}
  },
  "snapshots": {
    "vector_store": {
      "total_entries": 25,
      "by_type": {"mem": 10, "note": 5, "msg": 10},
      "estimated_mb": 0.04
    },
    "mcp": {
      "hotnews": {"state": "running", "tools": 1, "version": "mcp-server/hotnewslist"}
    },
    "active_conversations": 3
  }
}
```

## 配置

```yaml
# config.yaml
server:
  admin_users:               # 管理员用户 ID 列表
    - "admin-user-uuid-here"
```

## 中间件

```go
// middleware/admin.go
func AdminOnly(adminUsers []string) gin.HandlerFunc {
    return func(c *gin.Context) {
        userID := c.GetString("user_id")
        for _, id := range adminUsers {
            if id == userID {
                c.Next()
                return
            }
        }
        c.AbortWithStatusJSON(401, ...)
    }
}
```

路由注册（在 NewRouter 内部）：

```go
admin := r.Group("/api/admin", monitor.AdminOnly(cfg.Server.AdminUsers))
admin.GET("/health", monitor.HealthHandler(mon))
```

## 前端管理页面

单页 `/admin`，使用 Chart.js，风格参考 Prometheus 控制台。

### 布局

```
┌────────────────────────────────────────────┐
│  服务运行中 · 运行 1h 23m                    │
├──────────────┬──────────────┬───────────────┤
│  🔥 LLM 调用  │  🧠 内存占用  │  ⚡ goroutines │
│   120 次      │   96.4 MB    │    42         │
│   97.5% 成功  │   堆 12.5 MB  │               │
├──────────────┴──────────────┴───────────────┤
│  📊 LLM 调用分布 (按 caller)      [柱状图]    │
│  📊 工具调用 TOP (按 tool)        [柱状图]    │
│  📈 管线耗时 (avg ms)             [横向条形图] │
├─────────────────────────────────────────────┤
│  📋 工具详情 (名称/schema/状态，可折叠)       │
│  📋 MCP 服务状态                             │
└─────────────────────────────────────────────┘
```

### 技术选型

Chart.js（~60KB gzipped），柱状图 + 横向条形图 + 卡片即可，不需复杂交互。

### 数据流

前端定时 fetch `/api/admin/health`，更新图表。Naive UI 卡片组件 + Chart.js canvas。

## 不确定的项

- **OS RSS 跨平台**：macOS 没有 `/proc/self/status`，dev 环境取不到时填 `-1`。
- **持久化**：现阶段不做。将来需要时 Monitor 接口不变，内部加刷盘逻辑即可。
- **llm_tokens**：TODO，需要包装流式 channel，后续再做。
