# 记忆 / 笔记改造 — 设计文档

> 版本：v1｜日期：2026-09-22｜状态：设计定稿，待开发
>
> 参照：《事实系统（Facts）· 设计文档》（`docs/design/facts-system/design.md`）、《新旧系统产出对比》（同目录 `old-vs-new-output-comparison.md`）、《记忆方案调研》（同目录 `memory-research.md`）
> 估算（开工）：10 面 / 4 缝（1 需探针）/ 2 次真机往返 / 5 个待拍决定 · ~7–12h
> 估算（定稿）：10 面 / 4 缝（1 需探针）/ 2 次真机往返 / 0 个待拍决定 · ~6–10h
>
> 备注：开工粗估那行是回填的——本需求的前半程（调研 + 定方向）没有单列粗估，范围与定稿时一致，故按同一批面/缝回填，仅把当时未拍的决定数记为 5。
> 排期：分首批 / 后续两段（见 §7）。首批另有两篇：`topic-merge.md`、`ledger-migration.md`；删除清理修复记在 `docs/bugs/017-vector-cleanup-on-delete.md`。
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

这个问题的反面价值在 §4.5 说——topic 一旦收敛，「一个 topic 下挂几十条记录」会成为常态，而**这对将来做 theme 反而是好事**。

## 3. 目标与非目标

**要做**：

- 把记忆提取从「一行/会话」改成「多条/会话」，每条独立嵌入，上限 5 条。
- 建一道**共享决策闸门**：新记录落库前先检索近邻、再让 LLM 判断 ADD / UPDATE / DELETE / NONE，然后执行。记忆和笔记共用这道闸门。
- 笔记的 `record_note` 内部接入闸门，**主 LLM 不参与判断**。
- topic 收敛：复用已有的概念嵌入机制，写 topic 时列出相似项、太相似直接拒绝。
- 修正 prompt 路由：逐日读数（睡眠、体重等流水）该进文档，不进记忆/笔记。

**不做**（这次明确不碰）：

- **退役 facts**——不停写、不删表。`user_memories.facts` 字段**保留**，它是将来做 ADD/UPDATE/DELETE 判断的重要依据。
- **tags**——见 §4.6，什么都不做。
- **存量数据梳理**——775 条记忆 + 990 条笔记的「从后往前」梳理，另起一票，不着急。
- **theme**——继续挂起（当前 4 个 active 主题里 2 个 `state_text` 还是空的），topic 收敛后自然会好做。
- **版本历史**——记忆和笔记都不新增版本能力。笔记现有的 `ContentVersion` 保留不动，但不再往上加东西。

## 4. 设计

### 4.1 总览

一句话：**把 facts 里那道「决策闸门」抽出来，作为通用件，接到记忆和笔记的写入路径上；顺手把记忆提取拆成多条、把 topic 收敛。**

写入路径变成两条，闸门是共享的：

```
记忆（会话结束，后台）:  提取多条候选 → ┐
                                        ├→ 决策闸门 → ADD/UPDATE/DELETE/NONE → 落库
笔记（对话中，工具内）:  一条候选     → ┘
```

闸门内部：**向量近邻检索（跨记忆 + 笔记两域）→ 可选自搜索 → LLM 决策 → 执行**。

设计上有一条重要的原则：**记忆让位笔记**。笔记是「重要、当场记」，记忆是「次要、事后补」。笔记在对话中先写，记忆在会话结束后写，所以后写的记忆一旦发现笔记已经覆盖了同一件事，就应该让位（NONE，或删掉自己那条）。互斥由记忆这一侧负责执行。

### 4.2 记忆提取改成多条

文件：`agent/extractor.go`。

把 LLM 的输出从「一个对象」改成「一个列表」：

```json
{"extracted": true, "memories": [
  {"topic": "睡眠管理", "tags": "褪黑素,早醒", "summary": "...", "facts": ["..."]},
  {"topic": "职业规划", "tags": "...",        "summary": "...", "facts": ["..."]}
]}
```

- **一条 = 一个 topic + 一句 summary + facts 细节**。同一件事合成一条，不同的事分开。
- 每条独立落一行 `user_memories`，**每条各自嵌入 `memory_value`**——这样每条都能被检索到，不再只有一句话。
- 硬上限 **5 条/会话**（配置项 `server.memory_max_per_session`），防止噪声。
- `facts` 字段继续存这条的细节，不删。
- 提示词同步改：明确「一条 = 一个 topic」，说明上限。解析器保留对旧单对象格式的兼容，作为兜底。

