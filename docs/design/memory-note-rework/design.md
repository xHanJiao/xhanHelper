# 记忆 / 笔记改造 — 设计文档

> 版本：v1｜日期：2026-09-22｜状态：设计定稿，待开发
>
> 参照：《事实系统（Facts）· 设计文档》（`docs/design/facts-system/design.md`）、《新旧系统产出对比》（同目录 `old-vs-new-output-comparison.md`）、《记忆方案调研》（同目录 `memory-research.md`）
> 估算（开工）：10 面 / 4 缝（1 需探针）/ 2 次真机往返 / 5 个待拍决定 · ~7–12h
> 估算（定稿）：10 面 / 4 缝（1 需探针）/ 2 次真机往返 / 0 个待拍决定 · ~6–10h
>
> 备注：开工粗估那行是回填的——本需求的前半程（调研 + 定方向）没有单列粗估，范围与定稿时一致，故按同一批面/缝回填，仅把当时未拍的决定数记为 5。
> **2026-09-23 方案调整**：后续主体的形态由「决策闸门」改为**统一的「记录循环」（ReAct，复用 `MiniReAct`）**——提取与决策都变成模型调工具，topic 复用留在写入工具内部。总流程图见同目录 `flow.md`。**估算待重细化**（上面的定稿行是闸门形态的数）。
> 排期：分首批 / 后续两段（见 §7）。首批另有两篇：`topic-merge.md`、`ledger-migration.md`；删除清理修复记在 `docs/bugs/017-vector-cleanup-on-delete.md`。
> 另起一票：`tags-retirement.md`（退役 notes/memories 的 tags），不并入首批。
> 首批估算：~8 面 / 3 缝（1 需探针）/ 2 次真机往返 / 0 待拍决定 · ~6–10h

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

- 把记忆与笔记的写入**统一成一个「记录循环」（ReAct，复用 `MiniReAct`）**：循环里给模型「检索 + 写入」两类工具，模型自主决定记什么、是增是改是删。
- 记忆从「一行/会话」变成「多条/会话」（循环内多次调 `save_memory`），每条独立嵌入，上限 5 条（**prompt 里明示**）。
- 笔记的 `record_note` 内部跑**同一个**循环，**主 LLM 不参与判断**。
- topic 复用：确定性逻辑（向量相似度）留在 `save_*` 工具内部，≥ 0.80 就用最相似的已有 topic。
- 修正 prompt 路由：逐日读数（睡眠、体重等流水）该进文档，不进记忆/笔记。

**不做**（这次明确不碰）：

- **退役 facts**——不停写、不删表。`user_memories.facts` 字段**保留**，它是将来做 ADD/UPDATE/DELETE 判断的重要依据。
- **tags**——本批不动；退役方案见 §4.5 与 `tags-retirement.md`（**另起一票**）。
- **存量数据梳理**——775 条记忆 + 990 条笔记的「从后往前」梳理，另起一票，不着急。
- **theme**——继续挂起（当前 4 个 active 主题里 2 个 `state_text` 还是空的），topic 收敛后自然会好做。
- **版本历史**——记忆和笔记都不新增版本能力。笔记现有的 `ContentVersion` 保留不动，但不再往上加东西。

## 4. 设计

### 4.1 总览

一句话：**把记忆与笔记的写入统一成一个「记录循环」（复用 `MiniReAct`）——循环里给模型「检索 + 写入」两类工具，提取与决策都变成模型调工具；topic 复用是确定性逻辑，留在写入工具内部。**

入口两条，循环一个：

```
入口 A（记忆，会话结束/后台）: session 窗口 → ┐
入口 B（笔记，对话中/工具内）: 单条候选     → ┴→ 记录循环（MiniReAct）
                                                    ├ 循环前预检索一次：跨域近邻（≥ 0.5）
                                                    ├ 工具: search_memory / search_note
                                                    └ 工具: save_* / update_* / delete_*
                                                          （save_* 内部做 topic 复用）
```

「提取多条」不再是单独的 LLM 调用与 JSON 结构，而是**模型多次调 `save_memory`**；「ADD/UPDATE/DELETE/NONE」不再是让模型吐一个 op 字段，而是**模型选哪个写入工具**（一个都没调 = NONE）。

