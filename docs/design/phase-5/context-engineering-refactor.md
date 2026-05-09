# 上下文工程重构 — Retriever 职责拆分 + ContextSource 抽象

> 日期: 2026-05-08 | 状态: 方案已确认，待实施

---

## 一、现状问题

### 1.1 Retriever 职责过重

```
Retriever.Retrieve()
  ├── embedder.Embed(userMessage)        ← 嵌入向量，不属于「检索」
  ├── vectorStore.Search(vec)            ← 检索（核心职责）
  ├── go saveMsgEmbedding(vec)           ← 异步存储，不属于「检索」
  ├── searchMemories(results)            ← 格式化为 prompt 文本
  ├── searchHistoryMessages(results)     ← 同上
  └── searchNotes(results)               ← 同上
```

- 嵌入、存储、检索三者耦合在一个结构体中
- 调用方无法控制嵌入/存储的时机
- `saveMsgEmbedding` 用 `go` 异步逃逸了 context 生命周期

### 1.2 三种数据拉取没有统一抽象

记忆/历史/笔记三者的检索逻辑散落在 Retriever 的方法中，各自无法独立复用。

### 1.3 向量化覆盖不完整

| 数据源 | 写入向量 | Warmup | 重启后可用 |
|--------|---------|--------|-----------|
| 记忆 | ✅ `MemoryExtractor.storeMemory` | ✅ `Retriever.Warmup` | ✅ |
| 历史消息 | ✅ `Retriever.saveMsgEmbedding`（仅 user） | ❌ | ❌ |
| 笔记 | ✅ `NoteTool.embedNote` | ❌ | ❌ |

assistant 消息（含 `reasoning_content`）从未被向量化。

### 1.4 消息入库位置

全部在 `orchestrator.go` 的 `runReActLoop`：

| 角色 | 方法 |
|------|------|
| `user` | 直接写 `msgRepo.Create` |
| `assistant` | `saveAssistantMsg()` |
| `tool` | `saveToolMsg()` |

---

## 二、目标

1. **拆分 Retriever 职责** — 嵌入向量和存储上移到 Orchestrator，Retriever 只做检索+格式化
2. **抽象 ContextSource 接口** — 记忆/历史/笔记三种拉取统一为同一接口的不同实现
3. **完善向量化覆盖** — 笔记补充 Warmup，重启后可用；历史消息 Warmup 后续再做
4. **保持现有行为不退化**

---

## 三、新增类型

```go
// ContextPacket — 上下文片段，来自任意数据源
type ContextPacket struct {
    ID                     string
    Text                   string
    TokenCount             int
    CorrelationCoefficient float64
}

// ContextSource — 上下文数据源接口
type ContextSource interface {
    Name() string
    Fetch(ctx context.Context, queryVec []float64, userID, convID string,
        threshold float64, limit int) ([]ContextPacket, error)
    Warmup(ctx context.Context, embedder *llm.Embedder)
}
```

### 三种实现

| 实现 | Name | 向量库过滤 | 数据源 | Warmup |
|------|------|-----------|--------|--------|
| `MemorySource` | `"memory"` | `type=="mem"` | `UserMemoryRepo` | ✅ 有实现（从 Retriever 迁出） |
| `HistorySource` | `"history"` | `type=="msg"` | `MessageRepo` | no-op 占位（后续补） |
| `NoteSource` | `"note"` | `type=="note"` | `NoteRepo` | ✅ 有实现（本次新增） |

每个 Source 持有 `*VectorStore` + 自己的 repo，独立完成：向量搜索 → DB 查内容 → 返回 `[]ContextPacket`。

---

## 四、组件关系

```
Orchestrator
  ├── embedder.Embed(userMessage)        ← 上移
  ├── retriever.Retrieve(vec, params)    ← 接收向量做检索
  ├── go saveMsgEmbedding(vec)           ← 上移
  └── buildSysPrompt()

Retriever
  sources []ContextSource
  ├── Retrieve() → 遍历 sources，汇总 []ContextPacket
  └── FormatContext(packets) → prompt 文本（追加到 system prompt）

    ┌─────────┬──────────┬──────────┐
    ▼         ▼          ▼
 MemorySource  HistorySource  NoteSource
```

---

## 五、Note Warmup 补充

### 5.1 NoteRepo 新增方法

```go
// ListAllPaginated 分页获取所有笔记（用于 warmup 加载向量库）
func (r *NoteRepo) ListAllPaginated(ctx context.Context, limit, offset int) ([]model.Note, error)
```

### 5.2 NoteSource.Warmup 逻辑

与 MemorySource.Warmup 一致：

```
分页遍历所有笔记
  ├── 已有持久化 embedding → 直接 load 进向量库
  └── 没有 embedding → embedder.Embed(note.Content) → 写入向量库 + 回写 DB
```

---

## 六、文件变更

| 文件 | 变更 |
|------|------|
| `agent/context_source.go` | **新增** — `ContextPacket` + `ContextSource` 接口定义 |
| `agent/memory_source.go` | **新增** — `MemorySource` 实现，含 Warmup（从 Retriever 迁出） |
| `agent/history_source.go` | **新增** — `HistorySource` 实现，Warmup 占位 |
| `agent/note_source.go` | **新增** — `NoteSource` 实现，含 Warmup |
| `agent/retriever.go` | **重写** — 移除 embedder/embed/存储/三类搜索，改为遍历 sources |
| `agent/orchestrator.go` | **改** — 新增 embedder 字段，buildSysPrompt 中嵌入+检索+存储 |
| `service/note_repo.go` | **改** — 新增 `ListAllPaginated` 方法 |
| `cmd/xhan-helper/main.go` | **改** — 构造 Source → Retriever → Orchestrator，warmup 调用 |

### 不改

- `agent/vector_store.go`
- `agent/extractor.go`
- `llm/embedding.go`
- `controller/chat.go`
- `tool/note.go`

---

## 七、不做（后续）

- History 的 warmup 实现
- assistant 消息的向量化
- 历史消息的智能过滤（需先解决 user turn 粒度问题 + reasoning_content 约束）