为什么原子化这次可行、上次不行：上次 facts 是「原子 + 无闸门」，噪声无人拦；这次每条候选都要过 §4.3 的闸门，噪声在门口就被挡掉。

### 4.3 共享决策闸门（核心）

新建 `agent/record_decider.go`。它的前身是 facts 的决策段——`fact_worker.go` 里的 `applyCandidate`(:126)、`retrieveNeighbors`(:171)、`FactDecider.Decide`(:486) 和它用的提示词。我们**把这些抽出来、改写成对「记录」通用**，而不是对「事实」专用。

输入：一条候选记录（来自记忆提取，或来自 note 工具的参数）。

流程：

1. **向量近邻检索**，跨**记忆 + 笔记两个域**（无阈值，取 top_k）。跨域是关键——只有跨域才能实现「同一件事不在两处各存一份」。
2. **可选自搜索**：给决策器的 LLM 开放 MiniReAct 自搜索，白名单**只开 `note.search` 和 `memory.search`**，让它能主动多查几轮再决定。
3. **LLM 决策**（Flash）：输出 `{op: add|update|delete|none, target_id, topic, ...}`。
4. **执行**：

| op | 记忆侧 | 笔记侧 |
|---|---|---|
| add | `Create` | 插入 |
| update | **新增** `UserMemoryRepo.Update`（改 topic/summary/facts/tags + 重新嵌入） | `update_note` |
| delete | `Delete`（已有） | `delete_note` |
| none | `UpdateConfirm`（**复用已有的 `confirm_count` / `last_confirmed_at`**，`user_memory_repo.go:134`） | 无操作 |

注意「none」那一格：命中了旧记录但内容没变，不算白跑——把确认计数 +1、刷新确认时间，正好用上早就存在、却一直没派上用场的两个字段。**不新增任何数据库列。**

互斥的落点：**记忆决策器**跨查两域，命中笔记即 NONE 或删掉自己（记忆让位笔记）；**笔记决策器**主要查笔记域，可选地删掉被顶替的旧记忆。这样无论谁先写，最终一致。

### 4.4 笔记接入（工具内跑决策，主 LLM 不参与）

文件：`tool/note.go` 的 `record_note`。

- 调用时，工具内部直接跑 §4.3 的闸门——**主 LLM 完全不参与判断**。主 LLM 只负责表达「我要记这条」。
- **全放**：允许自动 add / update / delete（你确认过，风险可接受）。
- 代价是必须说清楚：`record_note` 的 description 要写明「可能与已有笔记重复，会自动新增/更新/删除」；返回值带上 `{action, id, note}`，让主 LLM 知道刚才到底发生了什么。
- **先同步**跑决策（Flash + 近邻不多，估计 2 秒内够用）。真机测下来如果慢，再落到 `DelayedTask`（新增 `TaskType = note_decide`，`model/delayed_task.go` 的 `TaskType` 字段和 CronRunner 分发都现成），异步时工具立即返回「已记录（整理中）」。

### 4.5 topic 收敛

**好消息：这套机制的零件已经全在了，只是没人用它来收敛 topic。**

- topic 早就被嵌入了：`ConceptEmbedService.EnsureTopicEmbedded` 会把 topic 写进 `concept_embeddings` 表并算好向量（`agent/concept_embedder.go:31`）。只是这些 topic 向量没进内存向量库，需要时得从 DB 读。
- 相似度比较和聚类也现成：`util.ClusterVectors` + `util.CosineSimilarity`（`internal/util/cluster.go`），`get_topic_stats.go:52-102` 已经在用「加载全部 topic 向量 → 聚类」这条路了。
- 阈值也现成：`cluster_threshold: 0.70`。

做法：新增一个 `FindSimilarTopics(userID, topic, threshold)`，加载该用户的 topic 向量、算余弦。然后两个写入路径接上：

- **笔记路径**：工具返回 `similar_topics` 列表；如果新 topic 跟某个已有 topic 相似度超过「拒绝线」，直接报错，要求主 LLM 从候选里重选。
- **记忆路径**：后台没有交互，程序化处理——超过拒绝线就落到最相似的那个已有 topic 上。

两级阈值（都可配）：

- **建议线**（`suggest`，默认 0.5）：超过就把它列出来给 LLM 参考，倾向复用。
- **拒绝线**（`reject`，默认 0.70）：超过就拒绝新建，必须复用已有的。

