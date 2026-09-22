# Phase-10 Token 优化：调研结论与方案

> 后续对标：opencode 的 prune / compaction 与「子任务隔离」的对比结论见
> `docs/design/agent-tooling-research/opencode-context-management.md`（2026-09-22）。

## 背景

对话上下文（历史 + 检索注入）占 LLM 输入 token 的大头。本 phase 基于线上数据 dump
（`server/data/mysqldumps/xhan_helper-20260815-184015.sql`）调研 token 消耗构成，
并确定三个优化任务：摘要附着去重（A）、公共资源区重构（B，后续 phase）、工具清理（C）。

数据说明：`messages` 表有近 30 天完整数据；`llm_cost_details` 表 8-12 才上线，
token 用量数据只有 8-12 ~ 8-15 四天（440 次 main_chat 调用）。

## 一、调研结论

### 1. 窗口大小分布（近30天，1580 条 user 消息）

| 指标 | 值 |
|------|-----|
| median | 6 轮 |
| mean | 8.2 |
| p90 / p95 / p99 | 17 / 21 / 29 |
| max | 40（全量 52） |

直方图：0-19 轮占 93%，20-39 轮 6.7%，40+ 仅 1 条。窗口远达不到 `max_window_rounds: 800` 上限，
**用轮数做压缩阈值没有意义，token 才是正确的阈值**。

### 2. 轮数 → token 映射（真实 join 261 个调用）

线性拟合：`prompt_tokens ≈ 30,026 + 7,746 × 轮数`

| window | n | median pt |
|--------|---|-----------|
| 5 轮 | 97 | 80,157 |
| 10 轮 | 23 | 106,694 |
| 15 轮 | 5 | 135,537 |
| 20 轮 | 3 | 154,253 |

- 固定开销 ≈ 30k tokens（system prompt + 工具 schema + 检索注入）
- 每轮 ≈ 7.7k tokens —— 其中大头是历史 user 消息的 XMLContext 注入（见下）

### 3. XML 放大实测（关键发现）

近30天 user 消息 `xml_context` 大小：

| | chars | ≈ tokens |
|---|---|---|
| xml_context median | 7,128 | ~5,700 |
| original_content median | 32 | ~25 |

窗口 median 6 轮 → 每轮 prompt 中 **~34k tokens 是重复的历史 XML**（6 条 × 5.7k），
占输入近 45%。幻觉防护（信息在历史里重复）的代价是 token 放大 6 倍。

块构成占比（单条消息）：topics 38%（median 2,841 chars，25-41 个活跃主题全注入）、
summaries 36%（median 4,261）、notes 10.7%、profile 4.4%、history 4.1%、reminders 2.5%。

### 4. Token 用量实况（近4天 main_chat）

- 440 calls，median 42.7k，p90 124k，max 191,701
- **hit 率 91%**（hit 21.8M / miss 2.1M）
- 4 天花费 ≈ $2.47（按 deepseek-chat 公开价：hit $0.07/M, miss $0.27/M, out $1.10/M）
- 42 个调用 prompt > 128k，14 个 > 160k（v4 上下文 1M，不构成当前风险）

### 5. 主动压缩（anchored compaction）模拟

借鉴 opencode `session/compaction.ts`：历史压成固定模板摘要 + 保留 tail 原文。
按真实调用序列模拟（压缩调用按 hit 价读 head + 前缀断裂一次 miss + 摘要输出 1.5k）：

| 阈值 S | 压缩次数 | 节省 | 净收益(4天) | ROI |
|--------|---------|------|------------|-----|
| 40k | 241 | $1.24 | −$1.7 | ~0.45 |
| 60k | 151 | $1.01 | −$0.9 | ~0.55 |
| 100k | 74 | $0.59 | −$0.7 | ~0.5 |

**结论：主动压缩在经济上是亏的**——省下的 token 91% 是缓存命中的廉价 token（$0.07/M），
而压缩自身要付读取 + 摘要输出 + 前缀断裂。且 v4 上下文 1M，不存在溢出风险。
**压缩作为"保险丝"暂缓**（若未来上下文吃紧再启用，阈值建议 100k+）。

### 6. 编码格式对比（XML vs JSON）

近30天实测：JSON 紧凑版仅省 7.6%（7,145 → 6,606 chars），JSON 缩进反而多 14.2%。
XML 的结构开销只占 2.4%，97.6% 是内容本身。**保持 XML 编码**，token 大头在内容，
在 quota/去重/注入策略上解决。

### 7. 主题匹配验证

