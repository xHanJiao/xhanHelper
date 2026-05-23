# Phase 10 — 记忆检索增强：Tool + 时间衰减 + 打分明细

> 日期: 2026-05-23 | 状态: 待审批

---

## 背景

借鉴 OpenHuman 记忆系统，xhanHelper 当前检索有三个可改进的点：
1. 检索只在对话开始时预注入一次，LLM 无法主动再搜
2. 检索结果没有时间衰减，200 天前的记忆和 2 天前的权重一样
3. 没有打分明细，调试时不知道为什么某条记忆被捞到

---

## 改动概览

| # | 功能 | 改动文件 | 工作量 |
|---|------|---------|--------|
| 1 | Memory 作为 Tool | 新增 1 个文件 + main.go 注册 | ~80 行 |
| 2 | 时间衰减 | memory_source.go + 配置 | ~30 行 |
| 3 | 打分明细 | model/message.go + orchestrator.go | ~25 行 |

---

## 1. Memory 作为 Tool

### 新增 `server/internal/tool/memory_search.go`

参照 `search_memories.go` 的模式，新增一个内置工具：

```go
type MemorySearchTool struct {
    vectorStore app.VectorStore
    memRepo     *service.UserMemoryRepo
    embedder    *llm.Embedder
}
```

### Tool 定义

| 项 | 值 |
|----|-----|
| Name | `"memory_search"` |
| Description | 在用户的长期记忆中搜索相关信息。当需要了解用户的偏好、习惯、过往决定、行为模式时调用 |
| Function | `search` |
| 参数 | `query`（必填，搜索关键词）、`top_k`（可选，默认 5） |

### Execute 流程

1. 解析 `query` + `top_k`（默认 5，最大 10）
2. `embedder.Embed(query)` → 向量
3. `vectorStore.Search(vec, topK*2, filter(type=mem, user_id=ec.UserID))` — 取 2 倍候选给时间衰减留空间
4. `memRepo.GetByIDs()` 获取完整记忆记录
5. 计算时间衰减，重排序，取 top-K
6. 格式化返回 JSON

### 注册：`server/cmd/xhan-helper/main.go`

```go
reg.Register(tool.NewLoggingWrapper(
    tool.NewMemorySearchTool(infra.VectorStore, memRepo, embedder),
    log.Default(),
))
```

### 去重：避免重复获取已注入的记忆

预注入的记忆已在 system prompt 中，LLM 已看到。`memory_search` 再捞回来是浪费 token。

**方案**：在 `ExecuteContext` 中加一个显式字段，orchestrator 执行工具前填入本轮已注入的记忆 ID 列表，工具内据此过滤。

**`server/internal/tool/tool.go`** — ExecuteContext 加字段：
```go
type ExecuteContext struct {
    Ctx                context.Context
    UserID             string
    InjectedMemoryIDs  []string  // 本轮已注入的记忆 ID，工具可据此去重
}
```

**`server/internal/agent/orchestrator.go`** — 执行工具前填入：
```go
ec.InjectedMemoryIDs = memIDs
```

**`server/internal/tool/memory_search.go`** — Execute 中过滤：
```go
skipIDs := make(map[string]bool, len(ec.InjectedMemoryIDs))
for _, id := range ec.InjectedMemoryIDs {
    skipIDs[id] = true
}
// 在构建结果列表时跳过 skipIDs 中的记忆
```

### 设计决策

- 工具返回格式与预注入格式一致（含时间、topic、唤起次数），让 LLM 能统一理解
- 工具调用也会触发 `BatchUpdateConfirm`，提升被搜索记忆的唤起计数
- LLM 什么时候调这个工具由它自己决定

---

## 2. 时间衰减

### 修改 `server/internal/agent/memory_source.go`

在 `Fetch` 方法中，获取记忆后、构建 ContextPacket 之前，对每条记忆计算 freshness 分数：

