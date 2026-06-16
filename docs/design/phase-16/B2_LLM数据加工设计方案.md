# B2 LLM 数据加工设计方案

> 2026-06-15 · 离线试题集 Phase B2
>
> 在 B1 导出的冻死数据上，用 LLM 进行增量加工：精选 query + 检索预筛 + 五档预标注。

---

## 一、目标

| 步骤 | 做什么 | 输入 | 输出 |
|------|--------|------|------|
| **精选 query** | LLM 从 raw_queries 中挑 N 条有价值的 | `raw_queries`（全量，跳过已标） | `annotation_session_queries` 记录 |
| **检索预筛** | 从 embedding_cache 读向量 → 本地 cosine → top 20 | query + corpus 向量 | 每条 query 的 top 20 候选 corpus |
| **预标注** | LLM 对 top 20 候选打 0~4 分 | query + 候选 corpus 内容 | `eval_ground_truth`（label_source=llm） |

## 二、增量构建

不一次全量标注，渐进式推高。每次开一个 `annotation_session`：

```
session_1: 选 10 条 → LLM 预标注 → ground_truth 有 10×M 行
session_2: 选 40 条（跳过已标）→ ground_truth 增长到 50×M
session_3: 选 150 条 → ground_truth 增长到 200×M
...
```

新 session 选 query 时，跳过 `eval_ground_truth` 中已有记录的任何 query。

### CLI

```bash
go run ./cmd/eval-label/ \
  --project 2026-06-15-retrieval-baseline \
  --session batch-1 \
  --query-limit 10 \
  --candidate-limit 20
```

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `--project` | 必填 | 数据集项目名 |
| `--session` | 必填 | 标注批次名（如 batch-1） |
| `--query-limit` | 10 | 本轮精选条数 |
| `--candidate-limit` | 20 | 每条 query 取 top N 候选 |
| `--step` | `all` | `select` / `prelabel` / `all` |
| `--strategy` | `default` | LabelStrategy 名称 |
| `--retry-session` | false | 若 session 已存在，重新处理 |

### 运行模式

```
# 全量：精选 + 预标注
eval-label --project ... --session batch-1 --query-limit 10

# 只精选
eval-label --project ... --session batch-1 --step select

# 只预标注（query 已精选完）
eval-label --project ... --session batch-1 --step prelabel

# 重跑 session
eval-label --project ... --session batch-1 --retry-session
```

## 三、LabelStrategy 接口

与 B1 ExportStrategy 同模式——核心逻辑走接口，后续换模型/换 prompt 只需实现新策略。

```go
type QueryRecord struct {
    ID      string
    Content string
}

type CorpusRecord struct {
    ID      string
    Content string
}

type LabelResult struct {
    CorpusID  string
    Relevance int // 0~4
}

type LabelStrategy interface {
    Name() string
    // 从候选 query 列表里挑 N 条有检索价值的，返回 query ID
    SelectQueries(ctx context.Context, llm LLMClient, queries []QueryRecord, limit int) ([]string, error)
    // 对一条 query 的 top-K 候选 corpus 做五档评分
    PreLabel(ctx context.Context, llm LLMClient, query string, candidates []CorpusRecord) ([]LabelResult, error)
}
```

### 默认实现：DefaultLabelStrategy

- 模型：DeepSeek Flash
- 模式：同步调用（ChatSync），非流式

## 四、详细流程

### 4.1 流程总览

```
raw_queries (1496 条，只读快照)
    │
    ▼ Step 0: 排除已标的 query
    │   SELECT id FROM raw_queries
    │   WHERE id NOT IN (SELECT DISTINCT query_id FROM eval_ground_truth)
    │   → 得出未标注候选池
    │
    ▼ Step 1: SelectQueries
    │   LLM 批量评估，从候选池里挑 N 条
    │   → INSERT INTO annotation_session_queries
    │
    ▼ Step 2: 检索预筛
    │   每条 query：从 eval_embedding_cache 读向量
    │   每条 corpus：从 eval_embedding_cache 读向量
    │   → 本地 cosine 算 top 20
    │
    ▼ Step 3: PreLabel
    │   LLM 逐条对 top 20 候选评分 0~4
    │   → INSERT OR REPLACE INTO eval_ground_truth
```

### 4.2 Step 1：LLM 精选 query

**Prompt 核心结构：**

