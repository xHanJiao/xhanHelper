# 记录循环 — prompt 与工具契约

> 版本：v3｜日期：2026-09-23｜状态：设计定稿，待开发
>
> 上位：《记忆 / 笔记改造 · 设计文档》（`design.md`）§4.1–4.2；流程图见 `flow.md`；笔记退役见 `note-retirement.md`
> 覆盖细化项：① system prompt / 指令消息全文；② 工具集 schema + `target_id` 命名空间
>
> v2 是「单一实体、单循环」的版本（提取也在循环里）；v3 按 2026-09-23 的补充决定改为**两段式**——**抽取段**（`post_chat` 单次调用出 N 条候选，`inline` 由主 LLM 直接给）**+ 逐候选决策循环**（本文档的对象，ReAct）。理由见 `design.md` §4.1。

## 0. 本循环只管决策，不管提取

决策循环是**两段式里的第二段**，输入永远是**一条已经定好的候选**，从来不是对话原文：

| 入口 | 候选从哪来 |
|---|---|
| `inline` | 主 LLM 调 `record_memory(topic, summary, facts)` 直接给 |
| `post_chat` | 抽取段（`MemoryExtractor`，单次 `ChatSync`，`{memories:[...]}`，≤ 5 条）逐条喂进来 |

所以本循环的 prompt **不写「从对话里提炼」**——那是抽取段的职责（`extractor.go` 的 `memoryInstructionXML`，本批保留并改造其输出结构）。本循环只回答一件事：**这条候选，该怎么入库。**

### 0.1 「记忆是什么」只有一份定义

链路上有三处 prompt 需要知道「记忆是什么」，全部引用**同一个 Go 常量** `promptbuilder.MemoryDefinition`（`promptbuilder/memory_definition.go`），不得各写一份：

| 位置 | 怎么用 | 代码 |
|---|---|---|
| 抽取段（`post_chat`） | 拼进 task —— 「按这个定义从对话里捞候选」 | `agent/extractor.go` `buildMemoryInstructionXML` |
| 决策段（两入口） | 拼进 system prompt —— 「按这个定义判断候选该怎么入库」 | `agent/record_loop*.go` `recordLoopSystemPrompt` |
| 主循环准则 4 | `fmt.Sprintf` 嵌入 —— 「何时触发 `record_memory`」 | `promptbuilder/static.go` `corePrinciples` |

> 这段历史是个反例：三处曾各写一份，于是漂移了——抽取段说「最多 3 条」、主循环只说「反思、决定、经验、观点」、决策段干脆没定义。现在**改定义只动 `MemoryDefinition` 一处**（`corePrinciples` 因嵌入它，从 `const` 改为一次 `fmt.Sprintf` 的 `var`，进程内字符串不变，前缀缓存照旧稳）。

```go
// promptbuilder.MemoryDefinition（全文）
const MemoryDefinition = `记忆是从对话里沉淀下来的、将来聊天还会用到的用户长期信息，包括：用户的决定与理由、
结论与洞察、观点与判断、偏好与习惯、状态与计划、人物关系、经验教训，以及用户分享的外部信息。

一条记忆 = 一件事（一个 topic + 一句 summary + 若干 facts 细节），不是一段对话的摘要。
逐日流水（睡眠打卡、体重、饮酒、运动、时间日志等按天读数）不属于记忆，属于文档台账。`
```

## 1. system prompt（全文）

稳定常量，进 system 字段。**不复用 `postChatSharedSystemPrompt`**——那条写死了「输入均为用户角色的发言，按时间顺序排列」（`agent/profile_worker.go:26`），而这里输入是「一条候选 + 它的近邻」，两者对不上（`FactDecider` 当初也是这个理由另起一条，`fact_worker.go:353`）。

```go
// recordLoopSystemPrompt — 决策循环专用 system prompt。
// 拼接 promptbuilder.MemoryDefinition（全链路唯一定义），而不是另写一份。
var recordLoopSystemPrompt = "你是记忆库的写入决策助手。\n\n" + promptbuilder.MemoryDefinition +
	"\n\n你的输入是一条候选记忆，和它在记忆库里检索到的近邻记录。"
```

## 2. 指令消息（全文）

常量，拼在 user 消息最前，输入块（`<candidate>` + `<neighbors>`）紧随其后 —— 固定部分构成可命中的缓存前缀，变化部分只在尾部。结构照 `factDecideInstruction`（`fact_worker.go:359`：输入 / 动作 / 判据 / 输出）。

