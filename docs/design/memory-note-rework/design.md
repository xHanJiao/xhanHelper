# 记忆 / 笔记改造 — 设计文档

> 版本：v1｜日期：2026-09-22｜状态：设计定稿，待开发
>
> 参照：《事实系统（Facts）· 设计文档》（`docs/design/facts-system/design.md`）、《新旧系统产出对比》（同目录 `old-vs-new-output-comparison.md`）、《记忆方案调研》（同目录 `memory-research.md`）
> 估算（开工）：10 面 / 4 缝（1 需探针）/ 2 次真机往返 / 5 个待拍决定 · ~7–12h
> 估算（定稿）：10 面 / 4 缝（1 需探针）/ 2 次真机往返 / 0 个待拍决定 · ~6–10h
>
> 备注：开工粗估那行是回填的——本需求的前半程（调研 + 定方向）没有单列粗估，范围与定稿时一致，故按同一批面/缝回填，仅把当时未拍的决定数记为 5。
> **2026-09-23 方案调整（一）**：后续主体的形态由「决策闸门」（一次 LLM 调用吐 `{op:...}`）改为**「记录循环」（ReAct，复用 `MiniReAct`）**——决策走模型调工具，topic 复用留在写入工具内部。总流程图见同目录 `flow.md`，prompt 与工具契约见 `record-loop-prompt.md`。（其中「提取也并入循环」这一半已被调整（三）作废。）
> **2026-09-23 方案调整（二）**：收敛到**单一实体**——笔记与记忆记的是同一类东西，只有写入时机不同，故 `notes` **停写**、新写入一律落 `user_memories`（靠 `source` 区分 `inline` / `post_chat`），旧笔记留库、逐步清理。方案见 `note-retirement.md`；「记忆让位笔记」这条跨层规则随之消失。
> **2026-09-23 方案调整（三）**：写入改回**两段式**——**抽取**（单次调用出 N 条候选）+ **逐候选决策循环**（ReAct）。原因是调整（一）把提取塞进循环后，`post_chat` 的预检索失去了 query（输入是整段原文，没有可嵌入的「一个点」）。`inline` 天然有候选，直接进决策段。详见 §4.1。
> 排期：分首批 / 后续两段（见 §7）。首批另有两篇：`topic-merge.md`、`ledger-migration.md`；删除清理修复记在 `docs/bugs/017-vector-cleanup-on-delete.md`。
> 另起一票：`tags-retirement.md`（退役 notes/memories 的 tags）、`note-retirement.md` 的阶段 2/3（存量消化与读侧退役），均不并入首批。
> 首批估算：~8 面 / 3 缝（1 需探针）/ 2 次真机往返 / 0 待拍决定 · ~6–10h
> 估算（定稿·2026-09-23 单实体两段式形态，**仅后续主体**）：13 面 / 4 缝（1 需先建探针）/ 2 次真机往返 / 0 个待拍决定 · ~8–13h
> 　（首批已完成，其 8 面 / ~6–10h 另计；上面那行「估算（定稿）」是闸门形态的旧数，留档对比）

---

## 1. 为什么要做这件事

前一阵子做了一套「事实系统」（facts）：把对话里的事实抽成原子记录，支持 ADD / UPDATE / DELETE。系统建起来了，跑了一段时间，**结论是它失败了**——`facts` 表 14 条记录里 12 条是噪声，价值远低于成本。

复盘后我们的判断是：

> **facts 的失败不是「原子化」错了，是「原子化 + 没有决策闸门」错了。**

原子记录本身是对的——一条一件事，将来才好改、好删、好检索。问题在于它只管往里写：没有一道闸门判断「这条到底该不该写、是不是已经有了、该覆盖旧的还是新增」。少了闸门，LLM 每次会话的絮语都被当成事实存下来，14 条里 12 条噪声就是这么来的。

所以我们不去修 facts 本身，而是**回到真正日常在用的记忆（`user_memories`）和笔记（`notes`）上，把它们缺的东西补上**——补的那件东西，正是从 facts 里抽出来的那道决策闸门。facts 本身的退役先搁置（见 §8），它现在还在写，我们不打断它。

## 2. 现状：三个真实的问题

这三个问题不是推测，都是翻代码翻出来的。

### 2.1 记忆一次会话只出一条，而且只嵌入一句话

`MemoryExtractor.ExtractFromMessages` 的返回值是**单个** `*model.UserMemory`（`agent/extractor.go:114`）。提取提示词里写着「每次最多提取 3 条最重要的」（`extractor.go:47`），但输出结构只有一个 `Summary` 字符串加一个 `Facts []string`——所谓「3 条」实际退化成「一句话 + 一串支持细节」，**永远只落一行**。

