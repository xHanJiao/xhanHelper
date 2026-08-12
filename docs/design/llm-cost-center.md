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
| reat_loop | `agent/orchestrator.go:682` | Chat（流式） | Flash/Pro | 用户每次发消息 |
| verify | `agent/orchestrator.go:977` | ChatSync | Flash/Pro | ReAct 结束后幻觉验证 |
| title | `agent/orchestrator.go:1775` | ChatSync | Flash | 新会话首条消息后 |

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
- 仅 2 处流式调用（reat_loop、replay），其余全是 ChatSync。
- `BuildSystemPrompt` 和 `ChatWithRawJSON` 为死代码，无需追踪。

## 目录结构

```
server/internal/llmcost/
├── types.go        ← Scenario 结构体 + Context 传播（WithScenario / ScenarioFromContext）
├── decorator.go    ← CostTracker 装饰器，实现 llm.LLMClient 接口
├── recorder.go     ← Recorder 接口 + Record 结构体
└── db_recorder.go  ← DB 实现（异步写入）

server/internal/model/
└── llm_cost.go     ← LlmCostDetail GORM 模型

server/internal/service/
└── llm_cost_repo.go ← Insert + 通用聚合查询

server/internal/controller/
└── cost.go         ← Admin API（/api/admin/cost/query）
```

## 数据模型

```go
// server/internal/model/llm_cost.go

type LlmCostDetail struct {
    ID               uint      `gorm:"primaryKey"`
    Date             string    `gorm:"type:date;not null;index"`
    UserID           string    `gorm:"type:varchar(32);not null;default:'';index"`
    Category         string    `gorm:"type:varchar(30);not null;index"`
    SubCategory      string    `gorm:"type:varchar(50);not null"`
    Model            string    `gorm:"type:varchar(50);not null"`
    ConvID           string    `gorm:"type:varchar(32);index"`
    RefType          string    `gorm:"type:varchar(30);index"`
    RefID            string    `gorm:"type:varchar(32);index"`
    PromptTokens     int64     `gorm:"not null;default:0"`
    CompletionTokens int64     `gorm:"not null;default:0"`
    TotalTokens      int64     `gorm:"not null;default:0"`
    MetaJSON         string    `gorm:"type:text"`
    CreatedAt        time.Time `gorm:"autoCreateTime;index"`
}
```

### 字段说明

| 字段 | 含义 | 说明 |
|------|------|------|
| Date | 调用日期（YYYY-MM-DD） | 从 CreatedAt 提取，便于 GROUP BY |
| UserID | 用户 ID | 空字符串 = 系统级后台任务 |
| Category | 大类 | main_chat / post_chat / reflection / tool / admin |
| SubCategory | 子类 | reat_loop / verify / memory_extract / ... |
| Model | 模型名 | deepseek-v4-flash / deepseek-v4-pro |
| ConvID | 会话 ID | main_chat / post_chat 场景填充 |
| RefType | 引用类型 | finding / theme / mission / reminder / agent |
| RefID | 引用 ID | 对应类型的实体 ID |
| MetaJSON | 扩展信息 | 其余场景相关元数据（JSON 格式） |

### 各场景 ConvID / RefType / RefID 填充规则

| 大类 | 子类 | ConvID | RefType | RefID |
|------|------|--------|---------|-------|
| main_chat | reat_loop | ✓ | — | — |
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
| reflection | mission_discover | — | mission | mission_id |
| reflection | mission_rank | — | mission | mission_id |
| reflection | mission_run | — | mission | mission_id |
| tool | reminder_gen | — | reminder | reminder_id |
| admin | agent_summary | — | agent | agent_id |
| admin | replay | ✓ | — | — |

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
    SubCategory string                 // reat_loop / verify / ...
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

    record := Record{
        Date:             time.Now(),
        UserID:           coalesce(scenario.UserID, req.UserID),
        Category:         scenario.Category,
        SubCategory:      scenario.SubCategory,
        Model:            t.inner.ModelName(),
        ConvID:           scenario.ConvID,
        RefType:          scenario.RefType,
        RefID:            scenario.RefID,
        PromptTokens:     usage.PromptTokens,
        CompletionTokens: usage.CompletionTokens,
        TotalTokens:      usage.TotalTokens,
        Meta:             scenario.Meta,
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
    Record(record Record)
}

type Record struct {
    Date             time.Time
    UserID           string
    Category         string
    SubCategory      string
    Model            string
    ConvID           string
    RefType          string
    RefID            string
    PromptTokens     int
    CompletionTokens int
    TotalTokens      int
    Meta             map[string]interface{}
}
```

### DB Recorder 实现

```go
type DBRecorder struct {
    repo *service.LlmCostRepo
    ch   chan Record  // 缓冲 channel，解耦写入
}

