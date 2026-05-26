# Phase 11: 向量历史消息 Warmup

## 目标

为 HistorySource 实现向量匹配基建，包括：embedding warmup、Fetch 多条返回 + Ranker 打分、上下文窗口重构、检索权重全面配置化。

## 背景

每条用户消息在 chat 时已自动嵌入（`saveUserMsgEmbedding`），同时写入内存 VectorStore 和 `embeddings` 表（`entity_type="message"`）。但 VectorStore 是内存存储，服务重启后向量数据丢失。MemorySource 和 NoteSource 都有 Warmup 从 DB 恢复向量，HistorySource 的 Warmup 是空桩。

同时 HistorySource.Fetch 存在 bug：用一个 +3/-4 的 context window 只返回一条结果，而且底层 SQL 查询拿不到匹配消息所在位置，导致大多数匹配被静默丢弃。

## 改动清单

### 1. `retrieval/history_source.go` — 核心改造

**结构体变更：**
- 删 `enabled bool`
- 加 `embeddingRepo *service.EmbeddingRepo`
- 加 `ranker *Ranker`

```go
type HistorySource struct {
    vectorStore   VectorStore
    msgRepo       *service.MessageRepo
    embeddingRepo *service.EmbeddingRepo
    ranker        *Ranker
}
```

**Warmup 实现：**
- `embeddingRepo.ListByType(ctx, EntityTypeMessage, 100, offset)` 分页加载
- 反序列化 `Embedding` 字段 → `[]float64`
- `vectorStore.Upsert("msg:"+emb.EntityID, vec, map[string]string{"type": "msg", "user_id": emb.UserID})`
- 不设 `conv_id` 元数据
- 失败直接 `log.Fatalf`

**Fetch 重写：**
- 向量搜索去掉 `conv_id` 元数据过滤，只保留 `type=="msg"` + `user_id`
- 循环遍历所有结果（不再提前 return 一条）
- 对每条命中的消息：`GetByID` → 后置 `msg.ConversationID == convID` 跳过
- `GetMessagesSince` 获取从匹配消息开始的后续消息 → `FilterMessageRound` 截取单轮 Q&A
- 用 `FormatMessageRound` 格式化
- 用 Ranker 打分（向量分 + 关键词 bigram + 新鲜度时间衰减）
- 返回 `[]ContextPacket`

### 2. 新建 `retrieval/message_round.go` — 从 `tool/message_search.go` 抽取公共函数

```go
// FilterMessageRound 从消息列表中截取以 matchedID 开头的一轮 Q&A
// 规则：保留 user + assistant 消息，跳过 tool/internal，遇到下一个 user 消息时截断
func FilterMessageRound(msgs []model.Message, matchedID string) []model.Message

// FormatMessageRound 将一轮对话格式化为文本
func FormatMessageRound(msgs []model.Message, convTitle string) string

const MaxContentLen = 500
```

### 3. `tool/message_search.go` — 改用 retrieval 包

- 删除 `filterMessageRound`、`formatMessageRound`、`maxContentLen`
- 改为 `retrieval.FilterMessageRound`、`retrieval.FormatMessageRound`、`retrieval.MaxContentLen`

### 4. `config/config.go` — 新增 SourceWeights

```go
type SourceWeights struct {
    VectorWeight    float64 `yaml:"vector_weight"`
    KeywordWeight   float64 `yaml:"keyword_weight"`
    FreshnessWeight float64 `yaml:"freshness_weight"`
    DecayRate       float64 `yaml:"freshness_decay"`
}

type RetrievalConfig struct {
    DecayRate float64       `yaml:"freshness_decay"` // 保留作为默认值
    Memory    SourceWeights `yaml:"memory"`
    Note      SourceWeights `yaml:"note"`
    History   SourceWeights `yaml:"history"`
}
```

验证和默认值逻辑在 `config.go` 的 `validate()` 中处理。

### 5. `conf/config.yaml` — 新增检索权重配置

```yaml
retrieval:
  freshness_decay: 0.02           # 保留作为默认值
  memory:
    vector_weight: 0.5
    keyword_weight: 0.3
    freshness_weight: 0.2
    freshness_decay: 0.01
  note:
    vector_weight: 0.5
    keyword_weight: 0.3
    freshness_weight: 0.0
    freshness_decay: 0
  history:
    vector_weight: 0.6
    keyword_weight: 0.3
    freshness_weight: 0.1
    freshness_decay: 0.005
```

### 6. `cmd/xhan-helper/main.go` — 连线

- 从 `cfg.Retrieval.Memory/Note/History` 读取权重，构造对应的 `*Ranker`
- `NewHistorySource(vs, msgRepo, embeddingRepo, historyRanker)`
- 调用 `historySource.Warmup(ctx, embedder)`
- HistorySource 暂不接入 Retriever（等 system prompt 精简后再开）

### 7. `service/message_repo.go` — 可选优化

如果 HistorySource.Fetch 中 GetByID 加载了不需要的大字段（RequestJSON 等），新增轻量查询方法，只 SELECT `id, conversation_id, user_id, role, content, created_at`。

## 不改的文件

- `memory_source.go` / `note_source.go` — 已在构造时接收 `*Ranker`，权重通过 main.go 配置即可
- `bigram.go` / `rank.go` — Ranker 已支持权重可配

## 不在本次范围

- HistorySource 不接入 Retriever
- 不含 Phase 2（为无 embedding 的消息补算向量），因为所有用户消息在 chat 时已自动嵌入

## 权重设计理由

| 维度 | 历史消息 | 记忆 | 笔记 | 理由 |
|------|----------|------|------|------|
| vector | 0.6 | 0.5 | 0.5 | 语义相似是历史消息检索的核心价值 |
| keyword | 0.3 | 0.3 | 0.3 | bigram 重叠对中文有效，三源一致 |
| freshness | 0.1 | 0.2 | 0 | 旧对话仍有参考价值，衰减低于记忆；笔记无衰减 |
| freshness_decay | 0.005 | 0.01 | 0 | 历史消息随时间衰减更慢 |