活跃主题全量注入（25-41 个/条），但**带详情（匹配）的 topic median 0 个**，
77.3% 的消息 0 个、21.3% 1 个、max 3；session 内去重带详情 ≤3。
→ 一个会话窗口内实际谈到的主题先验很少，unmatched 主题注入是 2.8k chars 的浪费。

## 二、任务 A：摘要附着去重

- `buildSummaryPackets` 按 `summary_attach_count: 2` 取最近 2 条
- 复用 `getOrEmbedSummary` 缓存向量（VectorStore/DB），pairwise 余弦 ≥ 0.85 丢弃较旧者
- 实际附着 1~2 条（相邻摘要是增量合并生成，内容高度重叠，判重命中率高）
- 阈值 0.85 与记忆去重口径一致

## 三、任务 C：工具清理（本 phase 落地）

### C1. 删除长期未用的工具（基于 30 天调用统计）

| 处理 | 对象 | 依据 |
|------|------|------|
| 删整个工具 | `search_memories_v2` | 30天 0 次，最后使用 06-29，已被 unified_search 取代 |
| 删整个工具 | `mission` | 30天 6 次（list 0 次），价值低 |
| 删函数 | `note.get_note_topics` | 30天 0 次，最后 06-28（代码中已不存在，无需改动） |
| 删函数 | `agent.get_agent_versions` / `agent.rollback_agent` | 30天 0 次，最后 07-13 |

保留：chart、plan（计划模式）、task（子任务派发）、datetime 及全部高频工具。

### C2. 检索工具统一（unified_search 收编）

unified_search 覆盖 memory/note/summary/theme/history 五域（语义+关键词），
各独立检索工具与其重叠部分全部下线，检索统一走 unified_search：

| 处理 | 对象 | 依据 |
|------|------|------|
| 删整个工具 | `memory_search` | 检索函数全被 unified 覆盖；残余 get_by_ids/list_topics 仅 4 次/30天 |
| 删整个工具 | `search_notes_v2` | 30天 6 次，chunk 检索被 unified(note 域) 覆盖 |
| 删死文件 | `search_memories.go` / `search_notes.go` | 从未注册，0 调用 |
| 删整个工具 | `tool_retriever` | 工具发现元工具，native FC 下 schema 全在 tools 数组里，长期 0 次调用（已被 HideTool） |
| 瘦身 | `message_search` | 删 semantic_search/search_by_keyword，保留时间/ID 查询（search_by_time/search_by_range/get_by_ids/count_messages，22 次/30天） |
| 瘦身 | `get_summary` | 删 search_summaries/search_by_keyword，保留 get_summary/query_by_time（15 次/30天） |
| 瘦身 | `theme_search` | 删 semantic_search/search_by_keyword，保留 get_detail/list_active_themes/update_theme（65 次/30天） |
| 保留 | `note.semantic_search` | 高频（47 次/30天），note 管理工具核心能力 |

**连带改造**：
- 内部子系统（MissionRunner/StateUpdater/ThemeAnalyst）检索工具集替换为 unified_search 实例，
  相关 prompt 文本同步更新
- web_explore 子 agent 白名单 search_notes_v2 → unified_search
- orchestrator 删除 memory_search 专属的 injectExcludeMemIDsForMemSearch/injectExcludeIDs
  （防重复注入机制，unified 为 LLM 主动检索不进 XML，无需此逻辑）
- prompt-eng 路径：toolDefinitionsProvider 改用 GeneratePrompt（全量完整定义，无 search 机制）；
  删除 GeneratePromptWithOverview/GenerateImportantRules/importantRulesProvider 死代码；
  deepseek.go 收尾文本去掉 tool_retriever 引用
- seeder：新增 unified_search 种子、更新 message_search 描述；retiredBuiltinTools 增加
  memory_search/search_notes_v2/search_memories/search_notes/tool_retriever
- corePrinciples 第 6 条检索优先：检索工具描述改为 unified_search

**效果**：主对话 tools 数组从 ~23 工具减至 ~19，且 message_search/get_summary/theme_search
的 schema 体积显著缩小（各删 2 个函数）。检索入口唯一化降低 LLM 工具选择混淆。

## 四、任务 B：公共资源区重构（已实施，2026-08-15）

实施要点落地情况：B0（theme 只注入 matched）、B1（历史消息简化重放 `[MM-DD HH:mm] 原文`，`SimpleUserContent`）、
B2（`ctxflow/resource_zone.go`：增量解析上一条 XMLContext + ID/内容双索引去重 + quota + 预算帽 4000）、
B3（reminder 不进资源区每轮重查）、配置 `retrieval.resource_zone`。单测覆盖 parse/merge/quota/budget/round-trip。

