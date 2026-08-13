# LLM Token 成本中心

> 2026-08-12

## 概述

为所有 LLM 调用建立统一的 token 消耗追踪系统，支持按日期/用户/场景/模型等多维度聚合查询，为后续的成本分析和优化提供数据基础。

核心原则：
- 装饰器模式拦截，调用方只加 1 行场景标注，采集逻辑全收在 `internal/llmcost/`
- 不统计 Embedding API（独立 API 路径）
- 异步写入 DB，不阻塞 LLM 响应
- 明细永久保留，所有图表查询 GROUP BY 实时跑

## LLM 调用场景全景

经过深度调研，当前系统共 21 个 LLM 调用点，分为五大类：

### 主聊天流（main_chat）

| 子类 | 文件:行 | 方法 | 模型 | 触发 |
|------|---------|------|------|------|
| re_act_loop | `agent/orchestrator.go:682` | Chat（流式） | Flash/Pro | 用户每次发消息 |
| verify | `agent/orchestrator.go:977` | ChatSync | Flash/Pro | ReAct 结束后幻觉验证 |
| title | `agent/orchestrator.go:1775` | ChatSync | Flash | 新会话首条消息后 |
| sub_agent_run | `agent/subagent.go`（RunSubAgent） | ChatSync | Flash | task 工具派生子 agent，子循环每轮 |

### 对话后处理（post_chat）

| 子类 | 文件:行 | 方法 | 模型 | 触发 |
|------|---------|------|------|------|
| memory_extract | `agent/extractor.go:125` | ChatSync | Flash | session_end → MemoryWorker |
| profile_extract | `agent/profile_worker.go:199` | ChatSync | Flash | session_end → ProfileWorker（增量） |
| profile_cold_start | `agent/profile_worker.go:154` | MiniReAct ChatSync | Pro/Flash | 无画像时冷启动提取 |
| summary | `agent/summary_worker.go:133` | ChatSync | Flash | session_end → SummaryWorker |

### 反思系统（reflection）

| 子类 | 文件:行 | 方法 | 模型 | 触发 |
|------|---------|------|------|------|
| finding_score | `agent/scorer.go:152` | ChatSync | Flash | deliver 窗口 → Scorer |
| finding_humanize | `agent/secretary.go:86` | ChatSync | Flash | deliver 窗口 → Secretary |
| theme_discover_conv | `agent/theme_discover.go:292` | ChatSync | Flash | 主题发现（对话模式，当前未启用） |
| theme_discover_stats | `agent/theme_discover.go:317` | ReactLoop ChatSync | Flash | discover 窗口（9:00/18:00） |
| theme_analyze | `agent/theme_analyst.go:122,167` | ReactLoop ChatSync | Flash | review 窗口 |
| state_verify | `agent/theme_verifier.go:52` | ChatSync | Flash | 状态页验证 |
| state_update | `agent/state_updater.go:149` | ReactLoop ChatSync | Flash | session_end → StateWorker |
| mission_discover | `agent/mission_discoverer.go:192` | ChatSync | **Pro** | 任务发现 |
| mission_rank | `agent/mission_discoverer.go:247` | ChatSync | **Pro** | 任务去重排序 |
| mission_run | `agent/mission_runner.go:101` | ReactLoop ChatSync | Flash | mission cron |

### 工具内置（tool）

| 子类 | 文件:行 | 方法 | 模型 | 触发 |
|------|---------|------|------|------|
| reminder_gen | `tool/reminder.go:338` | ChatSync | Flash | 定时提醒触发 |

### 管理与评估（admin）

| 子类 | 文件:行 | 方法 | 模型 | 触发 |
|------|---------|------|------|------|
| agent_summary | `controller/agent.go:32` | ChatSync | Flash | 创建/修改自定义 Agent |
| replay | `agent/replay/runner.go` | Chat（流式） | Flash/Pro | Admin 重放工具 |

### 特殊说明

- ReactLoop / MiniReAct 内部一次 Run 可能包含多轮 ChatSync（每轮一次），每轮均独立记录。
- 仅 2 处流式调用（re_act_loop、replay），其余全是 ChatSync。
- `BuildSystemPrompt` 和 `ChatWithRawJSON` 为死代码，无需追踪。

## 目录结构