更糟的是：这条记忆做向量嵌入时，嵌的是 `memory_value`，也就是那一句 summary（`extractor.go:219`）。`facts` 里的细节从头到尾没有被嵌入，检索永远够不着。于是一个几十轮的会话，可被检索到的面只有一句话。

这让「记忆」这个名字名不副实——对话里明明讨论了很多，最后只剩一句摘要能被找回来。

### 2.2 笔记没有更新与去重，全靠 LLM 自觉

笔记有 `update_note` 工具、有 `ContentVersion` 版本快照、有软删（status=2），看起来齐全。但 `record_note` 在代码层就是一个**纯插入**（`tool/note.go:295`），没有任何去重或冲突检测。会不会重复记、该不该改成旧的那条，**完全依赖主 LLM 自己读 system prompt 里的准则 4**（`promptbuilder/static.go:14`）来判断。

结果是同一件事经常记成好几条措辞不同的笔记，谁也没去合并。记忆那边更彻底——`UserMemoryRepo` 连一个改内容的 `Update` 方法都没有，只有 append，加上一个 0.85 的向量去重和一个 `content_hash` 精确去重。

### 2.3 topic 泛滥，同一个事有好几个 topic

笔记的 topic 由主 LLM 自由填，工具描述里只举了几个例子（`note.go:115`「房地产/睡眠/人物关系/健康/...」）；记忆的 topic 由提取 LLM 自由填，提示词只要求「一个关键主题词、禁斜杠逗号」（`extractor.go:53`）。**两边都没有任何相似度约束**，于是「睡眠」「睡眠管理」「睡眠质量」会同时存在，各挂各的记录，聚合不起来。

这个问题的反面价值在 §4.4 说——topic 一旦收敛，「一个 topic 下挂几十条记录」会成为常态，而**这对将来做 theme 反而是好事**。

## 3. 目标与非目标

**要做**：

- 把写入**分成两段**：**抽取**出候选（`post_chat` 单次调用，`inline` 由主 LLM 直接给），再逐条过**决策循环**（ReAct，复用 `MiniReAct`）——循环里给模型「检索 + 写入」两类工具，模型自主决定这条候选是增是改是删。
- **收敛到单一实体**：`notes` 停写，新写入一律落 `user_memories`（两个入口靠 `source` 区分 `inline` / `post_chat`）；记忆从「一行/会话」变成「多条/会话」（抽取段出多条候选），每条独立嵌入 summary，上限 5 条。
- 主循环的写入工具从 `record_note` 换成 `record_memory`（走同一个循环，**主 LLM 不参与判断**）。
- topic 复用：确定性逻辑（向量相似度）留在 `save_memory` 工具内部，≥ 0.80 就用最相似的已有 topic。
- 修正 prompt 路由：逐日读数（睡眠、体重等流水）该进文档，不进记忆。

**不做**（这次明确不碰）：

- **迁移旧笔记**——1010 条存量留在 `notes` 表，照旧可读、可检索注入；清理只靠「闸门顺带」+「存量梳理」票，**不设时间硬删**（会误杀长期结论）。见 `note-retirement.md`。
- **退役 facts**——不停写、不删表。`user_memories.facts` 字段**保留**（落库但不嵌入），它是将来做 ADD/UPDATE/DELETE 判断的重要依据。
- **tags**——本批不动；退役方案见 §4.5 与 `tags-retirement.md`（**另起一票**）。
- **存量数据梳理**——旧笔记的批量消化 + 记忆的「从后往前」梳理，另起一票，不着急。
- **theme**——继续挂起（当前 4 个 active 主题里 2 个 `state_text` 还是空的），topic 收敛后自然会好做。
- **版本历史**——记忆不新增版本能力。笔记现有的 `ContentVersion` 原样保留，随 `note-retirement.md` 阶段 3 一起退役。
- **笔记的读侧与展示**——`note_source` 检索注入、`NotesView.vue`、Swift 笔记视图、`/api/notes` 全部保留到数据清空（`note-retirement.md` 阶段 3）。

## 4. 设计

### 4.1 总览

一句话：**写入分两段——「抽取」出候选（单次调用），再逐条过「决策循环」（ReAct，复用 `MiniReAct`）；循环里给模型「检索 + 写入」两类工具，模型自主决定这条候选是增是改是删。** topic 复用是确定性逻辑，留在写入工具内部。

只有**一个实体**（`user_memories`）、**一个循环**、**两个入口**：

