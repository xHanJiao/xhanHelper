# 向量内存治理

> 2026-09-18

**估算（定稿）：2 面 / 3 缝（0 条需先建探针）/ 0 次真机往返 / 1 个待拍决定** → 约 4-6h

## 背景

内存 `VectorStore` 启动全量加载，实测占 ~167MB（~20.4k 向量 × 1024 维 × 8B）。逐类审计发现大量冗余与死数据：

| 类型 | 条数 | 内存 | 消费者 | 结论 |
|---|---|---|---|---|
| message | 6291 | 50MB | 仅 `simple_history`（unified_search history 域）；`HistorySource.Fetch` 死代码 | 保留 14 天 |
| chunk·note | 4007 | 32MB | note.semantic_search + EntitySource | 短文本(均值149字) full 已覆盖 → 停建 |
| chunk·summary | 2695 | 22MB | EntitySource | 同上(177字) → 停建 |
| chunk·memory | 839 | 6.7MB | EntitySource | 1.04 chunk/父 = full 重复 → 停建 |
| chunk·theme | 244 | 2MB | ThemeSource 主检索 | 保留（state_text 均值242字） |
| concept | 3649 | 29MB | tag 2931 从不检索；topic 726 仅 get_topic_stats 取向量 | 移出内存，topic 改 DB 批量读；停建 tag |
| note/memory/summary full | 2554 | 20MB | 检索/去重/摘要附着 | 保留 |
| theme/tool | 133 | 1MB | 有用 | 保留 |
| mission | 5 | ~0 | 功能废弃，`MissionSource` 从未实例化 | 清空 + 删死代码 |

## 目标

- 向量内存 ~167MB → ~12MB。
- 不改变主链路检索语义（短文本退化为 full 单路；topic 聚类改 DB 读取）。
- 顺带 `VectorStore` 内部改 float32 存储，所有向量内存再腰斩。

## 方案

### 0. VectorStore float32
- `vectorEntry.Vec []float32`；内部 `cosineSimilarity32`（避免与 orchestrator 的 float64 同名函数冲突）。
- 公开签名不变（`[]float64` 边界转换）；filter 的 `Entry.Vec` 传 nil（无 filter 读它）。
- 新增 `DeleteByIDs(ids)` 供批量逐出。

### 1. message 保留 14 天 + 定时逐出
- `EmbeddingRepo.ListByTypeSince` 供 warmup 时间过滤。
- `EmbeddingRepo.ListEntityIDsByTypeBefore` + CronRunner 每日 `evictStaleVectors` 逐出内存（DB 行保留）。

### 2. chunk 只保留 theme
- `ChunkBuilder.disabledParents`（nil=全允许，兼容测试）；`BuildChunks`/`DiffAndBuildChunks` 短路。
- `warmupChunkEmbeddings` 跳过禁用父类型，防回灌。
- 启动幂等清理：删除 memory/note/summary 的 chunk 行（两张表）。
- 配置 `vector.chunk_disabled_parents`。

### 3. summary 保留 30 天
- `EntitySource.ListAll(ctx, since, limit, offset)`；summary 用 `ListAllSince`，memory/note 忽略 since。
- 逐出走阶段 1 的 cron。

### 4. mission 清空 + 死代码
- 删 `warmupMissionEmbeddings`、`MissionSource`、`MissionDiscoverer`、`MissionEmbedder` 及其接线。
- `MissionRunner` / controller 暂留（空表 no-op）。
- 一次性 migration：`vector.purge_missions_once=true` 时删 mission embedding + missions 表。

### 5. concept 移出内存 + 停建 tag
- `EmbeddingRepo.ListByEntities` 单查批量取 topic embedding。
- `get_topic_stats` 改批量读 DB 聚类，去掉 vectorStore/topicEmbedder 依赖。
- `ConceptEmbedService` 去掉 vectorStore 写入；`EnsureTagsEmbedded` no-op（tags 已存实体字符串）。
- 删 `warmupConceptEmbeddings`。
- 一次性清理 tag concept 行与 embedding。

## 风险

- note/summary 失去 chunk 路 → 语义召回略降（短文本影响小），上线后观察。
- float32 精度 ~1e-7 扰动，阈值边界极小概率翻转。
- 一次性清理必须**先于 warmup**，否则旧 chunk 被回灌。