func NewDBRecorder(repo *service.LlmCostRepo) *DBRecorder {
    r := &DBRecorder{repo: repo, ch: make(chan Record, 1024)}
    go r.worker() // 单 goroutine 消费，顺序写入
    return r
}

func (r *DBRecorder) Record(record Record) {
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
    SubCategory: "reat_loop",
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

```
GET /api/admin/cost/query
```

### 请求参数

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| start | string | 是 | 开始日期 YYYY-MM-DD |
| end | string | 是 | 结束日期 YYYY-MM-DD |
| user_id | string | 否 | 按用户筛选 |
| category | string | 否 | 按大类筛选 |
| group_by | string | 是 | 分组维度：date / category / model / user_id / sub_category |
| sub_group | string | 否 | 二级分组（与 group_by 不同维度） |

### 响应格式

```json
{
  "total_tokens": 1234567,
  "total_calls": 890,
  "groups": [
    {
      "key": "2026-08-12",
      "total_tokens": 50000,
      "prompt_tokens": 30000,
      "completion_tokens": 20000,
      "call_count": 30,
      "sub_groups": [
        {"key": "main_chat", "total_tokens": 30000, "call_count": 20},
        {"key": "post_chat", "total_tokens": 15000, "call_count": 8},
        {"key": "reflection", "total_tokens": 5000, "call_count": 2}
      ]
    }
  ]
}
```

### 典型查询场景

| 图表 | group_by | sub_group | 说明 |
|------|----------|-----------|------|
| 每日消耗柱状图 | date | category | 每天一条柱，按大类堆叠 |
| 分类分布饼图 | category | — | 各分类 token 占比 |
| 模型分布饼图 | model | — | Flash vs Pro 占比 |
| 用户消耗排行 | user_id | — | 各用户 token 消耗排序 |
| 每日按用户 | date | user_id | 每天每用户 token |
| 分类按子类 | category | sub_category | 大类下的子类明细 |

## 实施步骤

### Phase 1: 核心包 `internal/llmcost/`

1. `types.go` — Scenario 结构体 + context 传播函数 + 常量定义（CategoryMainChat 等）
2. `decorator.go` — CostTracker 实现 Chat / ChatSync 拦截
3. `recorder.go` — Recorder 接口 + Record 结构体
4. `db_recorder.go` — 基于 channel 的异步 DB Recorder

### Phase 2: 数据层

5. `model/llm_cost.go` — LlmCostDetail GORM 模型
6. `service/llm_cost_repo.go` — Insert + 通用聚合查询

### Phase 3: API

7. `controller/cost.go` — `/api/admin/cost/query` 接口
8. 注册路由（Admin 路由组）

### Phase 4: 接入

9. `main.go` — AutoMigrate + 创建 DBRecorder
10. `app/init_llm.go` — 包装 CostTracker，调整 InitLLM 签名
11. 21 个调用点标注 ctx

### Phase 5: 验证

12. `go build ./...` + `go test ./...` 全绿
13. 启动服务，发送一条聊天消息，检查 `llm_cost_details` 表有对应记录
14. 调用 `/api/admin/cost/query` 验证查询结果

## 影响范围

| 层级 | 文件 | 改动性质 |
|------|------|----------|
| 新增 | `internal/llmcost/` (4 files) | 新建包 |
| 新增 | `internal/model/llm_cost.go` | 新建模型 |
| 新增 | `internal/service/llm_cost_repo.go` | 新建 Service |
| 新增 | `internal/controller/cost.go` | 新建 Handler |
| 修改 | `cmd/xhan-helper/main.go` | 注册 Model + 创建 Recorder |
| 修改 | `internal/app/init_llm.go` | 增加 Recorder 参数 + CostTracker 包装 |
| 修改 | 21 个调用点 | 各加 1 行 WithScenario |
| 修改 | `internal/app/llm.go` (LLMClients) | 可能需要调整结构体 |

## 风险与边界

- **不追踪 Embedding**：豆包 Embedding API 是独立路径，不在 LLMClient 接口管辖范围。
- **ReactLoop 误标注风险**：标注在 ReactLoop 调用者入口，若 ReactLoop 被新调用者使用且未标注，会记录为 unknown。建议 ReactLoop 构造时校验 ctx 中是否有 Scenario，无则打 warning log。
- **ChatWithRawJSON 不追踪**：当前无生产调用，装饰器中直接透传，不记录。
- **DB Recorder channel 满丢弃**：极端情况（1024 条积压），丢弃并打 log。此规模下几乎不可能触发。
- **user_id 为空的后台任务**：reflection 的部分场景（如全局主题发现）user_id 为空字符串，查询时作为 "system" 归类。
- **gin context vs 标准 context**：Auth 中间件通过 `c.Set("user_id", ...)` 存入 gin context，不会自动进入 `c.Request.Context()`。HTTP Handler 中调用 LLM 时需显式将 `user_id` 填入 `Scenario.UserID` 或通过 `context.WithValue` 注入标准 ctx。