```
入口 inline   : 主 LLM 调 record_memory(topic, summary, facts) → 1 条候选 ┐
入口 post_chat: 对话原文 → MemoryExtractor（单次调用，N 条候选 ≤ 5）      ┤
                                                                       ↓
                                          【每条候选依次处理】
                                   预检索（embed 候选 summary → memory + note 两域，top_k，≥ 0.5）
                                                                       ↓
                                   决策循环（MiniReAct，candidate + 近邻 作初始上下文）
                                     ├ 工具: search_memory_* / search_note_*（只读）
                                     └ 工具: save_memory / update_memory / delete_memory
                                             + delete_note（过渡期，清旧笔记）
                                             （save_memory 内部做 topic 复用）
                                                                       ↓
                                   落库 + 同步向量（候选 N 的写入对 N+1 检索可见）
```

**为什么先抽取、再决策**：决策循环的第一步是「拿候选去检索近邻」，而 `post_chat` 的输入是整段对话原文——原文没有可嵌入的「一个点」，直接拿它 embed 去检索是噪声。所以 `post_chat` 必须先有一步抽取，把原文收成 N 条候选，预检索才有 query。`inline` 天然有候选（主 LLM 给的），直接进决策段。**这也是「预检索」这件事能成立的前提。**

**为什么决策段还要 ReAct**：预检索只给语义近邻，而语义搜索的盲区是**字面量**（人名、专有名词、日期、金额）。循环让模型能追加 `search_memory_keyword` 把这类近邻捞回来。多数候选一轮就决策完（预检索已给足上下文），循环只在需要时多跑。

**「ADD / UPDATE / DELETE / NONE」不再是让模型吐一个 op 字段，而是模型选哪个写入工具**（一个都没调 = NONE）。

**两个入口下发同一套工具**——只有一个实体，没有「只能写哪一侧」的区分；`entry` 只决定输入形态与落库的 `source`。

> 原设计里那条「**记忆让位笔记**」的跨层互斥规则**已随单实体化消失**：笔记与记忆记的是同一类东西，只是写入时机不同（详见 `note-retirement.md`）。同一张表下，`inline` 刚写的记忆，`post_chat` 入口检索时自然看得到 → 判 `update` / NONE 即可。这是本次简化最大的一处。

### 4.2 两段式：抽取 → 逐候选决策循环

前身是 facts 的两段式（`FactWorker`：`fact_worker.go:92` 编排 → `:123` 逐候选 → `:170` 预检索 → `:483` 单发决策）——把最后的**单发决策换成 `MiniReAct` 循环**，其余骨架照搬。新建 `agent/record_loop*.go`；`MiniReAct` 本体**零改动**（`ChatSync` + 原生 function calling，工具结果以 `role=tool` 回传，`maxIterations` 上限 + 强制收敛轮）。

**入口两条，循环一个：**

| 入口 | 触发 | 输入 | 抽取段 | `source` 落库 |
|---|---|---|---|---|
| `inline` | 对话进行中，主 LLM 调 `record_memory` | 一条候选 | 主 LLM 直接给（`record_memory` 的参数） | `inline` |
| `post_chat` | 会话结束，后台 `PostChatWorker` | session 窗口（原始消息） | `MemoryExtractor`：单次 `ChatSync`，N 条候选（≤ 5） | `post_chat` |

**抽取段（仅 `post_chat`）**：复用现有 `MemoryExtractor`，输出结构从单条 `extractResult` 改成 `{memories: [...]}`（照 `fact_extractor.go:34` 的 `factExtractResult{Facts []factCandidate}`），上限 5。

**决策段（两个入口共用）**：对每条候选依次——

1. **预检索**（确定性代码，非 LLM）：embed 候选 summary → memory + note 两域取近邻（≥ `memory_neighbor_threshold` = 0.5，top_k），格式化成 `<neighbors>`。
2. **决策循环**：候选 + 近邻作 `<candidate>` / `<neighbors>`，经 `MiniReActConfig.InitialMessages`（`mini_react.go:82`）注入，模型在循环里选工具。这样省掉模型的第一轮搜索；模型若觉得不够，仍可调检索工具追加。
3. **落库 + 同步向量**：立即写入，保证候选 N 的写入对候选 N+1 的检索可见（照 `fact_worker.go:24` 的注释——批内互不可见会让两条相似候选都判 ADD）。

**工具集：**

| 类 | 工具 | 说明 |
|---|---|---|
| 检索（记忆） | `search_memory_semantic` / `search_memory_keyword` | 只读，按需追加检索 |
| 检索（旧笔记，过渡期） | `search_note_semantic` / `search_note_keyword` | 只读，用来找旧笔记 |
| 写入（记忆） | `save_memory` / `update_memory` / `delete_memory` | 落 `user_memories` |
| 清旧笔记（过渡期） | `delete_note` | 落 `notes`（软删），**只删不写** |