```
server/internal/llmcost/
├── types.go        ← Scenario 结构体 + Context 传播（WithScenario / ScenarioFromContext）
├── decorator.go    ← CostTracker 装饰器，实现 llm.LLMClient 接口
├── recorder.go     ← Recorder 接口 + UsageRecord 结构体
└── db_recorder.go  ← DB 实现（异步写入）

server/internal/model/
└── llm_cost.go     ← LlmCostDetail GORM 模型

server/internal/service/
└── llm_cost_repo.go ← Insert + 通用聚合查询

server/internal/controller/
└── cost.go         ← Admin API（stats/summary/details/categories/users 5 端点）
```

## 数据模型

```go
// server/internal/model/llm_cost.go

type LlmCostDetail struct {
    ID                   uint      `gorm:"primaryKey"`
    Date                 string    `gorm:"type:date;not null;index"`
    UserID               string    `gorm:"size:36;not null;default:'';index"`
    Category             string    `gorm:"type:varchar(30);not null;index"`
    SubCategory          string    `gorm:"type:varchar(50);not null"`
    Model                string    `gorm:"type:varchar(50);not null"`
    ConvID               string    `gorm:"size:36;index"`
    RefType              string    `gorm:"type:varchar(30);index"`
    RefID                string    `gorm:"size:36;index"`
    PromptTokens         int64     `gorm:"not null;default:0"`
    PromptCacheHitTokens int64     `gorm:"not null;default:0"`
    PromptCacheMissTokens int64    `gorm:"not null;default:0"`
    CompletionTokens     int64     `gorm:"not null;default:0"`
    TotalTokens          int64     `gorm:"not null;default:0"`
    MetaJSON             string    `gorm:"type:text"`
    CreatedAt            time.Time `gorm:"autoCreateTime;index"`
}
```

### 字段说明

| 字段 | 含义 | 说明 |
|------|------|------|
| Date | 调用日期（YYYY-MM-DD） | 从 CreatedAt 提取，便于 GROUP BY |
| UserID | 用户 ID | 空字符串 = 系统级后台任务；UUID 36 字符 |
| Category | 大类 | main_chat / post_chat / reflection / tool / admin |
| SubCategory | 子类 | re_act_loop / verify / memory_extract / ... |
| Model | 模型名 | deepseek-v4-flash / deepseek-v4-pro |
| ConvID | 会话 ID | main_chat / post_chat 场景填充 |
| RefType | 引用类型 | finding / theme / mission_disc / mission / reminder / agent |
| RefID | 引用 ID | 对应类型的实体 ID |
| PromptTokens | prompt 总 token | = hit + miss |
| PromptCacheHitTokens | 命中缓存 token | DeepSeek 返回的 prompt_cache_hit_tokens |
| PromptCacheMissTokens | 未命中缓存 token | DeepSeek 返回的 prompt_cache_miss_tokens |
| CompletionTokens | 生成 token | completion_tokens |
| TotalTokens | 总 token | prompt + completion |
| MetaJSON | 扩展信息 | 其余场景相关元数据（JSON 格式） |

> 注：缓存字段为 Phase 4a 新增。当前已落库数据（Phase 2/3 期间）无缓存拆分，查询时按 0 处理。

### 各场景 ConvID / RefType / RefID 填充规则

| 大类 | 子类 | ConvID | RefType | RefID |
|------|------|--------|---------|-------|
| main_chat | re_act_loop | ✓ | — | — |
| main_chat | verify | ✓ | — | — |
| main_chat | title | ✓ | — | — |
| post_chat | memory_extract | ✓ | — | — |
| post_chat | profile_extract | ✓ | — | — |
| post_chat | profile_cold_start | ✓ | — | — |
| post_chat | summary | ✓ | — | — |
| reflection | finding_score | — | finding | finding_id |
| reflection | finding_humanize | — | finding | finding_id |
| reflection | theme_discover_conv | — | theme | theme_id |
| reflection | theme_discover_stats | — | theme | theme_id |
| reflection | theme_analyze | — | theme | theme_id |
| reflection | state_verify | — | theme | theme_id |
| reflection | state_update | — | theme | theme_id |
| reflection | mission_discover | — | mission_disc | theme_id |
| reflection | mission_rank | — | mission | mission_id |
| reflection | mission_run | — | mission | mission_id |
| tool | reminder_gen | — | reminder | reminder_id |
| admin | agent_summary | — | agent | agent_id |
| admin | replay | ✓ | — | — |
| admin | eval_prelabel | — | — | — |

### 索引策略

- `date` — 时间范围过滤 + GROUP BY date
- `user_id` — 按用户筛选
- `category` — 按大类筛选 + GROUP BY category（饼图）
- `conv_id` — 按会话聚合
- `ref_type` + `ref_id` — 按引用实体聚合
- `created_at` — 明细列表排序