- 角色：你是检索评测数据集构建助手，负责从用户真实消息中挑选适合做检索评测的 query
- 任务：从以下候选 query 列表中，选出最有检索价值的 N 条
- 标准：
  1. 问题语义清晰完整
  2. 在 corpus（笔记 + 记忆库）中可能存有答案
  3. 覆盖不同话题（记账、健康、职业、财务、社交等）
  4. 难度分层（有精确匹配的、有需要推理的、有跨条目综合的）
  5. 排除纯闲聊（问好、表情、无意义信息）
- 输入格式：`query_id | query内容`
- 输出格式：JSON 数组 `["query_id_1", "query_id_2", ...]`

**发送策略：**
- 候选量小（< 200 条）：一次发送全部
- 候选量大（≥ 200 条）：分批发送，每批 200 条，分别精选后合并去重

### 4.3 Step 2：检索预筛（方案 A）

仅使用 full-content 嵌入向量做本地 cosine 相似度计算：

1. 从 `eval_embedding_cache` 读 query 向量（source_type='query', model_name=当前模型）
2. 从 `eval_embedding_cache` 读所有 corpus 向量（source_type='corpus', model_name=当前模型）
3. 计算 query 向量与每个 corpus 向量的 cosine 相似度
4. 取 top 20 作为 LLM 预标注的候选集

- B1 已预灌所有 full-content 向量，无需调 API
- 以后换模型时，缺失的向量调 API 补算并缓存
- 这是简化方案（方案 A），只取 full-content 向量，不模拟 chunk 级别检索

### 4.4 Step 3：LLM 五档预标注

**Prompt 核心结构：**

- 角色：你是检索相关性标注助手
- 任务：对给定的用户 query，评判每条 corpus 候选的相关性，打出 0~4 分
- 标准（五档）：

| 分数 | 标签 | 含义 |
|------|------|------|
| 0 | irrelevant | 完全无关 |
| 1 | tangential | 同话题领域但无实质帮助 |
| 2 | partial | 弱相关，提供线索但不足以回答 |
| 3 | relevant | 直接相关，内容可回答该问题 |
| 4 | perfect | 完美命中，最优答案 |

- 输入格式：
  ```
  [Query]
  {用户消息}

  [Candidates]
  corpus_1: {内容片段}
  corpus_2: {内容片段}
  ...
  ```
- 输出格式：JSON 数组
  ```json
  [
    {"corpus_id": "xxx", "relevance": 3},
    {"corpus_id": "yyy", "relevance": 0},
    ...
    {"corpus_id": "zzz", "relevance": 4}
  ]
  ```

**注意：LLM 预标注不保证 100% 准确，后续 B3 人工复核修正。**

## 五、嵌入向量复用策略

| 场景 | 嵌入来源 | 是否花钱 |
|------|----------|----------|
| 首次导出 | MySQL `embeddings` 表 → `eval_embedding_cache` | 否 |
| B2 预筛 | 从 `eval_embedding_cache` 读，本地 cosine | 否 |
| 换模型评估 | cache miss → 调 API → 写缓存 | 首次花钱 |
| B3/B4 标注 | 不涉及嵌入 | 否 |

## 六、文件结构

```
server/internal/eval/
├── export.go                    # B1: ExportStrategy + AllPastDataStrategy
├── schema.go                    # B1: DDL + insert helpers
├── loader.go                    # B1: LoadDataset
├── embedding.go                 # B1: 从 MySQL embedding 表灌缓存
├── label_strategy.go            # B2: LabelStrategy 接口
├── label_query_select.go        # B2: SelectQueries 实现（prompt + LLM 调用）
├── label_prelabel.go            # B2: PreLabel 实现（prompt + LLM 调用）
├── label_cosine.go              # B2: 本地 cosine 相似度预筛
├── label_runner.go              # B2: 编排逻辑
└── llm_client.go                # B2: LLM 调用抽象（包装现有 llmClient）

server/cmd/eval-label/
└── main.go                      # B2: CLI 入口
```

## 七、增量构建记录

`eval_metadata` 表中记录每批 session 信息：

| key | value 示例 |
|-----|-----------|
| `session_batch-1_count` | `10` |
| `session_batch-1_date` | `2026-06-15T12:00:00Z` |
| `session_batch-2_count` | `50` |
| `session_batch-2_date` | `2026-06-16T12:00:00Z` |

`annotation_sessions` 表本身也是完整记录。

## 八、总结

B2 核心：注释 session 化 → 增量构建 → LLM 精选 + 本地 cosine 预筛 + 五档预标注。

跑通 10 条即可验证全链路（精选 prompt + 预筛 cosine + 预标注 prompt + DB 写入），验证后逐步扩展。
