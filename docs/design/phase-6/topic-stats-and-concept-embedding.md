# Topic Stats 统计工具 + Concept 嵌入表

> 日期: 2026-05-16 | 状态: 设计阶段

---

## 一、动机

1. **向量搜索不适合主题发现和分析**：ThemeDiscoverer 用自然语言问句 `"用户最近反复讨论的个人话题"` 搜向量，本质是主题归纳而非语义匹配，效果差
2. **Notes/Memories 已有 topic/tags 结构化数据**：可以直接做聚合统计，不需要向量检索
3. **Topic 聚类可以合并相似话题**：向量在这里起的作用是聚类预处理，不是检索
4. **同一个 topic 字符串反复 embed 浪费 token**：应该只 embed 一次并复用

## 二、整体架构

```
ThemeDiscoverer                     ThemeAnalyst
    ↓                                   ↓
get_topic_stats()                  get_topic_detail(topic)
    ↓                                   ↓
ConceptEmbedding 表                 直接读 notes + memories
    ↓
实时聚类（余弦相似度）
    ↓
7 维度统计摘要 → LLM
```

## 三、ConceptEmbedding 表

### 3.1 模型

```go
type ConceptEmbedding struct {
    ID         string    `gorm:"primaryKey;size:20" json:"id"`       // 雪花 ID（有序）
    Type       string    `gorm:"uniqueIndex:idx_type_value_user,priority:1;size:20;not null" json:"type"` // "topic" | "tag"
    Value      string    `gorm:"uniqueIndex:idx_type_value_user,priority:2;size:200;not null" json:"value"` // "职业规划"
    UserID     string    `gorm:"uniqueIndex:idx_type_value_user,priority:3;index:idx_type_user,priority:1;size:36;not null" json:"user_id"`
    ParentType string    `gorm:"size:20" json:"parent_type"`        // "note" | "memory"（来源追溯）
    ParentID   string    `gorm:"size:36" json:"parent_id"`          // 来源实体的 UUID
    CreatedAt  time.Time `gorm:"autoCreateTime" json:"created_at"`
}
```

**索引**：
- `idx_type_value_user`：唯一索引，防重复 + 存在性检查
- `idx_type_user (type, user_id)`：列表查询（列出某用户所有 topic/tag）

**ID**：使用 `idgen.Next()` 雪花 ID（有序、短小、索引友好），而非 UUID。

**parentType/parentID**：记录该概念首次由哪个 note/memory 引入，方便追踪来源。

**与 embbeds 关系**：
- ConceptEmbedding 是元数据实体（存 type、value、来源）
- 向量存 `embeddings` 表：`entity_type="concept"`, `entity_id=<ConceptEmbedding.ID>`（雪花 ID）
- vectorStore key：`concept:<ConceptEmbedding.ID>`

**EntityType 新增**：`model.EntityTypeConcept = "concept"`

### 3.2 写入时机

创建 note 或 memory 时，异步处理主题和标签：

**Topic**：
- 检查 concept WHERE type="topic" AND value="<topic>" 是否存在
- 不存在 → 新插入一条 + embed → 写入 embeddings 表 + vectorStore

**Tags**（逗号分隔字符串）：
- `split(",")` → 每个 tag 独立处理
- 检查 concept WHERE type="tag" AND value="<tag>" 是否存在
- 不存在 → 新插入一条 + embed → 写入 embeddings 表 + vectorStore

**聚类时 topic 和 tag 混合计算**——tags 的向量参与同一套相似度合并逻辑。例如 "职场" tag 和 "职业规划" topic 可能因语义接近归入同一 cluster。

