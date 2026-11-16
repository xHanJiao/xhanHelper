# Phase 6 — 可观测性方案

> 日期: 2026-05-15 | 状态: 设计阶段

---

## 一、现有基建盘点

| 能力 | 现有实现 | 覆盖范围 |
|------|---------|----------|
| LLM 调用计数 | `monitor.IncLLMCall(caller, err)` | 成功率统计，按 caller 分组 |
| LLM 延迟 | `monitor.RecordLLMCall(caller, ms, pt, ct)` | P50/P99 latency |
| LLM Token | `monitor.RecordLLMCall` 中的 pt/ct | 累计 prompt/completion tokens |
| 工具调用计数 | `monitor.IncToolCall(tool, fn, err)` | 成功率统计 |
| 工具延迟 | `monitor.IncToolCallWithLatency(tool, fn, err, ms)` | P50/P99 latency |
| 管线步骤延迟 | `monitor.RecordPipelineStep(step, ms)` | 已用于预处理管线 |
| SSE 连接数 | `monitor.IncSSE()/DecSSE()` | 实时连接数 |
| 自定义快照 | `monitor.Register(name, fn)` | 可注册任意快照数据 |
| LLM 请求/响应记录 | `Message.RequestJSON/ResponseJSON` | 仅聊天路径，子 agent 调用无 |
| 标准日志 | `log.Printf` | 分散各处，无结构化，可能被截断 |

**现有基建的缺口**：

1. **无结构化决策日志**——`log.Printf` 是纯文本，无法按决策类型检索或聚合
2. **子 Agent LLM 调用无记录**——聊天路径有 `RequestJSON/ResponseJSON`，但 ThemeAnalyst/Scorer/ThemeDiscoverer 的内部 ChatSync 调用没有
3. **无 trace ID**——一次反思循环内的多个 LLM 调用、多个工具调用无法关联
4. **决策原因无记录**——ThemeAnalyst 的 `skip_reason`、Scorer 的 `drop_reason` 仅在 JSON 响应中，未被系统化存储或查询
5. **日志可能被截断**——`log.Printf` 对长文本（LLM prompt 可能数万字符）会被终端或日志系统截断

---

## 二、需要新增的能力

### 2.1 反思 Trace 模型

每次反思循环（定时或事件触发）生成一个唯一的 `trace_id`。该循环内的所有 LLM 调用、工具调用、决策都关联到此 trace_id。

```go
type ReflectionTrace struct {
    ID            string    `gorm:"primaryKey;size:36" json:"id"`
    UserID        string    `gorm:"size:36;not null;index" json:"user_id"`
    TriggerType   string    `gorm:"size:20;not null" json:"trigger_type"` // "cron" | "event"
    StartedAt     time.Time `json:"started_at"`
    EndedAt       *time.Time `json:"ended_at,omitempty"`
    Status        string    `gorm:"size:20;default:'running'" json:"status"` // running | completed | empty
    TotalFindings int       `json:"total_findings"` // 所有 ThemeAnalyst 返回的发现总数
    DeliveredCount int     `json:"delivered_count"` // 最终投递到信箱的条数
    ErrorMessage  *string   `gorm:"type:text" json:"error_message,omitempty"`
}
```

### 2.2 LLM 调用记录

每条子 Agent 的 LLM 调用都记录完整输入输出，不截断。

```go
type ReflectionLLMCall struct {
    ID              string    `gorm:"primaryKey;size:36" json:"id"`
    TraceID         string    `gorm:"size:36;not null;index" json:"trace_id"`
    Caller          string    `gorm:"size:50;not null" json:"caller"` // "ThemeAnalyst" | "Scorer" | "ThemeDiscoverer" | "LaunchAgent"
    CallerDetail    string    `gorm:"size:100" json:"caller_detail,omitempty"` // 如 "ThemeAnalyst:职业规划"
    RequestJSON     *string   `gorm:"type:longtext" json:"request_json,omitempty"`   // 完整 LLM 请求 JSON
    ResponseJSON    *string   `gorm:"type:longtext" json:"response_json,omitempty"`  // 完整 LLM 响应 JSON
    PromptTokens    int       `json:"prompt_tokens"`
    CompletionTokens int     `json:"completion_tokens"`
    LatencyMs       int64     `json:"latency_ms"`
    ErrorMessage    *string   `gorm:"type:text" json:"error_message,omitempty"`
    CreatedAt       time.Time `gorm:"autoCreateTime" json:"created_at"`
}
```

### 2.3 决策日志

每个关键决策点结构化记录原因。