```go
// recordLoopInstruction — 决策循环的指令消息。
const recordLoopInstruction = `你是记忆库的写入决策者。上游给你一条候选记忆，以及它在记忆库里检索到的近邻记录。
你要为这条候选选一个动作：新开一条、改一条、删一条，或者什么都不做。

## 输入

<candidate>：待写入的候选，字段 topic / summary / facts。
<neighbors>：按 summary 向量相似度从记忆库取回的近邻（不设阈值，可能一条都不相关）。
每个近邻写成 <record id="…" type="memory|note" topic="…">正文</record>，id 是它的唯一标识。
type=note 的是旧笔记（过渡期遗留，见下）。

## 记忆的构成

一条记忆 = topic + summary + facts：

- topic：一个主题词，决定这条记忆归到哪一类（如「睡眠管理」「职业规划」）。同一件事必须用同一个 topic，不要造近义词。≤ 20 字，禁止斜杠、逗号、多个主题。
- summary：一句话概括核心信息，自足成句（主体写进句子里）。≤ 150 字。
  只有 summary 会被检索到，所以它必须自足——不能靠 facts 才读得懂。
- facts：支持这条 summary 的具体细节，0~10 条、每条 ≤ 100 字，如日期、数字、金额、要点。它跟随 summary 一起返回，不单独检索。

## 动作

- save_memory：新开一条。库里没有这件事。
- update_memory：近邻里有一条讲的是同一件事，而它变了、更全了、或需要修正 → 覆盖它。target_id 取自近邻的 id。
  - summary 写改写后的**完整内容**；facts 只写**新增的细节**——会与已有 facts 自动去重合并（只增不减，不逐出，事实不会被覆盖丢掉）。
- delete_memory：近邻里有一条被明确否定或已经作废。
- 什么都不调：近邻里已有一条同义记录，内容没变。

## 检索工具（只读）

<neighbors> 给的是按语义相似度取回的结果，可能漏掉你要找的那条。
关键词检索擅长字面量（人名、专有名词、日期、金额），相似度搜索恰恰不擅长这些。
需要时补一轮；够了就别反复搜。

## 判据

同一件事 = 同一主体 ＋ 同一件事本身。
库里有「只做技术」，候选是「兼做产品」——同主体、同一件事（他在项目里担任什么），答案变了 → update_memory。
事实性内容变了（改期、变更计划、数据修正）→ update_memory；只是补充细节 → update_memory；多了一件无关的新事 → save_memory。
观点/看法变了 → 新增一条，不要覆盖：旧观点是当时的真实记录，也有价值。
近邻可能一条都不相关，也可能内容相近但不是同一件事。以判据为准，不要因为拿到了 id 就硬找一条来改。

## delete 是最后手段

删除最重、不可逆，只有用户明确推翻时才用：
- 用：用户明确否定或作废（「那笔债不用还了」否定「计划还债」）。
- 不用：「新说法更全面」「措辞不一样」「看起来过时」「我想换个写法」——这些一律走 update_memory。
- 判不准就不动，或走 update_memory。宁可留着重复，也不要误删。

## 旧笔记（过渡期）

库里还留着一批旧笔记（type=note），是历史遗留，正在逐步清理：
- 不要新增或修改旧笔记。
- 如果发现某条旧笔记与候选讲的是同一件事，删掉那条旧笔记（delete_note）——内容已由记忆承接。
- 拿不准就留着，不要为了清理而删。

## 硬限制

- topic ≤ 20 字；summary ≤ 150 字；单条 fact ≤ 100 字。单次传入 facts ≤ 10 条（update 合并后不设上限）。超限会被工具拒绝。
- 一条候选最多新增 1 条记忆。上限由上游的抽取段控制，你不要试图拆分候选绕过它。

## 输出

- 用 tool call 表达，不要输出 JSON 文本。
- target_id 只能取自输入里的 id，不许自造。
- update_memory 的 summary 必须写改写后的完整内容（自足成句、主体写进句子），不是只写变化的那半句。
- update_memory 的 facts 是**增量**：只写这次新增的细节即可，已有 facts 会自动合并保留（只增不减、不逐出）。
- 相对时间（今天、昨天）在候选里若还是相对说法，落库前转成绝对日期（如 2026-09-15）。`
```