**入口 × 工具矩阵：两个入口完全相同。** 只有一个实体，不存在「记忆入口不该写笔记」这类区分——`notes` 侧的 `delete_note` 是过渡期的清理工具，两侧都需要。

> prompt 全文、8 个函数的 schema、`target_id` 命名空间（裸 UUID + 类型匹配）、输入块格式见 `record-loop-prompt.md`。

**语义：**

- **「提取多条」= 抽取段出多条候选**（`post_chat`），或主 LLM 多次调 `record_memory`（`inline`）。每条候选各自过一遍决策循环，各自落一行，**各自嵌入 summary**（不再只有一句话能被检索到）。
- **「决策」= 模型选哪个写入工具**：`save_memory` = ADD、`update_memory`（带 `target_id`）= UPDATE、`delete_memory` = DELETE；**一个写入工具都没调 = NONE**。
- **走原生 function calling**：不是让模型吐 JSON 再解析，而是给一组工具定义、读 `msg.ToolCalls`（照 `FactDecider`，`fact_worker.go:483`——注释明写「始终走原生 function calling：`use_native_fc` 只管 orchestrator 自己的工具列表」）。`target_id` 必须落在近邻集合内（`idSet` 校验，`:484`，防幻觉 id）**且类型匹配**（`idType`，否则 `delete_note` 可能被喂一个 memory 的 id）；参数非法丢弃并记日志，**不拿零值继续写库**。
- **NONE 的确认计数**：模型对这条候选一个写入工具都没调时，取**这条候选**的预检索近邻里**相似度最高的记忆**，**且该相似度 ≥ `memory_confirm_threshold` = 0.80**（近邻已按类型可辨；不达阈值或无记忆近邻则无操作），才调 `UpdateConfirm`（**复用已有的 `confirm_count` / `last_confirmed_at`**，`user_memory_repo.go:134`）。
  - 语义：旧记忆被**再次确认**（这轮又谈到它、内容没变）→ 计数 +1。这正好复用那两个早就有、却一直没人读的字段。
  - 为什么要卡 0.80：NONE 只说「没有新东西要写」，不代表「最高分近邻就是被重申的那条」。相似度不够就说明它只是碰巧排在最近，不是同一条记忆——不卡阈值会把 confirm 记到无关记忆头上。
  - 边界：不额外加 `confirm_memory` 工具——NONE 已经表达了「没有新东西要写」，再开一个函数只为表达 confirm 不值当。
  - 注意：`confirm_count` / `last_confirmed_at` 目前**只写不读**（`retrieval/memory_source.go:28` 也会在检索命中时批量 +1），没有任何排序/评分消费它。所以这个动作是**为将来攒信号**，不影响现有行为。**不新增任何数据库列。**
- **上限 5 条**：`memory_max_per_session` = 5。`post_chat` 落在**抽取段**（prompt 明示 + 解析后截断到 5）；`inline` 由主 LLM 自行克制（每次调用天然 1 条），不设跨调用硬计数。

**topic 复用留在 `save_memory` 内部**（见 §4.4）：模型给的是 topic 字符串，工具内部做确定性处理——**不交给 LLM 判**，向量相似度计算不该让 LLM 复现。

**判定交给模型，确定性逻辑只管两件事**：**topic 复用**（§4.4，向量计算）与 **`target_id` 的 id/类型校验**（`record-loop-prompt.md` §5）。不在 `save_memory` 里做阈值硬拦截——会与模型的语义判断打架，且「语义重复」与「向量相似」不是一回事（0.5 太松、0.8 太严）。漏判由 §9 兜底。

**实现：复用 `MiniReAct`，本体零改动。**

难点在工具：`MiniReAct` 要求 `ToolRegistry` 非 nil（`mini_react.go:111` panic），工具执行统一走 `toolRegistry.ExecuteByName`（`:359`）。而决策循环的 8 个函数（3 个 `ToolDefinition`）是**闭包**（持有 userID、repo、计数器、近邻 `idSet`），不是注册表里的全局工具。

做法：**为每次循环临时建一个 `tool.NewRegistry()`**，用薄适配器 `recordTool`（实现 `tool.Tool` 的五个方法，闭包挂在 `Execute` 上）把函数注册进去。`Register` 只是 map 插入（`registry.go:130`），`Validate*` 那套校验是启动时单独调用的，所以临时注册表不会触发 panic。`ec.Mode` 被 `MiniReAct` 硬编码成 `ConvModeWork`（`:358`），plan 拦截不会误伤。

四个配置点：