外加命名硬校验：禁止斜杠、逗号、多个主题，限制长度（记忆提取提示词里已有类似要求，笔记这边要补）。

topic 收敛之后，「一个 topic 下挂几十条记录」会成为常态——这正是我们想要的，它让 theme 以后变成一件简单事（按 topic 聚合即可）。

### 4.6 tags：不动

确认过，tags 现在**基本没用**，有两条证据：

1. `EnsureTagsEmbedded` 已经是个空实现（`concept_embedder.go:35-38`），tag 的向量早就不生成了。
2. 笔记和记忆上的 `tags` 字段，**只被 `get_topic_stats.splitTags` 读了一次**（`get_topic_stats.go:161,184`），检索链路（`retrieval/*_source.go`）完全不碰它。

所以：**不恢复嵌入、不加约束、不删列**，维持现状写入。将来真要用再说。

### 4.7 路由修正

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
| `memory_decide_enabled` | true | 决策闸门开关，便于灰度/回退 |
| `memory_max_per_session` | 5 | 单次会话最多提取几条记忆 |
| `memory_topic_suggest_threshold` | 0.5 | 列出相似 topic 的建议线 |
| `memory_topic_reject_threshold` | 0.70 | 拒绝新建 topic 的拒绝线 |

后两个阈值**先跑一段时间再看**，不合再调。

## 7. 分期与验收

> 2026-09-22 排期调整：先做**首批**（删除清理 bug + topic 收敛 + 台账挪走 + 措辞），把地基清干净；再动**后续**的记录决策器。topic 的「写入侧约束」从首批挪到后续。

**首批**（单独两篇：`topic-merge.md`、`ledger-migration.md`）：

1. **删除清理修复**（bug `017`）：抽共享 `PurgeEntityVectors`，修好笔记/记忆四个删除入口的派生数据残留；`concept_embeddings` 的 topic 行改走孤儿 GC。
2. **topic 存量收敛**：`server/cmd/topic-merge/`，leader 聚类出合并报告 → 人工确认 → 批量改 `notes.topic` / `user_memories.topic` + 概念表去重 + 孤儿 GC。
3. **台账挪走**（agent 执行）：逐日流水从 notes / memories 归入 document、删原记录。
4. **措辞修正**：note / 记忆提取 prompt 明确「台账不记入笔记与记忆，改走 document」。

**后续**（本 `design.md` 的正文设计）：

1. **闸门通用化**：抽 `record_decider.go`，记忆侧首先接上（含多条提取 + `UserMemoryRepo.Update`）。
2. **笔记接入**：`record_note` 内跑闸门；先同步，慢则加 `note_decide` 延迟任务。
3. **topic 写入侧约束**：`FindSimilarTopics` + 两个路径接入 + 命名校验。
4. **路由修正**：改 prompt。

**验收**：

- `go build ./... && go test ./...` 全绿，加针对性单测（闸门的分支、topic 相似度、提取解析）。
- 真机三测：
  1. 一次多主题会话 → 记忆应该出 2~5 条，且 topic 复用已有。
  2. 故意记一条和已有重复的笔记 → 应自动 update 或 delete。
  3. 用一个和旧 topic 很像的新 topic → 应被拒绝。

**缝（不能自测、只能真跑的部分）**：决策 LLM 的真实行为（会不会乱删）、工具内同步决策的延迟体验、topic 阈值在真实数据上的表现、记忆多条提取的质量。其中「决策 LLM 行为」打算先建一个探针——拿真实 session 窗口离线重放一遍，把结果导出来人工看。

## 8. 暂缓（记着，不着急）

- **退役 facts**：不停写、不删表和 Changelog。
- **存量梳理**：逐日流水那部分已在首批（`ledger-migration.md`）处理；其余存量（非流水的重复/过时记忆）仍待「从后往前」捋一遍，另起一票。
- **theme**：继续挂起。topic 收敛之后再做会简单很多。
- **tags**：维持现状。

## 9. 风险与待观察

1. **LLM 决策误删**：全放（含 delete）意味着主 LLM 可能不知道工具删了什么。靠 description + 返回值缓解，观察期重点看。
2. **工具内同步决策的延迟**：每次记笔记多一次 LLM 调用。若超过体感阈值就转异步。
3. **记忆多条提取的噪声**：原子化 + 闸门能否真的压住噪声，要用真实数据看；上限 5 是保险丝。
4. **决策闸门自身漏判**：跨域检索靠向量召回，召回不到就判不出重复。近邻取 top_k、无阈值是当前折中，观察期看漏判率。