单列索引即可覆盖所有查询模式。SQLite/MySQL 优化器在百万行级别下 GROUP BY + 索引表现良好。

## 装饰器设计

### 接口

```go
// Scenario 场景标注，通过 context 传播
type Scenario struct {
    Category    string                 // main_chat / post_chat / reflection / tool / admin
    SubCategory string                 // re_act_loop / verify / ...
    ConvID      string                 // 可选，会话 ID
    RefType     string                 // 可选，引用类型
    RefID       string                 // 可选，引用 ID
    Meta        map[string]interface{} // 其余扩展信息 → MetaJSON
}

// WithScenario 将场景标注注入 context
func WithScenario(ctx context.Context, s Scenario) context.Context

// ScenarioFromContext 从 context 提取场景标注
func ScenarioFromContext(ctx context.Context) (Scenario, bool)
```

### CostTracker 装饰器

```go
type CostTracker struct {
    inner    llm.LLMClient
    recorder Recorder
}

func WrapLLMClient(inner llm.LLMClient, recorder Recorder) llm.LLMClient

// Chat 流式调用 — 包装 channel，监听 done 事件提取 Usage
func (t *CostTracker) Chat(ctx context.Context, req *llm.ChatRequest) (<-chan *llm.StreamEvent, error)

// ChatSync 同步调用 — 直接从返回的 Message.Usage 提取
func (t *CostTracker) ChatSync(ctx context.Context, req *llm.ChatRequest) (llm.Message, error)

// ChatWithRawJSON 透传（当前未使用，保持接口完整）
func (t *CostTracker) ChatWithRawJSON(ctx context.Context, body json.RawMessage) (llm.Message, error)

// BuildSystemPrompt 透传（不产生 LLM 调用）
func (t *CostTracker) BuildSystemPrompt(base string, tools []llm.ToolDefinition) string

// ModelName 透传
func (t *CostTracker) ModelName() string
```

### Chat 流式处理关键逻辑

```
CostTracker.Chat(ctx, req)
  ├─ inner.Chat(ctx, req) → innerCh
  ├─ 创建 outerCh（buffer=100）
  ├─ goroutine:
  │   ├─ 遍历 innerCh，透传事件到 outerCh
  │   └─ 收到 "done" 事件 → go record(ctx, req, event.Usage)
  └─ 返回 outerCh
```

消费者提前关闭 channel 不影响记录（goroutine 会完成）。

### record() 逻辑

```go
func (t *CostTracker) record(ctx context.Context, req *llm.ChatRequest, usage *llm.Usage) {
    scenario, _ := ScenarioFromContext(ctx)

    record := UsageRecord{
        Date:                 time.Now(),
        UserID:               coalesce(scenario.UserID, req.UserID),
        Category:             scenario.Category,
        SubCategory:          scenario.SubCategory,
        Model:                t.inner.ModelName(),
        ConvID:               scenario.ConvID,
        RefType:              scenario.RefType,
        RefID:                scenario.RefID,
        PromptTokens:         usage.PromptTokens,
        PromptCacheHitTokens: usage.PromptCacheHitTokens,
        PromptCacheMissTokens: usage.PromptCacheMissTokens,
        CompletionTokens:     usage.CompletionTokens,
        TotalTokens:          usage.TotalTokens,
        Meta:                 scenario.Meta,
    }

    go t.recorder.Record(record) // 异步写入，不阻塞
}
```

- `user_id` 优先级：scenario.UserID > req.UserID > ""
- 写入失败只打 log，不影响 LLM 调用主流程
- 若 ctx 中无 Scenario，category/sub_category 为空，标记为 "unknown"

### Recorder 接口

```go
type Recorder interface {
    Record(record UsageRecord)
}

type UsageRecord struct {
    Date                 time.Time
    UserID               string
    Category             string
    SubCategory          string
    Model                string
    ConvID               string
    RefType              string
    RefID                string
    PromptTokens         int
    PromptCacheHitTokens int
    PromptCacheMissTokens int
    CompletionTokens     int
    TotalTokens          int
    Meta                 map[string]interface{}
}
```

> 缓存字段（PromptCacheHitTokens / PromptCacheMissTokens）为 Phase 4a 新增。

### DB Recorder 实现

