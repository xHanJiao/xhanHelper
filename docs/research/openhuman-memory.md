# OpenHuman 记忆系统分析

> 仓库: [tinyhumansai/openhuman](https://github.com/tinyhumansai/openhuman)
> 日期: 2026-05-22
> 主题: 记忆架构、混合检索、知识图谱

---

## 一、项目概况

OpenHuman 是一个 Rust 写的 AI 个人助手，单体应用（Tauri 桌面端），SQLite 本地存储。记忆系统是其核心模块之一，代码量很大（`src/openhuman/memory/` 下有约 70 个 .rs 文件）。

---

## 二、整体架构

### 2.1 三层存储模型

数据按**用途和检索需求**分成三层，不同层走不同的写入和检索路径：

| 存储层级 | 用途 | 写入路径 | 检索方式 |
|---------|------|---------|---------|
| **Document** | 重要内容，需要语义搜索 | `put_doc` → 向量嵌入 + 后台图谱提取 | 混合打分（graph+vector+keyword+freshness） |
| **Episodic** | 每轮对话原文 | 自动写入 FTS5 全文索引 | 全文搜索 + freshness 加权 |
| **KV** | 配置、计数器、状态 | `kv_set` → 不嵌入，不提取图谱 | 精确 key 查找 |

写入时提供三种路径，按内容重要性选择：
- `put_doc()` — 嵌入 + 后台图谱提取，适合值得语义搜索的内容
- `put_doc_light()` — 不嵌入、不提取图谱，适合高频/临时数据（如屏幕截图摘要每几秒写一次）
- `ingest_doc()` — 同步完成全部流程，适合需要立即可检索的关键内容

### 2.2 Namespace 隔离

数据按 namespace 逻辑隔离，而不是按表物理隔离：

| Namespace | 内容 |
|-----------|------|
| `skill-{id}` | 每个 skill 的原始状态（如 Notion、Gmail） |
| `global` | 从对话中提取的用户画像（偏好、目标、实体） |
| `conversations` | 对话上下文 |
| `autocomplete-memory` | 自动补全历史 |
| `vision` | 屏幕截图摘要 |

不同数据源写入同一个存储引擎但不同 namespace。检索时可按 namespace 过滤，也可跨 namespace 查询。优点是新数据源接入时不需要建新表，只需约定一个新 namespace。

### 2.3 Memory 作为 Agent Tool

OpenHuman 把 memory 暴露为 LLM 可调用的工具。Agent 在对话中可以主动调用 `memory.recall("query")` 再搜一次，不是只依赖对话开始时系统注入的那一批上下文。检索变成双向的——系统预注入 + Agent 按需主动查询。

---

## 三、混合检索打分

这是与 xhanHelper 最大的差异点。xhanHelper 只做向量余弦相似度。OpenHuman 用 **5 个信号加权合成**最终分数：

```
// 基础信号权重（无 episodic 信号时）
GRAPH_WEIGHT   = 0.55   // 知识图谱的实体/关系匹配
VECTOR_WEIGHT  = 0.30   // 向量余弦相似度
KEYWORD_WEIGHT = 0.15   // 关键词词汇匹配

// 有 episodic 信号时重新分配
GRAPH_WEIGHT_WITH_EPISODIC   = 0.45
VECTOR_WEIGHT_WITH_EPISODIC  = 0.25
KEYWORD_WEIGHT_WITH_EPISODIC = 0.10
EPISODIC_WEIGHT              = 0.20

// 召回（recall）模式使用不同的权重组合
RECALL_PRIORITY_WEIGHT  = 0.45
RECALL_GRAPH_WEIGHT     = 0.30
RECALL_FRESHNESS_WEIGHT = 0.25
```

每个检索命中返回结构化的打分明细：

```
score_breakdown: {
    graph_relevance:     0.85,
    vector_similarity:   0.42,
    keyword_relevance:   0.30,
    episodic_relevance:  0.0,
    freshness:           0.67,
    final_score:         0.71
}
```

Graph 是主信号，向量只是辅助。这跟 xhanHelper 纯向量检索的思路完全相反。

---

## 四、知识图谱

### 4.1 是什么

不是 Neo4j 那种重型图数据库。是一个轻量的**实体-关系三元组存储**，存在 SQLite 里。

### 4.2 写入流程（离线异步）

文档存入后，后台 ingestion pipeline 自动提取：

```
原始文本: "我决定以后新项目都用 Postgres，不用 MySQL 了"

提取实体 → USER, Postgres, MySQL, 新项目
提取关系 → USER DECIDED (用Postgres), USER REJECTS MySQL
去重合并 → 同一条关系被多次提取时累加 evidence_count
```

### 4.3 检索流程

```
用户 query: "哪个数据库比较好"

1. 从 query 提取实体: "数据库"
2. 查图谱: "数据库" 关联 Postgres, MySQL
3. 从 Postgres/MySQL 反查文档 → 找到相关记忆
4. 图谱路径作为 graph_relevance 信号（权重 0.55）
5. 叠加 vector + keyword + freshness → 最终分数
```

### 4.4 它解决了什么

向量相似度找的是**文本像不像**，图谱找的是**概念有没有关联**。两个换了说法但指向同一事物的文档，向量可能距离很远，图谱知道有关联。

```
用户搜: "数据库选型"

文档 A: "Postgres 查询优化器很成熟"     → 向量 0.72（语义相似）✓
文档 B: "新项目用 Postgres，不用 MySQL" → 向量 0.45（措辞不搭）✗

但在图谱里:
  Postgres — IS_A → Database → MATCHES → "数据库选型"
  → 文档 B 的 graph_relevance = 0.85 → 排到 A 前面
```

图谱的价值在于多跳匹配——文档不需要提到查询词，只要关联的实体能通过图路径到达查询词就行。

### 4.5 局限

- 构建成本高：实体和关系提取要走 LLM 或有噪声的正则
- 去重和合并复杂：同一个实体可能有多个名字（Postgres / PostgreSQL / PG）
- 跳数越多信噪比越低：OpenHuman 也不做深度多跳，主要是 1-2 跳
- 如果 embedding 模型足够强，图谱的边际收益有限。好的 embedding 已经隐式编码了大量概念关联

---

## 五、Memory 核心接口

```rust
pub trait Memory: Send + Sync {
    fn name(&self) -> &str;
    async fn store(&self, namespace, key, content, category, session_id) -> Result<()>;
    async fn recall(&self, query, limit, opts: RecallOpts) -> Result<Vec<MemoryEntry>>;
    async fn get(&self, namespace, key) -> Result<Option<MemoryEntry>>;
    async fn list(&self, namespace, category, session_id) -> Result<Vec<MemoryEntry>>;
    async fn forget(&self, namespace, key) -> Result<bool>;
    async fn namespace_summaries(&self) -> Result<Vec<NamespaceSummary>>;
    async fn count(&self) -> Result<usize>;
    async fn health_check(&self) -> bool;
}
```

`MemoryCategory` 分为三类：
- `Core` — 长期基础事实、用户偏好、永久决定
- `Daily` — 日常活动日志、临时状态
- `Conversation` — 对话上下文

`RecallOpts` 支持：namespace 过滤、category 过滤、session 过滤、最低分数阈值、跨 session 召回开关。

---

## 六、MemoryLoader：检索如何进入 Agent 上下文

`DefaultMemoryLoader` 是记忆和 Agent 之间的桥梁：

```
load_context(memory, user_message):
  1. Working memory 召回 — 从 "working.user.*" key 加载用户画像
  2. Prior conversations — 从之前的高重要度对话中加载跨 session 事实
  3. Cross-chat context — 跨线程的 FTS5 命中文档
  4. 预算控制 — max_context_chars: 2000, limit: 5, min_relevance_score: 0.4
  5. 返回格式化文本拼入 system prompt
```

关键设计：检索结果被预算严格限制——最多 2000 字符、5 条命中、最低 0.4 分。防止上下文膨胀。

---

## 七、对 xhanHelper 的启发

| 维度 | OpenHuman 做法 | xhanHelper 现状 | 可借鉴程度 |
|------|---------------|----------------|-----------|
| 检索打分 | 5 信号混合（graph + vector + keyword + episodic + freshness） | 纯向量余弦相似度 | **高** |
| 写路径分层 | put_doc / put_doc_light / ingest_doc | 统一路径 | 中 |
| Memory 作为 Tool | LLM 可主动调 memory.recall | 只在对话开始预注入 | **高** |
| Namespace | 逻辑分区，统一存储 | 按表物理分区 | 低（当前规模够用） |
| 知识图谱 | 实体-关系三元组，离线提取 | 无 | 低（边际收益 vs 成本不划算） |
| 打分明细 | 每个命中带 per-signal score | 无 | 中 |
| 跨 session 召回 | 支持跨对话搜索历史事实 | 部分支持（HistorySource） | 中 |

最值得做的三件事：

1. **混合打分** — 在现有向量检索上加一层关键词匹配和 freshness 时间衰减，不需要图谱也能显著提升检索质量
2. **Memory 作为 Tool** — 让 Agent 在对话中可以主动调用 memory_search 再搜一次，而不是只依赖开始时的预注入
3. **打分明细** — 检索结果带回每个信号的贡献值，方便理解为什么某条记忆被捞到，也方便调参