> 与 v2 的差异：system prompt 从「写入执行者」改成「写入决策助手」；输入从「对话原文 / 一条候选」统一成「一条候选」；删掉「写什么」（那是抽取段的职责，见 §0）与「一次最多 5 条」（上限落在抽取段，本循环只需说「不要拆分候选绕过」）。

## 3. 工具集 schema

三个 `ToolDefinition`（provider 会把函数名拼成 `memory__save_memory`；`splitToolFuncName` 取裸名，`fact_worker.go:528` 的既有做法）。**不进 `tool.Registry`**——不走注册表的校验/白名单/只读那套（同 `factDecideTool` 的说明，`fact_worker.go:396`）。

### 3.1 分组

| ToolDefinition | 函数 | 只读 |
|---|---|---|
| `record_search` | `search_memory_semantic` / `search_memory_keyword` / `search_note_semantic` / `search_note_keyword`（后两个**过渡期**） | ✅ |
| `memory` | `save_memory` / `update_memory` / `delete_memory` | ❌ |
| `legacy_note` | `delete_note`（**过渡期，只删不写**） | ❌ |

`legacy_note` 与 `search_note_*` 在 `notes` 清空后移除（见 `note-retirement.md` 阶段 3）。

### 3.2 函数签名

```go
// —— 检索 ——（底层复用 internal/tool/unified_search.go 的 semantic_search / keyword_search，types 限定域）
search_memory_semantic(query string, top_k int?)               → [{id, topic, summary, score}]
search_memory_keyword(keywords string, topic string?, top_k int?) → [{id, topic, summary}]
search_note_semantic(query string, top_k int?)                 → [{id, topic, content, score}]  // 过渡期
search_note_keyword(keywords string, topic string?, top_k int?)→ [{id, topic, content}]         // 过渡期

// —— 写记忆 ——
save_memory(topic string, summary string, facts []string?)                        → {action:"add", id}
update_memory(target_id string, topic string?, summary string, facts []string?)   → {action:"update", id}
delete_memory(target_id string)                                                   → {action:"delete", id}

// —— 清旧笔记（过渡期专用）——
delete_note(target_id string)                                                     → {action:"delete", id}
```

要点：

- **`save_memory` 不接收 topic 之外的定位信息**；topic 由工具内部做复用（`design.md` §4.4）。
- **`update_memory` 的 summary 是必填的完整内容**（不是增量），照 `update_fact` 的先例（`fact_worker.go:423`）。
- **`facts` 保留但不嵌入**（`design.md` §2.1 的解法是「多条提取、每条各自嵌 summary」）：它随 `update_memory` / `save_memory` 落库、随检索结果一起返回，但**不进向量库**。因此 summary 必须自足。
- **返回值带 `{action, id}`**：`inline` 入口由 `record_memory` 透传给主 LLM（`design.md` §4.3 要求带上 `{action, id, summary}`）；`post_chat` 入口只打日志与统计。

### 3.3 上限与校验落点

- **新增上限不在本循环**：一条候选最多 `save_memory` 一次（候选本身已经是抽取段拆好的原子单位）。上限 5 由**抽取段**控制（prompt 明示 + 解析后截断到 `memory_max_per_session`，见 `design.md` §4.2）。
- 循环侧只做一件事：同一条候选里，`save_memory` 若被调第二次，返回错误提示「一条候选只能写一条记忆」。
- 字段级硬限制（topic ≤ 20 字 / summary ≤ 150 字 / 单条 fact ≤ 100 字；单次传入 facts ≤ 10 条）是**工具侧校验**，超限返回错误让模型改写；**不设成配置**（是不变量，不是可调阈值）。
- `target_id` / `delete_note` 的校验见 §5。

## 4. 入口

两个入口**下发同一套工具**——只有一个实体，没有「只能写哪一侧」的区分。entry 只决定两件事：**候选从哪来**（§0）与**落库的 `source`**。

| entry | 触发 | 候选来源 | `source` 落库值 |
|---|---|---|---|
| `inline` | 对话进行中，主 LLM 调 `record_memory` | 主 LLM 给的 `topic` / `summary` / `facts` | `inline` |
| `post_chat` | 会话结束，后台 `MemoryWorker` | 抽取段（`MemoryExtractor`）逐条喂入 | `post_chat` |