```go
type DBRecorder struct {
    repo *service.LlmCostRepo
    ch   chan UsageRecord  // 缓冲 channel，解耦写入
}

func NewDBRecorder(repo *service.LlmCostRepo) *DBRecorder {
    r := &DBRecorder{repo: repo, ch: make(chan UsageRecord, 1024)}
    go r.worker() // 单 goroutine 消费，顺序写入
    return r
}

func (r *DBRecorder) Record(record UsageRecord) {
    select {
    case r.ch <- record:
    default:
        // channel 满则丢弃（极端情况），打 log
        log.Printf("[llmcost] record channel full, dropping")
    }
}
```

单 goroutine worker 模式避免并发写入竞争，channel 缓冲 1024 足够应对突发。

## 接入方式

### 初始化链路

```go
// server/cmd/xhan-helper/main.go

func main() {
    // ... existing init ...

    db := appinit.InitDB(cfg)
    db.AutoMigrate(&model.LlmCostDetail{})

    costRepo := service.NewLlmCostRepo(db)
    costRecorder := llmcost.NewDBRecorder(costRepo)

    llmClients := app.InitLLM(cfg, llm.NewClient, llm.NewStrongClient, costRecorder)
    // ...
}
```

```go
// server/internal/app/init_llm.go

func InitLLM(cfg *config.Config,
    newClient, newStrongClient LLMClientFactory,
    costRecorder llmcost.Recorder, // 新增参数
) *LLMClients {

    llmClient, _ := newClient(cfg.LLM)
    llmClient = monitor.WrapLLMClient(llmClient, "orchestrator/flash")
    llmClient = llmcost.WrapLLMClient(llmClient, costRecorder) // 新增

    strongClient, _ := newStrongClient(cfg.LLM)
    if strongClient != nil {
        strongClient = monitor.WrapLLMClient(strongClient, "orchestrator/pro")
        strongClient = llmcost.WrapLLMClient(strongClient, costRecorder) // 新增
    }
    return &LLMClients{Flash: llmClient, Strong: strongClient}
}
```

包装顺序：`ProClient → Monitor → CostTracker`。CostTracker 在最外层，确保最先拦截 Usage。

### 调用点标注模式

每个调用点改动 1 行：

```go
// 改前
resp, err := client.ChatSync(ctx, req)

// 改后
ctx = llmcost.WithScenario(ctx, llmcost.Scenario{
    Category:    "post_chat",
    SubCategory: "memory_extract",
    ConvID:      convID,
})
resp, err := client.ChatSync(ctx, req)
```

流式调用同理：

```go
// 改前
stream, err := client.Chat(ctx, req)

// 改后
ctx = llmcost.WithScenario(ctx, llmcost.Scenario{
    Category:    "main_chat",
    SubCategory: "re_act_loop",
    ConvID:      convID,
})
stream, err := client.Chat(ctx, req)
```

### ReactLoop / MiniReAct 标注

ReactLoop 和 MiniReAct 内部调用 `ChatSync`，标注应在调用者入口处完成，ctx 透传即可：

```go
// theme_analyst.go — 调用者
ctx = llmcost.WithScenario(ctx, llmcost.Scenario{
    Category: "reflection",
    SubCategory: "theme_analyze",
    RefType:  "theme",
    RefID:    themeID,
})
result, err := loop.Run(ctx) // ReactLoop 内部 ChatSync 自动继承 ctx 中的 Scenario
```

### HTTP Handler 场景标注

HTTP Handler 中的 LLM 调用（如 `agent_summary`）需注意：gin context 中注入的 `user_id` 不在标准 `context.Context` 中，需要显式传播。同时 RefID（如 agent.ID）在 LLM 调用前已可用：

```go
// controller/agent.go — CreateAgent
func (h *AgentHandler) CreateAgent(c *gin.Context) {
    userID := c.GetString("user_id")
    agent := &model.Agent{
        ID:     uuid.New().String(),
        UserID: userID,
        // ...
    }
    // agent.ID 已生成，可填入 RefID
    ctx := llmcost.WithScenario(c.Request.Context(), llmcost.Scenario{
        Category:    "admin",
        SubCategory: "agent_summary",
        UserID:      userID,
        RefType:     "agent",
        RefID:       agent.ID,
    })
    agent.Summary = h.generateSummary(ctx, req.SystemPrompt)
}
```

### 后台任务 user_id 传播

后台 worker 从处理的实体获取 user_id 并传入场景标注：

```go
// MemoryWorker.Process()
ctx = llmcost.WithScenario(ctx, llmcost.Scenario{
    Category:    "post_chat",
    SubCategory: "memory_extract",
    ConvID:      sess.ConvID,
    UserID:      sess.UserID, // 从 SessionSnapshot 获取
})
```