```go
type ReflectionDecision struct {
    ID           string    `gorm:"primaryKey;size:36" json:"id"`
    TraceID      string    `gorm:"size:36;not null;index" json:"trace_id"`
    Stage        string    `gorm:"size:30;not null" json:"stage"`      // "theme_selection" | "theme_analysis" | "scoring" | "discovery"
    Subject      string    `gorm:"size:100;not null" json:"subject"`   // 如 "职业规划" | "finding f-003"
    Verdict      string    `gorm:"size:20;not null" json:"verdict"`    // "selected" | "skipped" | "topn" | "dropped" | "rejected" | "nominated"
    Reason       string    `gorm:"type:text;not null" json:"reason"`   // 完整原因
    DetailJSON   *string   `gorm:"type:json" json:"detail_json,omitempty"` // 附加上下文（如完整 finding JSON）
    CreatedAt    time.Time `gorm:"autoCreateTime" json:"created_at"`
}
```

### 2.4 告警指标注册

利用现有 `monitor.Register()` 注册反思系统的快照指标。

---

## 三、每个环节的可观测性要求

### 3.1 启动 Agent — 主题选择

| 记录项 | 方式 | 不截断要求 |
|--------|------|-----------|
| LLM 输入（完整 system prompt + 主题列表） | `ReflectionLLMCall.RequestJSON` | 全文存储，longtext |
| LLM 输出（选中/跳过哪些主题） | `ReflectionLLMCall.ResponseJSON` | 全文 |
| 每个主题的选中/跳过原因 | `ReflectionDecision` × N | stage="theme_selection", verdict=selected/skipped |

### 3.2 ThemeAnalyst — 主题分析

| 记录项 | 方式 | 不截断要求 |
|--------|------|-----------|
| LLM 输入（system prompt + 检索到的数据） | `ReflectionLLMCall.RequestJSON` | 全文，caller_detail="ThemeAnalyst:<主题名>" |
| LLM 输出（findings + state_text + skip_reason） | `ReflectionLLMCall.ResponseJSON` | 全文 |
| 内部检索工具调用 | `ReflectionLLMCall` 每条一次（检索轮次），或复用 `monitor.IncToolCall` | — |
| 每条 finding 的 source_refs | `ReflectionDecision.DetailJSON`（可选） | 完整 finding JSON |
| skip_reason + skip_detail | `ReflectionDecision` | stage="theme_analysis", verdict="skipped", reason=skip_detail |

### 3.3 Scorer — 打分排序

| 记录项 | 方式 | 不截断要求 |
|--------|------|-----------|
| LLM 输入（所有 findings 列表） | `ReflectionLLMCall.RequestJSON` | 全文 |
| LLM 输出（ranked + dropped + brief_title） | `ReflectionLLMCall.ResponseJSON` | 全文 |
| 每条 finding 的入选/落选原因 | `ReflectionDecision` × N | stage="scoring", verdict="topn"/"dropped", reason=editor_note/drop_reason |
| 打分明细 | `ReflectionDecision.DetailJSON` | 完整 scores_detail |

### 3.4 ThemeDiscoverer — 候选主题发现

| 记录项 | 方式 | 不截断要求 |
|--------|------|-----------|
| LLM 输入（system prompt + 活动概览） | `ReflectionLLMCall.RequestJSON` | 全文 |
| LLM 输出（candidates + rejected） | `ReflectionLLMCall.ResponseJSON` | 全文 |
| 每个候选的提名理由 | `ReflectionDecision` | stage="discovery", verdict="nominated", reason=rationale |
| 每个被否决方向的否决原因 | `ReflectionDecision` | stage="discovery", verdict="rejected", reason=reject_reason |
| K 聚类去重结果 | `log.Printf` + 可选 `ReflectionDecision` | 哪个候选被聚类过滤、距离多少 |

### 3.5 信箱投递

| 记录项 | 方式 | 不截断要求 |
|--------|------|-----------|
| 投递的完整内容 | `MailboxItem` 表本身 | 已存储 |
| 投递时间 | `MailboxItem.CreatedAt` | — |

---

## 四、需要改动的文件

### 新建

| 文件 | 说明 |
|------|------|
| `internal/model/reflection_trace.go` | ReflectionTrace + ReflectionLLMCall + ReflectionDecision 模型 |
| `internal/service/reflection_trace_repo.go` | 三个模型的 DB CRUD |

### 改动

| 文件 | 改动 |
|------|------|
| `internal/monitor/llm_wrapper.go` | `ChatSync` 增加返回耗时和 token 数（当前只统计了调用次数，没拿 token 数） |
| `internal/agent/reflection.go` | 创建 ReflectionTrace，生成 trace_id，在各个环节记录决策 |
| `internal/agent/theme_analyst.go` | 每次内部 ChatSync 调用后写入 ReflectionLLMCall；分析结果写入 ReflectionDecision |
| `internal/agent/scorer.go` | 打分结果写入 ReflectionDecision（每条 finding 一条） |
| `internal/agent/theme_discover.go` | 候选/否决写入 ReflectionDecision；K 聚类结果追加日志 |
| `internal/monitor/monitor.go` | 注册反思快照（可选：当日 trace 数、平均发现数等） |

### 不截断保障

