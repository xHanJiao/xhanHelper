# Phase 8 — 依赖注入重构：从手动接线到 Config Struct

> 日期: 2026-05-20 | 状态: 方案已确认，待实施

---

## 一、问题回顾

当前后端所有依赖通过 `main.go` 手动接线，存在三个突出问题：

1. **构造函数参数爆炸** — `agent.New` 13 个参数、`controller.NewRouter` 12 个参数、`NewMemoryExtractor` 10 个参数。新增一个依赖要改所有调用方。
2. **Setter 注入** — `CronRunner` 构造后有 5 个 `Set*` 调用，`ReflectionOrchestrator` 有 3 个。构造函数被拆成多次调用，依赖关系不透明。
3. **context.Value 传递依赖** — `ReactLoop`/`Secretary`/`Scorer` 等组件通过 `CtxTraceRepo(ctx)` 从 context 获取 `traceRepo`，而非构造函数注入。`tool` 包中同一概念（VectorStore）定义了 3 套不同签名的接口，仅为了绕过循环依赖。

## 二、核心思路

**不引入外部 DI 框架**（Wire/FX 对单人项目过度工程化）。用两个轻量手段解决问题：

- **Config struct 分组** — 把相关依赖打包成 struct，构造函数收 1-2 个 struct 即可
- **模块化 init** — `main.go` 拆分出每个包的初始化入口函数

## 三、Config struct 设计

### 3.1 `app.Infra` — 基础设施

```go
// server/internal/app/infra.go
type Infra struct {
    DB          *gorm.DB
    Embedder    llm.Embedder
    VectorStore VectorStore  // interface，app 包内定义
}
```

被所有 Repo 和 Agent 组件共享。

### 3.2 `app.LLMClients` — AI 客户端

```go
// server/internal/app/llm.go
type LLMClients struct {
    Flash  llm.LLMClient
    Strong llm.LLMClient  // 可选，Pro 模型
}
```

仅 agent 层需要。

### 3.3 不做的事

- **不建 Repos struct** — 18 个 repo 打包在一起只是把 main.go 的 18 行平移到另一个文件，没有减少认知负担。各构造函数需要哪个 repo 就单独收哪个。
- **不做全局 DI 容器** — 保持构造函数显式传参，依赖关系一目了然。

## 四、接口去重

目前 `tool` 包中定义了大量 mini-interface 来绕过与 `agent` 包的循环依赖，同一概念被定义了多次：

| 概念 | tool 包定义 | agent 包实现 |
|------|------------|-------------|
| VectorStore (Search) | `VectorSearcher` (search_notes.go) | `agent.VectorStore` |
| VectorStore (Upsert) | `VectorStorer` (note.go) | `agent.VectorStore` |
| VectorStore (SearchSimilar) | `VectorStore` (tool_retriever_strategies.go) | `agent.VectorStore` |
| Chunk 切分 | `ChunkEmbedder` (note.go) | `agent.ChunkBuilder` |
| 概念嵌入 | `ConceptEmbedDeder` (note.go) | `agent.ConceptEmbedService` |
| 主题嵌入 | `TopicEmbedder` (get_topic_stats.go) | `agent.ThemeEmbedder` |
| 主题向量写入 | `ThemeVectorWriter` (theme.go) | `agent.ThemeEmbedder` |
| 定时器 | `CommitmentTimer` (reminder.go) | `agent.CommitmentTimer` |
| MCP 代理 | `MCPToolCaller` (mcp_tool.go) | `mcp.Manager` |
| 工具缓存 | `CacheReader`/`CacheWriter` (dynamic.go) | `service.ToolRepo` |

**策略**：所有能合并的接口迁入 `app` 包，作为唯一的规范定义。`tool` 和 `agent` 都从 `app` import，消除重复。`tool` 包只保留调用方需要的接口子集（接口隔离）。

合并后 `app` 包保留以下接口：

```go
// VectorStore 向量存储（合并 VectorSearcher + VectorStorer + VectorStore）
type VectorStore interface {
    Search(queryVector []float64, topK int) ([]SearchResult, error)
    Upsert(id string, vector []float64) error
    SearchSimilar(query string, threshold float64, limit int) ([]SimilarResult, error)
}

// ChunkEmbedder 文本切分
type ChunkEmbedder interface { ... }

// ConceptEmbedder 概念嵌入
type ConceptEmbedder interface { ... }

// ... 等
```

## 五、Setter 消除

所有 setter 分两类处理：

### 5.1 配置值 → Config struct

`CronRunner` 的 5 个 setter、`ReflectionOrchestrator` 的 3 个 setter、`ThemeDiscoverer` 的 `SetUpgradeThresholds` 全部是配置值，收进各自构造函数的 Config struct：

```go
type CronRunnerConfig struct {
    DiscoverSchedules  []string
    DeliverInterval    time.Duration
    PostChatDelay      time.Duration
    RunTimeout         time.Duration
    MemoryAutoExtract  bool
}
```

### 5.2 可选依赖/回调 → 构造函数参数

`Orchestrator.SetPromptResolver` 和 `Orchestrator.SetOnChatEnd` 实际上每次都会被设置，不是真正的「可选」。直接移入构造函数。

## 六、context.Value 消除

当前 `traceRepo` 和 `traceID` 通过 `context.WithValue` 在 `ReflectionOrchestrator` → `ReactLoop` → `Secretary`/`Scorer` 链中传递：

```go
// 现状
ctx = WithTraceInfo(ctx, traceRepo, traceID)
loop := NewReactLoop(..., CtxTraceRepo(ctx), CtxTraceID(ctx))
```

改为构造函数注入：

```go
// 重构后
loop := NewReactLoop(llmClient, tools, prompt, maxIterations, userID, label, traceRepo, traceID)
```

`Secretary` 和 `Scorer` 本身不需要 `traceRepo`——它们通过 `ExecuteContext` 获取。改为在 `ExecuteContext` 中直接携带 `TraceRepo` 字段（可选），而非从 `context.Context.Value` 取。

## 七、迁移顺序

渐进式迁移，每步完成后跑 `make test`：

| 步骤 | 范围 | 内容 |
|------|------|------|
| 1 | `app` 包 | 新建包，定义 `Infra`、`LLMClients`、合并后的共享接口 |
| 2 | `tool` 层 | 各 tool 构造函数改为收 Config struct + 接口 |
| 3 | `agent` 子组件 | ChunkBuilder、ConceptEmbedService、ThemeEmbedder 等收 Config struct |
| 4 | `agent` 核心 | Orchestrator、MemoryExtractor、ReflectionOrchestrator 等大构造函数切到 Config struct，消除 setter 和 context.Value |
| 5 | `controller` + `main.go` | Router 收 Config struct，main.go 精简为组装 Infra → 调用各模块入口 |

## 八、风险与边界

- **不改变对外 API** — 这是纯内部重构，HTTP 接口、SSE 事件、请求/响应格式都不变
- **不改变工具注册机制** — Registry 保持动态加载逻辑不变，两个 Registry（主 + reflection）都保留
- **`ExecuteContext` 保持现状** — 它传递的是运行时的 userID 和 context，不是构造时依赖
- **`gorm.DB` 仍作为 Infra 的一部分** — 所有 Repo 从 `Infra.DB` 创建，保持一致性