| 配置 | 取值 | 原因 |
|---|---|---|
| `Tools` | 三个 `ToolDefinition`（`record-loop-prompt.md` §3.1）：`record_search`（4 个检索函数）+ `memory`（3 个写记忆）+ `legacy_note`（`delete_note`） | **两个入口下发同一套**，不按入口裁剪 |
| `FinalRoundPrompt` | **自定义**：「已达最大步数，不要再调用工具，也不要输出说明文字」 | 默认那条是「直接输出最终结果」（`genericFinalRoundPrompt`），决策循环没有「最终结果」要给谁看。仅轮次耗尽才触发（罕见），代价是白跑一次调用 |
| `OnEvent` | `nil`（静默） | 不往 SSE 推 `mini_react:*`；结果经 `record_memory` 返回值透传 |
| `MsgSink` | `nil`（纯内存） | 循环消息不落库；需要观测时读 `ToolCallLog` |

出口：读 `MiniReActResult.ToolCallLog`（`mini_react.go:363`）拿到「执行了哪些写入、各自结果如何」——`inline` 入口据此拼 `{action, id, summary}` 返回给主 LLM，`post_chat` 入口据此打日志与统计。

轮数上限单独给一个配置 `record_loop_max_iterations`（默认 6，见 §6）——不沿用 `MiniReAct` 的默认 10，后台每候选一次 × 10 轮调用偏多；一轮可发多个 tool call，6 轮足够单个候选「检索 1~2 轮 + 决策」。

### 4.3 `record_memory` 接入主循环（inline 入口，同步跑）

文件：新增 `record_memory` 工具（`tool/` 下，与 `note` 工具并列），由主 LLM 在对话中调用。

- 调用时，工具把这条候选（topic + summary + facts）交给**同一个决策循环**（`entry=inline`）——**主 LLM 完全不参与判断**。主 LLM 只负责表达「我要记这条」。
- **全放**：允许自动 add / update / delete（你确认过，风险可接受）。
- 代价是必须说清楚：工具 description 要写明「可能与已有记忆重复，会自动新增/更新/删除」；返回值带上 `{action, id, summary}`，让主 LLM 知道刚才到底发生了什么。
- **同步跑**（Flash + 近邻不多，估计 2~4 秒）。真机测下来如果慢，再落到 `DelayedTask`（新增 `TaskType = memory_decide`，`model/delayed_task.go` 的 `TaskType` 字段和 CronRunner 分发都现成），异步时工具立即返回「已记录（整理中）」。
- **与 `note` 工具的关系**：`record_note` 退役（`note-retirement.md` 阶段 0），`record_memory` 是它在主循环里的接替者。`note` 工具只保留读 + 删。

`post_chat` 入口由 `PostChatWorker` 编排（改造现在的 `MemoryWorker`，`agent/memory_worker.go`）：先跑 `MemoryExtractor`（`agent/extractor.go`，改造为输出 N 条候选），再逐候选进决策循环。无工具返回值需求。

### 4.4 topic 复用（仅 ADD）

**好消息：这套机制的零件已经全在了，只是没人用它来收敛 topic。**

- topic 早就被嵌入了：`ConceptEmbedService.EnsureTopicEmbedded` 会把 topic 写进 `concept_embeddings` 表并算好向量（`agent/concept_embedder.go:31`）。只是这些 topic 向量没进内存向量库，需要时得从 DB 读。
- 相似度比较和聚类也现成：`util.ClusterVectors` + `util.CosineSimilarity`（`internal/util/cluster.go`），`get_topic_stats.go:52-102` 已经在用「加载 topic 向量 → 聚类」这条路了。
- 阈值也现成：`cluster_threshold: 0.70`。

做法：新增一个 `FindSimilarTopics(userID, topic, threshold)`，**候选集只取该用户「在用 topic」**（notes ∪ memories 的 distinct topic，复用 `topicmerge/gcTx` 的引用集合逻辑：`DistinctTopicsWithCount` + `DistinctTopics`），加载这些 topic 的向量、算余弦。**只在 `save_memory` 时跑**（`update_memory` / `delete_memory` / `delete_note` / NONE 不碰 topic），且**不拒绝**：≥ 阈值就用最相似的已有 topic，否则用新的。没有「报错打回重选」这条路径。

> 候选集仍是**两域**（notes ∪ memories）：旧笔记还在库里，它们用过的 topic 依然是有效的复用目标；等 `notes` 清空（`note-retirement.md` 阶段 3）后自然收敛成单域。