通用原则：**能追溯到具体用户的场景填实际 user_id，全局系统级任务（如 theme_discover_stats 全局扫描）user_id 留空或填 Scenario.UserID = ""。**

## Admin API

从页面需求反推，共 5 个端点。页面布局：

```
┌──────────────────────────────────────────────────────────┐
│  概览卡片（summary）                                      │
│  今日/昨日/本月: total │ calls │ cache_hit_rate          │
├──────────────┬───────────────────────────────────────────┤
│  时间范围 ▼   │  分类筛选 ▼（多选）   用户筛选 ▼（多选）   │
├──────────────┴───────────────────────────────────────────┤
│  柱状图: 每日 Token 消耗（堆叠维度前端自选: hit/miss/completion）│
│  柱状图: 每日 Completion Token 总量                       │
├───────────────────────┬──────────────────────────────────┤
│  饼图: 分类占比（hit/miss/completion 前端自选）           │
│  饼图: 分类 Completion Token 占比                        │
│  饼图: 模型分布（Flash vs Pro）                           │
├───────────────────────┴──────────────────────────────────┤
│  饼图: 子类占比（选定分类下 sub_category）                │
│  表格: 最近调用明细（details）                            │
└──────────────────────────────────────────────────────────┘
```

### 1. `GET /api/admin/cost/stats` — 聚合查询（图表数据源）

请求参数：

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| start | string | 是 | 开始日期 YYYY-MM-DD |
| end | string | 是 | 结束日期 YYYY-MM-DD |
| user_id | string[] | 否 | 多选用户（`?user_id=a&user_id=b`）；不传 = 所有用户聚合 |
| category | string | 否 | 按大类筛选 |
| group_by | string | 是 | date / category / model / user_id / sub_category |

响应：

```json
{
  "total": {
    "prompt_cache_hit": 300000,
    "prompt_cache_miss": 500000,
    "completion": 434567,
    "total_tokens": 1234567,
    "call_count": 890,
    "cache_hit_rate": 0.375
  },
  "groups": [
    {
      "key": "2026-08-12",
      "prompt_cache_hit": 15000,
      "prompt_cache_miss": 15000,
      "completion": 20000,
      "total_tokens": 50000,
      "call_count": 30,
      "cache_hit_rate": 0.5
    }
  ]
}
```

- `cache_hit_rate` = hit / (hit + miss)，分母为 0 时返回 0
- 柱状图/饼图堆叠维度由**前端自选**（hit/miss/completion 勾选组合），API 每行返回三个维度值

### 2. `GET /api/admin/cost/summary` — 概览卡片

请求参数：`user_id`（可选，多选）

```json
{
  "today": { "cache_hit": 5000, "cache_miss": 3000, "completion": 4300, "total": 12300, "calls": 45, "hit_rate": 0.63 },
  "yesterday": { "total": 11000, "calls": 38, "hit_rate": 0.55 },
  "this_month": { "total": 345000, "calls": 1200, "hit_rate": 0.58 }
}
```

### 3. `GET /api/admin/cost/details` — 调用明细分页

请求参数：

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| page | int | 否 | 默认 1 |
| page_size | int | 否 | 默认 20，最大 100 |
| user_id | string[] | 否 | 多选 |
| category | string | 否 | 筛选 |
| date | string | 否 | 精确日期筛选 YYYY-MM-DD |

```json
{
  "total": 500,
  "rows": [{
    "id": 1,
    "date": "2026-08-12",
    "user_id": "xxx",
    "category": "main_chat",
    "sub_category": "re_act_loop",
    "model": "deepseek-v4-flash",
    "prompt_cache_hit": 5000,
    "prompt_cache_miss": 3000,
    "completion": 2000,
    "total_tokens": 10000,
    "conv_id": "xxx",
    "ref_type": "",
    "ref_id": "",
    "meta_json": "...",
    "created_at": "2026-08-12T10:00:00Z"
  }]
}
```

### 4. `GET /api/admin/cost/categories` — 分类下拉列表

```json
["main_chat", "post_chat", "reflection", "tool", "admin"]
```

直接返回 llmcost 常量，无需查 DB。

### 5. `GET /api/admin/cost/users` — 用户下拉列表（多选）

```json
[
  {"user_id": "9b7e164c-...", "total_tokens": 28884, "call_count": 5},
  {"user_id": "7893c5a4-...", "total_tokens": 15493, "call_count": 4}
]
```

