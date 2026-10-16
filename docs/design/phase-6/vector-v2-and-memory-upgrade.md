# 向量 v2 改造 + 记忆表升级

> 日期: 2026-05-15 | 关联: Phase 6 反思 Agent

---

## 一、动机

1. **向量检索效果差**：对整篇笔记/记忆做全量 embedding，一条长文本包含多个主题时向量语义模糊，检索相关性低
2. **LLM 关键词搜索不适应向量**：LLM 不了解向量搜索原理，用空格分隔的关键词列表搜向量，精度差
3. **记忆缺少分类属性**：Notes 有 topic/tags，Memories 之前只有 summary，无法按主题聚合筛选
4. **检索只靠向量，缺少精确匹配**：缺乏 topic/tags 的结构化筛选辅助

## 二、改动总览

### 2.1 记忆表升级

| 列 | 类型 | 说明 |
|----|------|------|
| `topic` | varchar(100) | 主题词，与 notes 对齐 |
| `tags` | varchar(500) | 逗号分隔标签 |
| `facts` | text | 支持细节列表（JSON 数组） |

> 后续变更（2026-10-16）：`facts` 的语义已从「支持细节」改为**用户原话逐字摘录**（硬校验），见 `docs/design/memory-facts-verbatim/design.md`。本表的类型与列名不变。

提取 prompt 同步更新：去掉 `category` 字段，新增 `tags` 字段。`formatMemoryValue` 改为 `[topic] summary`。

存量数据通过 `cmd/memory-rebuild` 全量重跑。

### 2.2 Embedding 维度降到 1024

豆包 embedding API 支持 `dimensions` 参数。句级 chunk 语义集中，1024 维足够，存储和计算量减半。

### 2.3 全篇 embedding → sentence-level chunk

**旧方案**：一条 note/memory 对应一个 embedding 向量。

**新方案**：中文分句（按 `。！？\n` 切分），每句一个 chunk，独立 embedding。

**新增表 `embedding_chunks`**：

| 列 | 说明 |
|----|------|
| id | UUID |
| parent_type | "note" / "memory" |
| parent_id | 源实体 UUID |
| user_id | 用户 ID |
| chunk_index | 句子序号 |
| content | 句子原文 |

chunk 向量以 `chunk:<parentType>:<parentID>/<index>` 为 key 存入 Embedding 表 + 内存向量库。

**搜索流程**：向量搜 chunk → 按 parentID 去重 → DB 取源全文 + chunk 匹配片段 → 返回带原文引用。

### 2.4 新旧嵌入路径统一

`storeMemory`（memory extractor）和 `embedNote`（note tool）从整篇 embed 改为调用 `ChunkBuilder.BuildChunks()`，统一走 chunk 嵌入路径。不再有整篇嵌入。

### 2.5 检索工具 v2

 `search_notes_v2` / `search_memories_v2`：chunk 级检索 → 去重 → 返回 parent 全文 + 匹配 chunk 片段。

共享逻辑抽到 `search_chunk.go`：
- `searchChunks()` — 向量搜 + 去重
- `dedupParents()` — 从 chunk 命中提取 parentID
- `parseChunkParentID()` — 解析 `chunk:<type>:<id>/<n>` 格式
- `formatMatchedChunks()` / `groupChunksByParent()` / `marshalResult()` / `emptyResult()`

### 2.6 Rebuild 工具

`cmd/memory-rebuild/main.go`：独立二进制。
1. 清理旧 chunk + embedding 数据
2. 遍历所有会话重新提取记忆（使用新版 prompt）
3. 对所有 notes + memories 做 chunk 嵌入
4. 退出

编译：`GOOS=linux GOARCH=amd64 go build -o memory-rebuild cmd/memory-rebuild/main.go`

### 2.7 Warmup

新增 `warmupChunkEmbeddings()`：启动时从 Embedding 表加载 `entity_type="chunk"` 的向量到内存，从 EntityID 解析 parent_type/parent_id，配合 UserID 列重建 metadata。

## 三、不再保留

- `search_messages` — 已删除（单条消息信息量低，易产生幻觉）
- 旧 `search_notes`/`search_memories` — 保留文件但未注册，可用于对比 v1/v2 效果
- 整篇 embedding 逻辑 — `storeMemory`/`embedNote` 已替换

## 四、后续方向

- **Layer 1/2 检索**：先用 topic/tags 聚合统计（`get_note_stats()` / `get_memory_stats()`）给 LLM 选 topic，再取该 topic 下全文
- **全量灌入模式**：如果 notes+memories 总量小（<100 条），绕开向量检索，直接全量发给 LLM