### 目标

解决 XML 放大（34k tokens/轮 重复历史注入）。方案（缓存推演已确认）：

1. **历史消息简化重放**：历史 user 消息 = `[MM-DD HH:mm] original_content`，
   不再发送完整 XMLContext（DB 不动，只改重放路径）
2. **增量资源区**：最新 user 消息内维护 `<资源区>`，只解析**上一条消息**的 XMLContext
   （encoding/xml 反解，零额外 DB 读——`computeWindowSize` 已持有 lastUserMsg）
   + 本轮检索结果 merge
3. **quota 制**（数据驱动）：note 10 / memory 8 / summary 2 / topic 5（只收 matched）；
   reminder 不进资源区、每轮重查（upcoming 1 + expired 1，已确认 ack/cancelled/mentioned 过滤闭环）
4. **预算帽** 总预算 7000 chars 兜底 + per-type 预算（profile 1600/note 3000/summary 800/memory 600/topic 200，
数据依据：近30天实测 note p75 272×10、memory 62×8、summary 265×2、topic 16×5、profile 块 p90 1407）；
两阶段：先每类独立截断（防跨类挤占），再总预算按砍序 memory→note→theme→summary→profile 兜底
5. **ID 去重**：summary/reminder/theme 已有 id；memory/note 新增 `id,attr`（旧 XML 降级内容匹配）；
   summary 另走 0.85 余弦判重
6. **时间线序**：历史累计在前 + 新检索追加在后（不重算相似度，quota 后条目少排序收益小）
7. **theme 注入策略**：只注入 matched（带 StateText 详情的），unmatched 不再注入
   （数据：77% 消息 0 个匹配；补偿路径 `theme_search.list_active_themes`）
8. **profile 进资源区**：稳定画像层（field key 去重、值更新替换），不参与条数 quota，
   预算砍序最低优先级（最后砍画像）
9. **分组编码**：资源区按类型分组单块输出（每类型一个块，description 不重复 N 次）
10. 解析失败打日志降级（禁止静默）；不做旧数据兼容

### 缓存推演结论

DeepSeek 前缀缓存是连续前缀匹配。资源区无论放哪（历史前/后），跨轮必然整体 miss
（新历史消息插在其前破坏前缀）。**跨轮能命中的只有 [system + 简化历史] 前缀**；
资源区唯一吃到缓存的场景是同一轮 ReAct 迭代间（前缀完全一致）。
因此"追加式排序以获得公共前缀"无跨轮收益，采用时间线序即可。

### 预期收益（6 轮窗口稳态）

- 每轮 prompt ≈ 47k vs 现状 76k（省 ~38%）
- miss ≈ 4.5k vs 现状 7.1k chars/轮（topics 只注入 matched 后）
- hit 省 ~24k/轮，miss 省 ~2.6k/轮

## 附录：分析脚本与数据

分析脚本为临时 heredoc Python（解析 mysqldump INSERT 语句），未入库。
关键数据表：`messages`（window_size 在 extra JSON）、`llm_cost_details`
（category=main_chat, sub=re_act_loop/sub_agent_run）。

价格假设：deepseek-chat 公开价（hit $0.07/M, miss $0.27/M, out $1.10/M），
v4 实际价目以 `llm_cost_details` 真实用量数据为准，比值结构（hit ≈ miss 的 1/4）不变。

## 五、任务 D：文档工具读写 token 治理（2026-09-17）

背景：文档（`document` 工具）的读写没有上下文保护——`get_document` 无条件返回全文，
`get_document_versions` 返回每个历史版本的完整快照；而本地文件侧早就有
「分页 + 20KB 注入预算 + 溢出转 output_path」的双层保护。

### 数据（dev 库 = prod 副本，成本窗口 8-12 ~ 9-17）

调用统计（`messages.tool_calls_json`，实际调用而非 schema 出现次数）：

| 工具 | 调用 |
|------|------|
| `get_document` | 223 |
| `update_document` | 305（含 `old_text` 102） |
| `create_document` | 127 |
| `query_documents` | 149 |
| **`get_document_versions`** | **0** |
| **`rollback_document`** | **0** |

文档规模：106 篇，总 51.6 万字，max 38,313 字，**44 篇（42%）> 3000 字**。

花费构成（5 周，8,306 次调用，¥119.5）：
cache hit 192.9M tok（83.1%，¥7.7）/ cache miss 39.3M tok（¥78.6）/
completion 8.29M tok（¥33.2）。