```go
// 伪代码
func ensureTopicEmbedded(topic, userID string) {
    id := "topic:" + topic
    if exists, _ := conceptRepo.Exists(id); exists {
        return
    }
    conceptRepo.Create(&ConceptEmbedding{ID: id, Type: "topic", Value: topic, UserID: userID})
    vec, _ := embedder.Embed(ctx, topic)
    vectorStore.Upsert("concept:" + id, vec, map[string]string{
        "type": "concept",
        "concept_type": "topic",
        "user_id": userID,
    })
    embeddingRepo.Upsert(&Embedding{
        ID: id, EntityType: "concept", EntityID: id, UserID: userID,
        Embedding: &embStr, Model: embedder.ModelName(),
    })
}
```

### 3.3 生命周期

- **创建**：首次遇到新 topic 时自动创建
- **清理**：不主动删除（孤儿概念不影响查询）
- **Warmup**：启动时加载 `entity_type="concept"` 的 embedding → vectorStore

## 四、Topic Stats 工具（get_topic_stats）

### 4.1 工具签名

```
get_topic_stats() → topic 统计摘要
```

### 4.2 执行流程

```
1. 读取所有 ConceptEmbedding WHERE type="topic"
2. 从 vectorStore 读取每个 topic 的向量
3. 聚类：两两余弦相似度 > 0.85 → 归为同一 cluster
4. 对每个 cluster，从 notes + memories 表聚合统计维度
5. 返回 JSON
```

### 4.3 输出格式

每个 cluster 的所有统计维度（time_range、conversation_count、source、tags、weekly_distribution）均基于 cluster 内**所有 topic** 汇总得出，而非仅主 topic。

```json
{
  "clusters": [
    {
      "topic": "职业规划",
      "cluster": ["求职", "面试", "职业发展", "离职"],
      "time_range": "2026-04-20 → 2026-05-14",
      "conversation_count": 5,
      "source": {"notes": 3, "memories": 9},
      "tags": ["裸辞", "面试", "薪资", "offer"],
      "weekly_distribution": [3, 2, 5, 7]
    },
    {
      "topic": "睡眠管理",
      "cluster": ["睡眠", "早醒", "褪黑素"],
      ...
    }
  ],
  "total_topics": 12,
  "total_clusters": 5
}
```

### 4.4 聚类算法

- 读取所有 concept 向量（type="topic" 和 type="tag"，N 个）
- 两两计算余弦相似度
- 相似度 > 0.85 → 归入同一 cluster
- 用并查集合并。取出现次数最多的 topic 名作为 cluster 主名称，tags 不参与命名但计入关联 tags 列表
- 时间复杂度 O(N²)，N < 100 时 < 1ms

### 4.5 SQL 统计查询（概念实现）

**关键原则**：所有统计是对 cluster 里所有 topic 汇总（`WHERE topic IN ('求职','面试','职业发展','离职')`），不是只统计 cluster 主 topic。

```sql
-- notes 侧：一个 cluster 下所有 topic 汇总
SELECT COUNT(*) as cnt, MIN(created_at) as first, MAX(created_at) as last
FROM notes WHERE user_id = ? AND topic IN ('求职','面试','职业发展','离职');

-- memories 侧同理
SELECT COUNT(*) as cnt, MIN(created_at) as first, MAX(created_at) as last
FROM user_memories WHERE user_id = ? AND topic IN ('求职','面试','职业发展','离职');

-- tags 汇总：去重列表
SELECT DISTINCT tags FROM notes WHERE user_id = ? AND topic IN (...)
UNION SELECT DISTINCT tags FROM user_memories WHERE user_id = ? AND topic IN (...);
```

**读放大注意**：每个 cluster 需要 3 次 SQL（notes 统计 + memories 统计 + tags 汇总）。N 个 cluster = 3N 次查询。当 cluster 数量增长到数十个时，需要合并为一次全量查询后在应用层分组聚合。MVP 阶段 cluster 数量少（<10），直接循环查可接受。

## 五、Topic Detail 工具（get_topic_detail）

### 5.1 工具签名

```
get_topic_detail(topic) → 该 topic 下所有 notes + memories 全文
```

### 5.2 执行流程

