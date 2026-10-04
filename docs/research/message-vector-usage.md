# 历史消息向量用途调研（msg 向量该不该留）

**日期**: 2026-10-04
**背景**: 内存 `VectorStore` 里 `msg` 类向量（本地实况 743 条，占 ~12628 KB 的 ~24%）是为历史消息语义检索而建。
  `vector.message_retention_days: 14` 只保留近 14 天，动机是省内存。
  用户体感「消息这个域，语义检索不如直接关键词搜索给力」，故复核消息向量当前到底被谁消费、能否取消。
**结论**: 历史消息**已经几乎不走语义**（`unified_search` 语义/关键词默认域都不含 history，需显式指定）；
  此结论仅为现状盘点，本次**未改代码**。

---

## 一、历史消息检索的现状链路

1. **每轮自动注入 RAG：已关。** 主检索器 `ContextRetriever` 的源只有 `{theme, memory, note}`——
   `server/internal/appinit/agents.go:209`，没有 history。配置注释亦写明「历史消息不再重放检索注入」
   （`server/conf/config.yaml:153`）。
2. **`HistorySource`（带 round 展开 + assistant + 时间衰减）的 `Fetch` 是死代码。**
   仅 `Warmup` 被调用一次（`agents.go:53`），作用是把消息向量灌进内存；`Fetch` 无任何非测试引用
   （`server/internal/agent/retrieval/history_source.go:203`）。设计文档亦已标注「`HistorySource.Fetch` 死代码」
   （`docs/design/vector-memory-governance.md:13`）。
3. **真正被 LLM 用的是 `SimpleHistorySource`**，注册在 `unified_search` 的 `history` 域
   （`agents.go:69`；实现 `retrieval/simple_history_source.go`）。语义/关键词两路，按需调用。
4. **history 域默认不参与检索。** `unified_search` 语义与关键词的默认 `types` 均为
   `memory/note/summary/theme`，history 只有调用方显式传 `types:["history"]` 才会召回
   （`server/internal/tool/unified_search.go:212`、`:297`）。
5. **只有用户消息有向量。** 写入仅一处 `Orchestrator.saveUserMsgEmbedding`（chat 时，仅 `role=user`，
   hidden round 跳过）——`server/internal/agent/orchestrator.go:504`、`:2177`。assistant 消息无向量。

## 二、消息向量的全部消费者

| 消费者 | 位置 | 性质 |
|---|---|---|
| `simple_history` 语义检索 | `retrieval/simple_history_source.go:26` | 历史语义本身（history 域） |
| 主题多查询 | `retrieval/theme_source.go:43`（`SearchOptions.HistoryEmbeddings`，取近 10 条用户消息向量） | 把消息向量当**现成缓存**复用，非历史检索 |
| 摘要相关性检查 | `agent/orchestrator.go:1016`（`embedHistoryUserMessages`） | 同上 |
| 主题匹配 | `agent/state_updater.go:186`（`matchThemesFromMessages`） | 同上，后处理 |

其中后三者依赖 `Orchestrator.embedHistoryUserMessages`（`orchestrator.go:436`：按消息 ID 从 VectorStore 取向量）；
`MessageSearchTool` 虽持有 `embedder`/`vectorStore` 字段，但文件内无任何调用（**死字段**，
`tool/message_search.go:38-39`）。

## 三、DB 消息 embedding 行

- **唯一消费者是 `HistorySource.Warmup`**（`history_source.go:31-78`，按保留窗加载进内存）。
  非测试代码里没有别处读 `entity_type=message` 的 embedding 行。
- 定时逐出：`CronRunner.evictStaleVectors` → `evictType` 每日删内存向量 **并删除同窗 DB 行**
  （`agent/cron_runner.go:598-631`）。故 `message_retention_days` 不只影响内存，DB 行也会删。
- **`messages` 表原文不删**：按时间/ID/关键词查（`message_search`、`unified_search` keyword）直查表，
  绕开向量，不受 14 天限制。14 天是**语义召回窗**，不是历史保留窗。

## 四、若后续要动，分两档（本次未实施）

**档 A（小，零副作用）— 历史只走关键词。**
从 `unified_search` 摘掉 `simple_history` 语义源，history 域仅保留关键词
（`unified_search.go:526 searchHistoryByKeyword`）。消息向量照旧写，主题/摘要/主题匹配不受影响。

**档 B（大，动主链路）— 彻底停消息向量。**
停写（内存 + DB）、删 `HistorySource`/warmup、移除 history 语义路径。收益：启动内存 -743 条、DB 不再积压。
代价：主题多查询、摘要相关性、后台主题匹配三处退化为只用当前 `queryVec`（都是「复用缓存」，非历史检索）。

**折中**：只停 warmup 与 DB 持久化、保留 chat 时内存 upsert——会话内主题/摘要窗口仍有效，重启后无历史消息向量。