**配置文件** — `server/conf/config.yaml`：
```yaml
retrieval:
  decay_rate: 0.02  # 时间衰减系数，越大旧记忆权重越低
```

**`server/internal/config/config.go`** — Config 结构体加字段：
```go
Retrieval struct {
    DecayRate float64 `yaml:"decay_rate"`
} `yaml:"retrieval"`
```

**`server/internal/agent/memory_source.go`** — 构造函数收 `decayRate float64`，Fetch 中使用：
```go
daysAgo := time.Since(m.CreatedAt.In(config.BJ)).Hours() / 24
freshness := 1.0 / (1.0 + daysAgo*s.decayRate)
// decayRate=0.02:
//   1 天 → 0.98, 14 天 → 0.78, 30 天 → 0.63, 100 天 → 0.33
```

**`server/cmd/xhan-helper/main.go`** — 创建 MemorySource 和 MemorySearchTool 时传入 `cfg.Retrieval.DecayRate`。

然后按 `原始向量分 × 0.7 + freshness × 0.3` 得到 finalScore，重新排序。

**不改变检索边界** — 向量搜索仍拉 top-K 候选，时间衰减只在 MemorySource 内部重排序时应用。

---

## 3. 打分明细

### 3.1 新增 `Extra` 字段

**`server/internal/model/message.go`**：
```go
Extra *string `gorm:"type:mediumtext;column:extra" json:"extra,omitempty"`
```

### 3.2 ContextPacket 加 Score

**`server/internal/agent/context_source.go`**：
```go
type ContextPacket struct {
    ID                     string
    Text                   string
    TokenCount             int
    CorrelationCoefficient float64
    Score                  float64  // 新增：向量检索原始分数
}
```

### 3.3 存储打分明细

**`server/internal/agent/orchestrator.go`** — `runReActLoop` 中保存 user message 的位置，与 `MemoryIDs` 逻辑并列：

```json
// extra 字段内容
{
  "retrieval_scores": [
    {
      "id": "mem-uuid-1",
      "vector_score": 0.82,
      "freshness_score": 0.95,
      "final_score": 0.86
    },
    {
      "id": "mem-uuid-2", 
      "vector_score": 0.71,
      "freshness_score": 0.30,
      "final_score": 0.52
    }
  ]
}
```

---

## 文件改动清单

| 文件 | 操作 | 说明 |
|------|------|------|
| `server/internal/tool/memory_search.go` | **新增** | MemorySearchTool 实现（~90 行） |
| `server/internal/tool/tool.go` | 修改 | ExecuteContext 加 `InjectedMemoryIDs`（+1 行） |
| `server/internal/model/message.go` | 修改 | 加 `Extra` 字段（+1 行） |
| `server/internal/agent/context_source.go` | 修改 | ContextPacket 加 `Score float64`（+1 行） |
| `server/internal/agent/memory_source.go` | 修改 | Fetch 中计算时间衰减 + 填 Score（~25 行） |
| `server/internal/agent/note_source.go` | 修改 | Fetch 中填 Score（一致性，不衰减）（~5 行） |
| `server/internal/agent/orchestrator.go` | 修改 | 保存 Extra + 工具执行前设 InjectedMemoryIDs（~20 行） |
| `server/internal/config/config.go` | 修改 | 加 `Retrieval.DecayRate` 配置项（+3 行） |
| `server/conf/config.yaml` | 修改 | 加 `retrieval.decay_rate`（+2 行） |
| `server/cmd/xhan-helper/main.go` | 修改 | 注册 MemorySearchTool + 传递 decayRate（+6 行） |

---

## 验证方式

1. `make test` — 全量 API 测试通过
2. 手动验证：
   - 发送消息 → 查数据库 user message 的 `extra` 字段，确认有 `retrieval_scores` JSON
   - 对话中说「帮我搜一下之前的记忆」→ LLM 调用 `memory_search` 返回结果
   - 数据库中有一条创建于半年前的记忆和一条昨天的记忆，聊相关话题 → 昨天的排在前面