**每条候选一个独立的循环实例**，输入块形态（两者相同；`facts` 可为空）：

```
<candidate><topic>睡眠管理</topic><summary>用户从 9 月中起固定 23:00 上床、早晨 6:30 起床</summary><facts>["2026-09-15 起因早醒调整作息","睡前停用褪黑素"]</facts></candidate>
<neighbors>
  <record id="3f2a…" type="memory" topic="睡眠管理">用户长期入睡困难，试过褪黑素</record>
  <record id="a91c…" type="note" topic="睡眠">9 月初开始记录睡眠时长</record>
</neighbors>
```

> 注：`user_memories.source` 现有取值是 `conversation` / `csv_import`（`model/user_memory.go:18`）。本批把 `conversation` 拆成 `inline` / `post_chat`；历史行的 `conversation` 保持不动（不迁移）。

## 5. `target_id` 命名空间

**统一用裸 UUID，不带 `mem:` / `note:` 前缀**——类型已经写在 `<record type="...">` 属性里，前缀是多余的。

| 来源 | id 形态 | 说明 |
|---|---|---|
| 预检索 `<neighbors>` | `id="<uuid>"` | 内存向量库的 key 是 `mem:<id>` / `note:<id>`（`model.VectorPrefixMemory` / `VectorPrefixNote`），**喂给模型前必须剥掉前缀** |
| `search_*` 返回 | `{"id": "<uuid>"}` | 同上，剥前缀后返回 |
| `target_id` 参数 | `<uuid>` | 校验：剥前缀后落在 `idSet` 内，**且类型匹配** |

两个坑，实现时必须盯：

1. **剥前缀**：近邻的 id 来自向量库 key（带前缀），检索工具返回的 id 来自 DB（裸）。若不在同一处统一剥掉，`idSet` 校验会把所有 `target_id` 判为非法，**症状是模型怎么调都「id 不存在」**。剥前缀后统一进 `idSet`。
2. **类型匹配**：`idSet` 只存裸 id 时，note 的 id 与 memory 的 id 会混在一个集合里。校验必须再查一次类型（`idType map[string]string`），否则 `delete_note` 可能被喂一个 memory 的 id —— 那就删错表了。

### 5.1 NONE 的确认计数（`memory_confirm_threshold`）

这条候选一个写入工具都没调时，取**这条候选**的近邻里**相似度最高的记忆**（`type=memory`），**且该相似度 ≥ `memory_confirm_threshold` = 0.80**，才调 `UpdateConfirm`（`user_memory_repo.go:134`，`confirm_count + 1` / `last_confirmed_at`）。

- 卡阈值的原因：NONE 只说「没有新东西要写」，不代表「最高分近邻就是被重申的那条」。相似度不够说明它只是碰巧排最近，不是同一条记忆。
- 不达阈值 / 无记忆近邻 → 无操作。**不新增数据库列**，纯复用已有字段（该字段目前只写不读，为将来攒信号——`retrieval/memory_source.go:28` 也会在检索命中时 +1）。

## 6. 与其他 prompt 的关系

| 现有 | 关系 |
|---|---|
| `postChatSharedSystemPrompt`（`profile_worker.go:26`） | **不复用**：它写死「输入均为用户角色的发言，按时间顺序」。 |
| `factDecideSystemPrompt` / `factDecideInstruction`（`fact_worker.go:355/359`） | **蓝本**：本次的指令结构（输入 / 动作 / 判据 / 输出）照它，但对象从「事实」换成「记忆」，并新增检索工具、旧笔记清理两节。两段的编排（抽取 → 逐候选决策）也照 `FactWorker`（`fact_worker.go:92/123`）。 |
| `memoryInstructionXML`（`extractor.go:36`） | **拆分 + 共用定义**：本批**保留抽取段**（改为输出 `{memories:[...]}`，上限 5），task 里的「什么算记忆」改为引用 `promptbuilder.MemoryDefinition`；本指令只管「这条候选怎么入库」，system prompt 同样引用它。 |
| 准则 4（`promptbuilder/static.go:14`） | **本批改措辞 + 共用定义**：主循环的行为准则（主 LLM 何时调 `record_memory`），「调用 note 记录 / 更新对应笔记」→ 改 `record_memory` / 对应记忆；「什么算高价值信息」改为 `fmt.Sprintf` 嵌入 `promptbuilder.MemoryDefinition`（见 §0.1）。 |