> 为什么不用概念表全量：`concept_embeddings` 里会有「引用清零但没人回收」的孤儿行（删除入口按 bug017 的边界不清概念行），其中不少是历史遗留的复合垃圾名。若拿全量概念参与比较，新 topic 可能被复用到这种已死的词上。限定「在用 topic」即天然排除孤儿；孤儿本身无害（不进检索、不进 topic-merge 报告），隔几周手动跑一次 `make topic-merge-apply`（末尾带 GC，幂等）即可。

阈值**单级**：`memory_topic_reuse_threshold` = 0.80（与存量收敛 `topic-merge` 的 0.80 一致）。原先设想的「建议线 0.5 / 拒绝线 0.70」双阈值已废弃——在无交互的常见路径上，建议线没有意义，拒绝后又要求重选也属多余。

外加命名硬校验：禁止斜杠、逗号、多个主题，topic ≤ 5 字。

topic 收敛之后，「一个 topic 下挂几十条记录」会成为常态——这正是我们想要的，它让 theme 以后变成一件简单事（按 topic 聚合即可）。

### 4.5 tags：退役（另起一票）

确认过，tags 现在**基本没用**：tag 概念已是空实现（`concept_embedder.go:35-38`），笔记/记忆上的 `tags` 字段只被 `get_topic_stats.splitTags` 读一次（`get_topic_stats.go:161,184`）+ 关键词检索顺带 LIKE，检索链路（`retrieval/*_source.go`）完全不碰它。

topic 收敛后 topic 天然对应 theme，这唯一的弱信号也不再需要 → **退役**：停写、停读（含关键词 LIKE）、埋掉 tag 概念残骸，但**保留列与历史展示**。

详见 `tags-retirement.md`。**本票另起，不并入首批。**

### 4.6 路由修正

- **逐日读数**（睡眠、体重、饮酒、运动这类流水）→ 进 `document`，不进记忆。原因是它们单条没价值、攒起来才有意义，正确的介质是文档表格。这是**改路由**，不是丢弃。
- **工作研讨**：一律记 `memory`（对话中当场写 / 会话结束后提炼）——笔记退役后不再有「当场记 note、事后补 memory」的分工（见 `note-retirement.md`）。

## 5. 数据与兼容

- **不加数据库列。** 记忆侧新增一个 `Update` 方法（改内容 + 重嵌入），复用一个没用过的 `UpdateConfirm`；其余全用现有字段。
- **`notes` 停写、不迁移、不删表。** 1010 条存量留在库里，照旧可读、可检索注入；清理只靠闸门顺带 + 存量梳理票，**不设时间硬删**。详见 `note-retirement.md`。
- **`user_memories.source` 取值扩展**：`conversation` 拆成 `inline` / `post_chat`；历史行的 `conversation` 保持不动，查询侧容忍三种值。
- **不删表、不删列。** `user_memories.facts` 保留（落库但不嵌入），facts 表继续写。
- **不开版本历史。** 记忆不加；笔记现有的 `ContentVersion` 原样保留到 `note-retirement.md` 阶段 3。

## 6. 配置项

都放在 `server:` 段（和现有的 `memory_auto_extract` / `memory_dupe_threshold` 在一起，不新开段）：

| 配置 | 默认 | 说明 |
|---|---|---|
| `record_loop_enabled` | true | 记录循环总开关（抽取 + 决策），便于灰度/回退（关掉则回退到旧的单条提取 + 纯插入） |
| `record_loop_max_iterations` | 6 | 循环轮数上限（含强制收敛轮），见 §4.2 |
| `memory_max_per_session` | 5 | 单次会话最多新增几条记忆：`post_chat` 是**抽取段**输出上限（**prompt 里明示** + 解析后截断到 5），`inline` 每次调用天然 1 条 |
| `memory_neighbor_threshold` | 0.5 | 预检索近邻的相似度下限（低于此不入初始上下文） |
| `memory_topic_reuse_threshold` | 0.80 | topic 复用阈值：≥ 则用最相似的已有 topic（仅 `save_memory`） |
| `memory_confirm_threshold` | 0.80 | NONE 时才算「再次确认」的记忆近邻相似度下限（低于此不动 `confirm_count`），见 §4.2 |

> topic 复用阈值与存量收敛（`topic-merge`）一致取 0.80。**先跑一段时间再看**，不合再调。
> 字段级硬限制（topic ≤ 5 字 / summary ≤ 150 字 / 单条 fact ≤ 100 字、单次传入 facts ≤ 10 条）是**常量**，不进配置——它们是不变量，不是可调阈值。

## 7. 分期与验收

> 2026-09-22 排期调整：先做**首批**（删除清理 bug + topic 收敛 + 台账挪走 + 措辞），把地基清干净；再动**后续**的记录循环。topic 的「写入侧复用」从首批挪到后续。
> 2026-09-23：后续主体改为**单实体两段式**（抽取 + 逐候选决策循环，`notes` 停写）。