- 按 `total_tokens DESC` 排序，取有成本记录的用户
- 前端下拉多选：默认全不选 = 展示所有用户；选中后传回 `?user_id=...&user_id=...`，所有图表展示所选用户聚合数据

### 典型查询场景

| 图表 | group_by | 前端处理 |
|------|----------|----------|
| 每日 token 消耗柱状图 | date | 堆叠 hit/miss/completion（自选） |
| 每日 completion 柱状图 | date | 只取 completion 维度 |
| 分类占比饼图 | category | hit/miss/completion 自选 |
| 分类 completion 饼图 | category | 只取 completion |
| 模型分布饼图 | model | — |
| 子类占比饼图 | sub_category | + category 筛选 |

## 实施步骤

### Phase 1: 核心包 `internal/llmcost/` ✅

1. `types.go` — Scenario 结构体 + context 传播函数 + 常量定义（CategoryMainChat 等）
2. `recorder.go` — Recorder 接口 + UsageRecord 结构体
3. `model/llm_cost.go` — LlmCostDetail GORM 模型
4. `service/llm_cost_repo.go` — Insert + 通用聚合查询

### Phase 2: 装饰器 + 接线 ✅

5. `llmcost/decorator.go` — CostTracker 实现 Chat / ChatSync 拦截
6. `llmcost/db_recorder.go` — 基于 channel 的异步 DB Recorder
7. `main.go` — AutoMigrate + 创建 DBRecorder
8. `app/init_llm.go` — 包装 CostTracker，调整 InitLLM 签名

### Phase 3: 21 个调用点场景标注 ✅

9. main_chat / post_chat / reflection / tool / admin 五大类全部标注

### Phase 4a: 缓存字段（待做）

10. `model/llm_cost.go` — 加 `PromptCacheHitTokens` / `PromptCacheMissTokens`
11. `llmcost/recorder.go` — UsageRecord 加缓存字段
12. `llmcost/decorator.go` — record() 填入缓存 token
13. `llmcost/db_recorder.go` — worker 映射缓存字段
14. 更新测试

### Phase 4b: 查询升级（待做）

15. `service/llm_cost_repo.go` — QueryParams 支持多 user_id（[]string）；GroupRow 返回 hit/miss/completion 拆分 + cache_hit_rate

### Phase 5: API 端点（待做）

16. `controller/cost.go` — stats / summary / details / categories / users 5 个端点
17. 注册路由（Admin 路由组）

### Phase 6: 验证（待做）

18. `go build ./...` + `go test ./...` 全绿
19. 启动服务验证 5 个端点数据正确

## 影响范围

| 层级 | 文件 | 改动性质 |
|------|------|----------|
| 新增 | `internal/llmcost/` (4 files) | 新建包 |
| 新增 | `internal/model/llm_cost.go` | 新建模型 |
| 新增 | `internal/service/llm_cost_repo.go` | 新建 Service |
| 新增 | `internal/controller/cost.go` | 新建 Handler（5 端点） |
| 修改 | `cmd/xhan-helper/main.go` | 注册 Model + 创建 Recorder |
| 修改 | `internal/app/init_llm.go` | 增加 Recorder 参数 + CostTracker 包装 |
| 修改 | 21 个调用点 | 各加 1 行 WithScenario |
| 修改 | `internal/llm/deepseek.go` | 修复 ChatSync 漏 total_tokens（Phase 2 已做） |

## 风险与边界

- **不追踪 Embedding**：豆包 Embedding API 是独立路径，不在 LLMClient 接口管辖范围。
- **ReactLoop 误标注风险**：标注在 ReactLoop 调用者入口，若 ReactLoop 被新调用者使用且未标注，会记录为 unknown。建议 ReactLoop 构造时校验 ctx 中是否有 Scenario，无则打 warning log。
- **ChatWithRawJSON 不追踪**：当前无生产调用，装饰器中直接透传，不记录。
- **DB Recorder channel 满丢弃**：极端情况（1024 条积压），丢弃并打 log。此规模下几乎不可能触发。
- **user_id 为空的后台任务**：reflection 的部分场景（如全局主题发现）user_id 为空字符串，查询时作为 "system" 归类。
- **gin context vs 标准 context**：Auth 中间件通过 `c.Set("user_id", ...)` 存入 gin context，不会自动进入 `c.Request.Context()`。HTTP Handler 中调用 LLM 时需显式将 `user_id` 填入 `Scenario.UserID` 或通过 `context.WithValue` 注入标准 ctx。