设计上有一条重要的原则：**记忆让位笔记**。笔记是「重要、当场记」，记忆是「次要、事后补」。笔记在对话中先写，记忆在会话结束后写，所以后写的记忆一旦发现笔记已经覆盖了同一件事，就应该让位（不写入，或删掉自己那条）。互斥由记忆这一侧的循环负责执行。

### 4.2 记录循环（核心）

新建 `agent/record_react.go`，复用 `agent/mini_react.go` 的 `MiniReAct`：`ChatSync` + 原生 function calling，工具结果以 `role=tool` 回传，`maxIterations` 上限 + 强制收敛轮。前身是 facts 的决策段（`fact_worker.go:483` 的 `FactDecider.Decide`——它本来就是原生 FC，见下）与 `extractor.go` 的提取段。

**入口两条，循环一个：**

| 入口 | 触发 | 输入 |
|---|---|---|
| 记忆 | 会话结束，后台 `PostChatWorker` | session 窗口（原始消息） |
| 笔记 | 对话中，主 LLM 调 `record_note` | 单条候选（工具参数） |

**循环前：预检索一次。** 跨 note + mem 两域取近邻（相似度 ≥ `memory_neighbor_threshold` = 0.5，取 top_k），格式化成 `<neighbors>` 作为初始上下文。这样省掉模型的第一轮搜索；模型若觉得不够，仍可调检索工具追加。

**工具集：**

| 类 | 工具 | 说明 |
|---|---|---|
| 检索 | `search_memory` / `search_note` | 只读，按需追加检索 |
| 写入（记忆） | `save_memory` / `update_memory` / `delete_memory` | 落 `user_memories` |
| 写入（笔记） | `save_note` / `update_note` / `delete_note` | 落 `notes` |

**语义：**

- **「提取多条」= 模型多次调 `save_memory`**。一条 = 一个 topic + 一句 summary + facts 细节，由 `save_memory` 的参数定义；每次调用落一行，**各自嵌入 `memory_value`**（不再只有一句话能被检索到）。
- **「决策」= 模型选哪个写入工具**：`save_*` = ADD、`update_*`（带 `target_id`）= UPDATE、`delete_*` = DELETE；**一个写入工具都没调 = NONE**。
- **走原生 function calling**：不是让模型吐 JSON 再解析，而是给一组工具定义、读 `msg.ToolCalls`（照 `FactDecider`，`fact_worker.go:483`——注释明写「始终走原生 function calling：`use_native_fc` 只管 orchestrator 自己的工具列表」）。`target_id` 必须落在近邻集合内（`idSet` 校验，`:484`，防幻觉 id）；参数非法丢弃并记日志，**不拿零值继续写库**。
- **NONE 的确认计数**：模型没调任何写入工具时，对预检索的 top-1 近邻调 `UpdateConfirm`（**复用已有的 `confirm_count` / `last_confirmed_at`**，`user_memory_repo.go:134`）——命中了旧记录但内容没变，确认计数 +1。笔记侧无操作。**不新增任何数据库列。**
- **上限 5 条**：`memory_max_per_session` = 5，**必须在 prompt 里明示**「本次最多记 5 条」；循环内累计 `save_memory` 达 5 次后工具直接返回错误提示，逼模型收敛。

**topic 复用留在 `save_*` 内部**（见 §4.4）：模型给的是 topic 字符串，工具内部做确定性处理——**不交给 LLM 判**，向量相似度计算不该让 LLM 复现。

互斥的落点：**记忆侧循环**跨查两域，命中笔记即不写入（或删掉自己那条，记忆让位笔记）；**笔记侧循环**主要查笔记域，可选地删掉被顶替的旧记忆。这样无论谁先写，最终一致。

### 4.3 笔记接入（同循环，同步跑）

文件：`tool/note.go` 的 `record_note`。

- 调用时，工具内部跑**同一个**记录循环（入口 B）——**主 LLM 完全不参与判断**。主 LLM 只负责表达「我要记这条」。
- **全放**：允许自动 add / update / delete（你确认过，风险可接受）。
- 代价是必须说清楚：`record_note` 的 description 要写明「可能与已有笔记重复，会自动新增/更新/删除」；返回值带上 `{action, id, note}`，让主 LLM 知道刚才到底发生了什么。
- **同步跑**（Flash + 近邻不多，估计 2~4 秒）。真机测下来如果慢，再落到 `DelayedTask`（新增 `TaskType = note_decide`，`model/delayed_task.go` 的 `TaskType` 字段和 CronRunner 分发都现成），异步时工具立即返回「已记录（整理中）」。