**首批**（单独两篇：`topic-merge.md`、`ledger-migration.md`）：

1. **删除清理修复**（bug `017`）：抽共享 `PurgeEntityVectors`，修好笔记/记忆四个删除入口的派生数据残留；`concept_embeddings` 的 topic 行改走孤儿 GC。
2. **topic 存量收敛**：`server/cmd/topic-merge/`，leader 聚类出合并报告 → 人工确认 → 批量改 `notes.topic` / `user_memories.topic` + 概念表去重 + 孤儿 GC。
3. **台账挪走**（agent 执行）：逐日流水从 notes / memories 归入 document、删原记录。
4. **措辞修正**：note / 记忆提取 prompt 明确「台账不记入笔记与记忆，改走 document」。

**后续**（本 `design.md` 的正文设计）：

> **状态（2026-09-23）**：1–5 全部已实现并 `go build ./... && go test ./...` 通过、启动冒烟通过。代码落点：
> `agent/record_loop*.go`（决策循环 + `recordLoopState` 闭包工具 + `reuseTopic`/`distinctTopics`）、
> `agent/extractor.go`（`ExtractCandidates`/`memoryCandidate`/`memoryExtractResult`）、
> `agent/memory_worker.go`（抽取→逐候选编排）、`agent/promptbuilder/memory_definition.go`（定义单一来源）、
> `tool/record_memory.go`（inline 工具 + `RecordMemoryRunner` 窄接口）、`service/user_memory_repo.go`（`Update` + `hashMD5`）、
> `config`（7 个记录循环字段 + 默认值）、`model/user_memory.go`（`MemorySource*` 常量）、
> `agentinit`（tools.go 注册工具 + agents.go 回填 runner）、`tool/seeder.go` + Swift/前端展示映射。
> 尚缺：**离线重放探针**（缝，未建）、真机三测（待跑）。

1. **抽取段**：`MemoryExtractor` 输出结构改 `{memories: [...]}`（照 `fact_extractor.go:34`），上限 `memory_max_per_session` = 5。
2. **决策循环**：新建 `agent/record_loop*.go`（复用 `MiniReAct`），工具集 = 检索（`search_memory_*` / `search_note_*`）+ 写入（`save_memory` / `update_memory` / `delete_memory` / `delete_note`）；候选 + 预检索近邻（≥ 0.5）经 `InitialMessages` 注入；新增 `UserMemoryRepo.Update`；NONE（且最高分记忆近邻 ≥ `memory_confirm_threshold` 0.80）走 `UpdateConfirm`。
3. **两个入口接入**：`inline` = 新增 `record_memory` 工具接进主循环；`post_chat` = `MemoryWorker` 编排「抽取 → 逐候选决策循环」。先同步，慢则加 `memory_decide` 延迟任务。
4. **笔记停写**：`record_note` / `update_note` / `rollback_note` / `get_note_versions` 从 note 工具去掉（`note-retirement.md` 阶段 0）；准则 4 措辞改 `record_memory`；`seeder.go` / Swift / 前端的展示映射同步。
5. **topic 复用**：`FindSimilarTopics` + 接在 `save_memory` 内部（阈值 0.80）+ 命名校验。

**验收**：

- `go build ./... && go test ./...` 全绿，加针对性单测（**抽取段 N 条解析与截断**、循环的工具分发与 `idSet`/`idType` 校验、topic 相似度、source 落库）。
- 真机三测：
  1. 一次多主题会话 → **抽取段出多条候选** → 记忆出 2~5 条，且 topic 复用已有。
  2. 对话中说一件值得记的事 → 落 `user_memories`（`source=inline`），**`notes` 表不新增**。
  3. 用一个和旧 topic 很像的新 topic（≥ 0.80）→ 应自动落到旧 topic 上。

**缝（不能自测、只能真跑的部分）**：**抽取段的质量**（N 条候选是否合理、漏没漏 / 噪不噪）、决策循环里 LLM 的真实行为（会不会乱删、会不会空转）、`inline` 入口同步跑循环的延迟体验、topic 阈值在真实数据上的表现。其中「抽取质量」与「循环行为」打算先建一个探针——拿真实 session 窗口离线重放一遍，把**抽取结果与工具调用序列**都导出来人工看。

**估算明细（后续主体，13 面 / 4 缝 / 2 往返）**：