```
1. 通过聚类信息，获取该 cluster 下所有 topic 名
2. 从 notes 表 WHERE topic IN (...) 拉全文
3. 从 memories 表 WHERE topic IN (...) 拉全文
4. 返回按时间排序的全文列表
```

### 5.3 输出格式

```json
{
  "topic": "职业规划",
  "cluster": ["求职", "面试", "职业发展"],
  "items": [
    {"type": "note", "id": "abc", "topic": "求职", "content": "...", "created_at": "..."},
    {"type": "memory", "id": "def", "topic": "职业发展", "content": "...", "created_at": "..."}
  ]
}
```

## 六、与现有检索工具的替换关系

| 旧 | 新 | 用途 |
|----|----|------|
| `search_notes_v2` | `get_topic_stats` + `get_topic_detail` | ThemeDiscoverer 和 ThemeAnalyst 都改用新工具 |
| `search_memories_v2` | 同上 | 同上 |
| `get_bill_stats` | 保留不变 | 账单统计 |

`search_*_v2` 不删除，保留用于 A/B 对比。

## 七、Prompt 变更

### 7.1 ThemeDiscoverer

旧：LLM 调 `search_notes_v2("用户最近反复讨论...")` → 向量返回 → 分析

新：LLM 调 `get_topic_stats()` → 拿到聚类后的 topic 分布 → 如果某 cluster 看起来值得追踪 → 调 `get_topic_detail(topic)` 获取全文 → 判断是否提名

### 7.2 ThemeAnalyst

旧：LLM 调 `search_notes_v2("睡眠管理 睡眠质量")` → 向量返回 → 分析

新：LLM 调 `get_topic_detail("睡眠管理")` → 聚类后拉同 cluster 下所有相关 topic 的全文 → 分析

## 八、监控

所有新工具通过 `tool.NewLoggingWrapper` 注册，自动接入 `monitor.IncToolCallWithLatency`，监控面板可查看调用次数、错误率、延迟。

## 九、索引变更

| 表 | 操作 | 索引 | 说明 |
|----|------|------|------|
| `user_memories` | 新增 | `idx_user_topic (user_id, topic)` | 对齐 notes，stats 查询 GROUP BY topic 需要 |
| `concept_embeddings` | 新增 | `idx_type_value_user (type, value, user_id)` UNIQUE | 写入前查重 |
| `concept_embeddings` | 新增 | `idx_type_user (type, user_id)` | 聚类时列出所有 topic/tag |

## 十、未来优化（本次不做）

**提示词 YAML 集中管理**：当前 prompt 硬编码在 Go 源文件中（theme_analyst.go、scorer.go 等 6 处），分散难维护。改为 `conf/prompts.yaml` 集中存放，启动时加载，无需重编译即可调优。

**记忆渐进式提取**：当前 MemoryExtractor 每次把完整会话（最多 200 条消息）传给 LLM 提取记忆。随着会话增长，重复传输已提取过的消息浪费 token。优化方案：

- 只传上次最新提取的记忆（1-3 条）+ 未提取的增量消息 + 一小部分已提取过的旧消息做上下文
- 预期节省 50-70% 的提取 token

## 十一、实施清单

| 文件 | 操作 | 说明 |
|------|------|------|
| `model/concept_embedding.go` | 新 | ConceptEmbedding 模型 |
| `model/embedding.go` | 改 | 加 EntityTypeConcept |
| `service/concept_embedding_repo.go` | 新 | CRUD（Exists、Create、ListByType） |
| `tool/get_topic_stats.go` | 新 | stats 工具 |
| `tool/get_topic_detail.go` | 新 | detail 工具 |
| `agent/chunk_builder.go` | 改 | 建 chunk 时触发 topic 异步嵌入 |
| `cmd/xhan-helper/main.go` | 改 | AutoMigrate 加 ConceptEmbedding + warmup concept embedding + 注册新工具 |
| `agent/theme_analyst.go` | 改 | prompt 工具名替换 |
| `agent/theme_discover.go` | 改 | prompt 工具名替换 + 配合 stats 两阶段 |