### 4.4 topic 复用（仅 ADD）

**好消息：这套机制的零件已经全在了，只是没人用它来收敛 topic。**

- topic 早就被嵌入了：`ConceptEmbedService.EnsureTopicEmbedded` 会把 topic 写进 `concept_embeddings` 表并算好向量（`agent/concept_embedder.go:31`）。只是这些 topic 向量没进内存向量库，需要时得从 DB 读。
- 相似度比较和聚类也现成：`util.ClusterVectors` + `util.CosineSimilarity`（`internal/util/cluster.go`），`get_topic_stats.go:52-102` 已经在用「加载 topic 向量 → 聚类」这条路了。
- 阈值也现成：`cluster_threshold: 0.70`。

做法：新增一个 `FindSimilarTopics(userID, topic, threshold)`，**候选集只取该用户「在用 topic」**（notes ∪ memories 的 distinct topic，复用 `topicmerge/gcTx` 的引用集合逻辑：`DistinctTopicsWithCount` + `DistinctTopics`），加载这些 topic 的向量、算余弦。**只在 `save_*` 时跑**（`update_*` / `delete_*` / NONE 不碰 topic），且**不拒绝**：≥ 阈值就用最相似的已有 topic，否则用新的。笔记与记忆同一处理，没有「报错打回重选」这条路径。

> 为什么不用概念表全量：`concept_embeddings` 里会有「引用清零但没人回收」的孤儿行（删除入口按 bug017 的边界不清概念行），其中不少是历史遗留的复合垃圾名。若拿全量概念参与比较，新 topic 可能被复用到这种已死的词上。限定「在用 topic」即天然排除孤儿；孤儿本身无害（不进检索、不进 topic-merge 报告），隔几周手动跑一次 `make topic-merge-apply`（末尾带 GC，幂等）即可。

阈值**单级**：`memory_topic_reuse_threshold` = 0.80（与存量收敛 `topic-merge` 的 0.80 一致）。原先设想的「建议线 0.5 / 拒绝线 0.70」双阈值已废弃——在无交互的常见路径上，建议线没有意义，拒绝后又要求重选也属多余。

外加命名硬校验：禁止斜杠、逗号、多个主题，限制长度。

topic 收敛之后，「一个 topic 下挂几十条记录」会成为常态——这正是我们想要的，它让 theme 以后变成一件简单事（按 topic 聚合即可）。

### 4.5 tags：退役（另起一票）

确认过，tags 现在**基本没用**：tag 概念已是空实现（`concept_embedder.go:35-38`），笔记/记忆上的 `tags` 字段只被 `get_topic_stats.splitTags` 读一次（`get_topic_stats.go:161,184`）+ 关键词检索顺带 LIKE，检索链路（`retrieval/*_source.go`）完全不碰它。

topic 收敛后 topic 天然对应 theme，这唯一的弱信号也不再需要 → **退役**：停写、停读（含关键词 LIKE）、埋掉 tag 概念残骸，但**保留列与历史展示**。

详见 `tags-retirement.md`。**本票另起，不并入首批。**

### 4.6 路由修正

- **逐日读数**（睡眠、体重、饮酒、运动这类流水）→ 进 `document`，不进记忆/笔记。原因是它们单条没价值、攒起来才有意义，正确的介质是文档表格。这是**改路由**，不是丢弃。
- **工作研讨**：重要的当场记 `note`，次要的事后补 `memory`。

## 5. 数据与兼容

- **不加数据库列。** 记忆侧新增一个 `Update` 方法（改内容 + 重嵌入），复用一个没用过的 `UpdateConfirm`；其余全用现有字段。
- **不删表、不删列。** `user_memories.facts` 保留，facts 表继续写。
- **不开版本历史。** 记忆和笔记都不加；笔记现有的 `ContentVersion` 原样保留。
- **旧数据不动。** 存量梳理另起一票。

## 6. 配置项

都放在 `server:` 段（和现有的 `memory_auto_extract` / `memory_dupe_threshold` 在一起，不新开段）：

| 配置 | 默认 | 说明 |
|---|---|---|
| `record_loop_enabled` | true | 记录循环总开关，便于灰度/回退（关掉则回退到旧的单条提取 + 纯插入） |
| `memory_max_per_session` | 5 | 单次会话最多记几条记忆（**prompt 里明示** + 工具侧计数封顶） |
| `memory_neighbor_threshold` | 0.5 | 预检索近邻的相似度下限（低于此不入初始上下文） |
| `memory_topic_reuse_threshold` | 0.80 | topic 复用阈值：≥ 则用最相似的已有 topic（仅 `save_*`） |