- `RequestJSON` / `ResponseJSON` 使用 MySQL `MEDIUMTEXT`（最大 16MB），单次 LLM 调用远超实际需求
- `log.Printf` 仅用于实时调试，不依赖其截断输出做事后追溯。所有事后追溯走 DB 查询
- DB 中的 LLM 调用记录设置保留策略（如 30 天自动清理），避免涨满磁盘

---

## 五、API 与前端看板

### 5.1 后端 API

在现有 `/api/admin` 路由组下新增反思可观测性接口：

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/admin/reflection/traces` | 最近的反思循环列表（分页），含状态、发现数、投递数、耗时 |
| GET | `/api/admin/reflection/traces/:id` | 单次循环详情：所有 LLM 调用 + 所有决策 |
| GET | `/api/admin/reflection/decisions` | 决策列表，支持按 stage/verdict/时间筛选 |
| GET | `/api/admin/reflection/llm-calls` | LLM 调用列表，支持按 caller/时间筛选 |
| GET | `/api/admin/reflection/llm-calls/:id` | 单次 LLM 调用的完整输入输出 |
| GET | `/api/admin/reflection/stats` | 统计摘要：近 N 天 trace 数、平均发现数、空循环占比、各 skip_reason 分布等 |

认证：复用现有 `Auth` + `AdminOnly` 中间件。所有接口支持 `?limit=&offset=` 分页。

### 5.2 前端子 Tab

监控面板在 TopBar 切换子 Tab：

```
MonitorView (父路由 /admin)
├── Tab: 概览    → 现有 /api/admin/health 数据（LLM 调用量、工具调用量、延迟等）
├── Tab: 反思 🔍 → 新增，反思系统专属看板
│   ├── 最近 Trace 列表（时间、触发类型、状态、发现数→投递数）
│   │   └── 点击展开详情：该轮所有决策的时间线
│   ├── 决策分布饼图（selected / skipped / topn / dropped / nominated / rejected）
│   ├── LLM 调用列表（按 caller 分组，可展开查看完整输入输出）
│   └── 统计摘要卡片（近 7 天平均发现数、空循环率、Top3 主题）
└── Tab: 信箱 📬 → 待建（与 MailboxView 共享数据源）
```

### 5.3 实施优先级

| 优先级 | 内容 | 说明 |
|--------|------|------|
| P0 | 后端 API（5.1 全部） | 反思系统上线即需要，前端可以先不管 |
| P1 | 反思 Tab 核心（trace 列表 + 详情） | 排障最小可用 |
| P2 | 决策分布图 + LLM 调用查看 | 辅助调优 |
| P3 | 统计摘要 + 信箱 Tab | 锦上添花 |

---

## 六、查询与排障示例

日常使用中，开发者通过查询 DB 或 API 而非翻日志来定位问题。

```sql
-- 查看最近一次反思循环的所有决策
SELECT * FROM reflection_decisions WHERE trace_id = 'xxx' ORDER BY created_at;

-- 查看某个主题最近 3 次分析的 LLM 输入输出
SELECT * FROM reflection_llm_calls 
WHERE caller_detail LIKE '%职业规划%' 
ORDER BY created_at DESC LIMIT 3;

-- 统计 ThemeDiscoverer 的否决原因分布
SELECT reason, COUNT(*) FROM reflection_decisions 
WHERE stage = 'discovery' AND verdict = 'rejected' 
GROUP BY reason;

-- 统计 Scorer 的落选原因分布
SELECT reason, COUNT(*) FROM reflection_decisions 
WHERE stage = 'scoring' AND verdict = 'dropped' 
GROUP BY reason;
```

### 调优工作流

1. 发现异常（如信箱长期无推送）→ 查最近 trace 的 `status`
2. 全部 `empty` → 查 ReflectionDecision，看是哪些主题频繁返回 skip_reason
3. 找到具体主题 → 查 ReflectionLLMCall，看 LLM 的输入（数据够不够）和输出（分析有没有道理）
4. 找到 prompt 问题 → 调整 prompt → 对比调整前后同主题的 LLMCall 输出
5. 发现大量候选被拒 → 查 ReflectionDecision (discovery/rejected)，调整 ThemeDiscoverer 的不该提名列表

---

## 后续变更

- **2026-11-15 · 反思可观测性整条链下线**：`ReactLoop`（第二套 ReAct 循环）的最后两个生产调用方（MissionRunner 2026-09-22、StateUpdater 2026-11-05）先后删除后，`NewReactLoop` 已无任何生产调用方，`reflection_traces` / `reflection_llm_calls` / `reflection_decisions` 三张表从此只读不写。本次一次性删除：`agent/react_loop.go`、`service/reflection_trace_repo.go`、`model/reflection_trace.go`、`controller/reflection_admin.go`、`/api/admin/reflection/*` 四条只读路由、`ParseLLMResponse` / `LLMResponse`、`Registry.GeneratePrompt`（prompt-engineering 路径最后一块）以及前端 `AdminView` 的反思 Tab。表不做 DROP（存量数据保留，新装不再 AutoMigrate）。本文档描述的设计仅作历史参考。