- 回放倍率实测 ≈ **10×**（语料单份 24.46M 字 × 0.69 tok/字 = 17.1M tok，
  命中 token 扣固定开销 ~25M 后除以 17.1M）。
- 文档工具自身 ≈ **¥2.76 / 5 周 = 总花费 2.3%**（读 miss ¥0.80 + 读回放 ¥0.16
  + 写输出 ¥1.63，其中全量重写 ¥0.78 + 写参数回放 ¥0.17）。
- 结论：**金额收益约 2%**，真正的动机是上下文占用与防增长——50 万字上限下单次整读
  ≈ 35 万 tokens，单请求即可吞掉 30% 上下文。

### 落地改动

1. `get_document` 窗口读：`char_offset`/`char_limit`（默认 3000，上限 20000），
   ≤窗口时行为与全文读完全一致（零回归）；超窗返回窗口 + `total_chars/total_lines/
   next_char_offset/truncated` + `outline`（标题+偏移，最多 12 条）+ `hint`。
   小文档一次读完，大文档先看目录再按 `char_offset` 取片段。
2. 删除 `get_document_versions`、`rollback_document`（实测 0 调用）与文档版本快照写入
   （`VersionRepo` 保留给 note/agent）。
3. 文档标题上限 50 字（原先只有 DB `size:500`，无应用层校验），存量 106 篇中仅 1 篇超限。
4. `query_documents` 暴露 `limit`/`offset`（原硬编码 20/0）。
5. `llm.max_tool_result_chars`：`-1` → `20000`（历史回放安全网）。

### 阈值决策：为什么是 20000 而不是 5000

`max_tool_result_chars` 只作用于**历史回放**（`BuildMessagesForFlash`，当前轮不截）。
>5000 字的结果里，量最大的是 `fs__read`（554 条，5.19M 字，avg 9,376）与
`fs__grep`（296 条，2.67M 字，avg 9,021）——客户端文件工具**故意按 20KB 预算**
（`clientToolInjectBudgetBytes`）设计。截到 5000 会把文件读取的有效内容砍到 1/4，
伤多轮文件/编码跟进；实测削减量 5000→8.79M 字、10000→3.08M 字、**20000→0.49M 字
（仅 63 条病态结果，fs 系仅 1 条超 2 万）**。5000 与 20000 的钱差 ≈ ¥0.5/月，
不值得换来质量风险。

### 验收

`server/internal/tool/document_test.go`：窗口边界（恰好/超一）、`char_offset` 续读
拼接还原全文（含 emoji 多字节不劈字）、`char_limit` 收敛与非法值、offset 越界、
outline 偏移/条数/标题截断、标题 50 字边界与 update 不传 title 放行、
已删函数返回 unknown function、`query_documents` 分页。

### 语义约定与复测修正（2026-09-17）

复测确认的**有意行为**（不要当 bug 修）：

- `char_offset` 负数静默归零 —— 返回体里 `char_offset` 回显生效值，调用方对比即可发现。
- 空正文被拒（`title and content required`）：空文档无意义；要「先建空壳后追加」再放开。
- `outline` 仅在 `truncated=true` 时返回：小文档已返回全文，outline 是纯冗余。
- `limit`/`offset` 不传时用默认值（20 / 0）。

复测后修正的四个问题：

1. **删除不存在的 id 报成功** → `DocumentRepo.Delete` 改为返回 `RowsAffected`（对齐
   `bill_repo.go` / `image_asset_repo.go` 做法），工具返回 `document not found`，
   HTTP 侧 404（原先错误分支还没有日志）。
2. **排序不稳定** → `List` 的 `ORDER BY updated_at DESC` 补唯一 tie-break
   `id DESC`。缺它时同 `updated_at` 的文档顺序由执行计划决定，不同 `limit` 会给出
   不同顺序，翻页重复/漏项（正是 `list_documents` 与 `query_documents` 顺序不一致的根因）。
3. **tags 无法归一化/清空** → 新增 `NormalizeTags`（按半角+全角逗号切分、去空格、
   保序去重、半角 join），写入路径统一走它；`update_document` 的 `tags` 改指针语义，
   传 `""` 表示清空、不传表示不动。读取侧 `ListTagCounts` 也按双分隔符切分，兼容存量。
   存量 dev 库实测 0 行全角（102 行半角），故 `tagMatchClause` 的 SQL 不动。
4. **`limit=0` 与不传无法区分** → `limit` 改指针语义：不传 = 默认 20，`0` = 只取总数
   返回空列表（GORM `Limit(0)` 等于不加 LIMIT，repo 里需显式早返回），负数 = 明确报错。