> 循环的 `maxIterations` 复用 `MiniReAct` 现有配置。topic 复用阈值与存量收敛（`topic-merge`）一致取 0.80。**先跑一段时间再看**，不合再调。

## 7. 分期与验收

> 2026-09-22 排期调整：先做**首批**（删除清理 bug + topic 收敛 + 台账挪走 + 措辞），把地基清干净；再动**后续**的记录循环。topic 的「写入侧复用」从首批挪到后续。

**首批**（单独两篇：`topic-merge.md`、`ledger-migration.md`）：

1. **删除清理修复**（bug `017`）：抽共享 `PurgeEntityVectors`，修好笔记/记忆四个删除入口的派生数据残留；`concept_embeddings` 的 topic 行改走孤儿 GC。
2. **topic 存量收敛**：`server/cmd/topic-merge/`，leader 聚类出合并报告 → 人工确认 → 批量改 `notes.topic` / `user_memories.topic` + 概念表去重 + 孤儿 GC。
3. **台账挪走**（agent 执行）：逐日流水从 notes / memories 归入 document、删原记录。
4. **措辞修正**：note / 记忆提取 prompt 明确「台账不记入笔记与记忆，改走 document」。

**后续**（本 `design.md` 的正文设计）：

1. **记录循环**：新建 `agent/record_react.go`（复用 `MiniReAct`），工具集 = 检索（`search_*`）+ 写入（`save_*` / `update_*` / `delete_*`）；预检索近邻（≥ 0.5）作初始上下文；记忆侧先接上（多条提取 = 多次 `save_memory`），新增 `UserMemoryRepo.Update`；NONE 走 `UpdateConfirm`。
2. **笔记接入**：`record_note` 内跑同一个循环；先同步，慢则加 `note_decide` 延迟任务。
3. **topic 复用**：`FindSimilarTopics` + 接在 `save_*` 内部（阈值 0.80）+ 命名校验。
4. **路由修正**：改 prompt。

**验收**：

- `go build ./... && go test ./...` 全绿，加针对性单测（循环的工具分发与 `idSet` 校验、topic 相似度、上限计数）。
- 真机三测：
  1. 一次多主题会话 → 记忆应该出 2~5 条，且 topic 复用已有。
  2. 故意记一条和已有重复的笔记 → 应自动 update 或 delete。
  3. 用一个和旧 topic 很像的新 topic（≥ 0.80）→ 应自动落到旧 topic 上。

**缝（不能自测、只能真跑的部分）**：循环里 LLM 的真实行为（会不会乱删、会不会忘了 5 条上限）、工具内同步跑循环的延迟体验、topic 阈值在真实数据上的表现、记忆多条提取的质量。其中「循环行为」打算先建一个探针——拿真实 session 窗口离线重放一遍，把工具调用序列导出来人工看。

## 8. 暂缓（记着，不着急）

- **退役 facts**：不停写、不删表和 Changelog。
- **存量梳理**：逐日流水那部分已在首批（`ledger-migration.md`）处理；其余存量（非流水的重复/过时记忆）仍待「从后往前」捋一遍，另起一票。
- **theme**：继续挂起。topic 收敛之后再做会简单很多。
- **tags**：另起一票退役，方案见 `tags-retirement.md`。

## 9. 风险与待观察

1. **LLM 误删**：全放（含 delete）意味着主 LLM 可能不知道循环删了什么。靠 `record_note` 的 description + 返回值缓解，观察期重点看。
2. **工具内同步跑循环的延迟**：每次记笔记多一个 2~4 秒的 LLM 循环。若超过体感阈值就转 `note_decide` 异步。
3. **记忆多条提取的噪声**：原子化 + 循环决策能否真的压住噪声，要用真实数据看；上限 5（prompt 明示 + 工具计数）是保险丝。
4. **循环自身漏判**：跨域召回靠向量，召回不到就判不出重复。近邻取 top_k + 0.5 阈值是当前折中，观察期看漏判率。
5. **循环不收敛 / 空转**：模型可能反复检索不写入。靠 `maxIterations` 上限兜底；记日志观察实际轮数分布。