- **面（13）**：① 抽取段改造（`extractor.go` → `{memories:[...]}` + cap）；② `recordTool` 适配器 + 临时 registry；③ `record_loop*.go` 决策循环组装（含 `InitialMessages` 注入）；④ 预检索与 `idSet`/`idType` 构建；⑤ 3 个 `ToolDefinition` schema（8 函数）；⑥ `save_memory` 落库（topic 复用接入 + `source`）；⑦ **新增** `UserMemoryRepo.Update`；⑧ `delete_memory` / `delete_note`（接 bug017 清理器）；⑨ 4 个检索函数复用 `unified_search`；⑩ `FindSimilarTopics` + 命名校验；⑪ NONE → `UpdateConfirm`（含 0.80 阈值）；⑫ 两个入口接入（`record_memory` 工具 / `MemoryWorker` 编排）；⑬ 笔记停写 + 措辞 + 展示映射（单测随各面）。
- **缝（4）**：抽取段的质量（**需先建探针**）；决策循环里 LLM 的真实行为；`inline` 入口同步跑的延迟体感；topic 阈值 0.80 在真实数据上的表现。后三条可在探针报告里一并看。
- **往返（2）**：探针报告人工审阅 1 次；真机三测 1 次。
- **决定（0）**：形态、字段、阈值、入口命名均已定稿（`record-loop-prompt.md`、`note-retirement.md`、本文 §4.2/§4.4）。

## 8. 暂缓（记着，不着急）

- **退役 facts**：不停写、不删表和 Changelog。
- **存量梳理**：逐日流水那部分已在首批（`ledger-migration.md`）处理；其余存量仍待「从后往前」捋一遍，另起一票。**本票补一条范围：旧笔记的批量消化**（判断「已被覆盖 / 已过时 → 删；仍有价值 → 提炼进记忆后删」），见 `note-retirement.md` 阶段 2。
- **笔记读侧与展示退役**：数据清空后再做（`note-retirement.md` 阶段 3）。
- **theme**：继续挂起。topic 收敛之后再做会简单很多。
- **tags**：另起一票退役，方案见 `tags-retirement.md`。

## 9. 风险与待观察

1. **LLM 误删**：全放（含 delete）意味着主 LLM 可能不知道循环删了什么。靠 `record_memory` 的 description + 返回值缓解，观察期重点看。旧笔记的删除是**软删**（`notes.status=2`），行可恢复，但派生数据会被清（`note-retirement.md` 风险 2）。
2. **`inline` 入口同步跑循环的延迟**：每次记记忆多一个 2~4 秒的 LLM 循环。若超过体感阈值就转 `memory_decide` 异步。
3. **记忆多条提取的噪声**：原子化 + 循环决策能否真的压住噪声，要用真实数据看；上限 5（**抽取段** prompt 明示 + 截断）是保险丝。
4. **循环自身漏判**：跨域召回靠向量，召回不到就判不出重复。近邻取 top_k + 0.5 阈值是当前折中，观察期看漏判率。
5. **循环不收敛 / 空转**：模型可能反复检索不写入。靠 `maxIterations` 上限兜底；记日志观察实际轮数分布。
6. **过渡期两套并存**：旧笔记与新记忆可能同时被注入，出现重复信息。这是**有意的过渡状态**，不是 bug；随 `note-retirement.md` 阶段 2/3 收敛。
7. **注入预算失衡**：笔记 3000 / 记忆 600，笔记清空后记忆承载全部，600 会不够——阶段 3 必须一起调。

---

## 后续变更

- **2026-10-13**：`topic ≤ 20 字` 的取值早已被 `topic-5char.md` 收紧到 **5 字**（实现见 `memoryTopicMaxLen = 5`、
  `topic_namer.go`），本文 §4.4/§6 的 20 字是残留，已就地改齐。
- **2026-10-13**：上线一个月后的数据回收发现三类噪声（记账流水 / 本轮任务过程 / 同主题碎片化），
  以及一个实现缺口——**§4.2 的决策循环指令从未进代码**。修复方案见 `docs/design/memory-quality/design.md`
  （补齐指令 + 收窄门槛 + `<topic_records>` 视野），本票范围之外的循环改动（拒绝出口、幻觉核对）在该文档 §6 挂账。
- **2026-10-31**：`memory_max_per_session` 由 **5 改为 2**（本文 §4.2/§6/§9 等处写的「上限 5」是当时的取值）。
  动机：一次会话炸出 4~5 条碎片（10-24 10:44 五条、10-25 13:26 四条）。同时 `post_chat` 抽取段改为
  **只喂用户消息**（assistant 的结论是幻觉主源）。详见 `docs/design/memory-quality/design.md` 第四轮。
  影响文件：`agent/memory_worker.go`、`agent/extractor.go`、`config/config.go`、`conf/config.yaml`。
