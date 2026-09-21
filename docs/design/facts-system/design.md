# 事实系统（Facts）— 设计文档

> 版本：v1 | 日期：2026-09-21 | 状态：设计定稿，待开发
>
> 参照：《事实系统设计 · 汇总（2026-09-20）》《事实系统 · 两段提示词（复原 · 2026-09-21）》
> 估算（开工）：8 面 / 4 缝（0 条需先建探针）/ 1–2 次真机往返 / 2 个待拍决定 → 6–10 小时
> 估算（定稿）：10 面 / 4 缝（0 条需先建探针）/ 1–2 次真机往返 / 0 个待拍决定 → 8–12 小时

---

## 1. 背景与问题

现有长期记忆由三套同构的东西各存一份：**记忆（`user_memory`）、笔记（`notes`）、主题（`theme_state`）**。它们互相不知道对方存在，同一件事可能三处都有、措辞还不同。

四个具体问题：

1. **同构重复**——记忆与笔记非常同构，是否都要有存疑。
2. **没有去重机制**——都是单条插入，一件事重复记到多处。
3. **没有修订与退役机制**——过期的事实不会修、不会改。
4. **事实散落在几处**——没有一套机制把事实统合起来。

目标：借鉴 mem0，建立一套支持 **ADD / UPDATE / DELETE / NONE** 的事实记录系统。

## 2. 目标与边界

**要做**：

- 何时提取：沿用现状——会话空闲 `chat.session_gap_minutes`（默认 15min）触发 `session_end`，取当前 session 窗口，不分段。
- 怎么提取：两段 LLM（抽取、决策）＋ 中间一段纯代码检索。
- 提取什么：四字段 `subject / kind / fields / content`。
- 如何存储：MySQL 两张表（`facts`、`fact_changelog`）＋ `Embedding` 表 ＋ 内存向量。
- 如何监控：复用 `llm_call_io`。
- 旧数据兼容：双写，不迁移。

**不做**（明确划界）：

- **注入**——新系统只写入、不注入，本次不参与主对话检索（后续单独设计）。
- **A 类（会话中即时记录）**——只做 B 类（会话结束后处理）。
- **`subject` / `kind` 参与检索**——`subject` 降为分组／统计标签，`kind` 只统计，`content` 是唯一参与检索的字段。
- **`fields` 注入**——只落库，不进任何 prompt。
- **回滚能力**——UPDATE / DELETE 原地覆盖、不可逆，`changelog` 只观察。
- **旧系统下线**——记忆／笔记／主题照旧写、照旧注入，与新系统互不可见；观察期后再整体下线。
- **API / 前端 / Admin 看板**——本次无对外接口。
- **主题**——整节挂起，不在本次处理。

## 3. 概念模型

### 3.1 四字段

| 字段 | 说明 |
|---|---|
| `subject` | 这条事实关于谁／什么。**不一定是用户本人**，也可以是项目、政策、价格、机构。降为分组／统计标签，不参与检索。 |
| `kind` | 只有两个值：`state` / `event`。只统计，不参与检索（本次不注入）。 |
| `fields` | 可空。`due_at`（到期时间）、`has_doc_ref`（文档 ID）。只落库不注入。 |
| `content` | 一句话，不带日期前缀。**唯一参与检索的字段**。 |

- **`subject` 是最容易标错的一类**：通篇是「我」在讲，主体也可能是别人。抽取提示词里专门有一条 few-shot 教这个。
- **`kind` 只有一条判据：会不会被后来的事实取代。**
  - `state`：当前状态、身份、关系、计划、对某事的当前判断——会被取代，写入时按 subject 覆盖。
  - `event`：一次发生的事、观点——发生即固定，只增不改。**观点不许覆盖成「最新观点」**。
  - 计划 = `state` + `due_at`，不单开一类。
- **`content` 是嵌入、注入、比对的三重单位**，所以必须自足成句、指代在提取时消解完、主体名写进句子。

### 3.2 收录边界

**一句话判据**：这东西**能否在未来的对话中用来提供信息**。能——收录；只有攒起来看才有意义的——不收录（改路由到文档表格）。

**DO**：结论与判断、状态、身份与关系、计划、观点、对外部事物的判断；单条自足、脱离对话也读得懂的事实；会演化的东西（同一件事变了，覆盖旧值）；高信息量、能替代一次追问的内容。

**DO NOT**：

- **底层读数的时序流水**（睡眠、饮酒、汽水、运动、时间日志）——价值在集合，不在单条。正确去处是文档表格，这是**改路由**不是丢弃。
- 对话过程（谁问过、模型纠正过、绕了几步）。
- 助手的话。
- 寒暄与零信息量问答。
- 原文摘录、过程转述这类底稿。

两处澄清：

1. **流水不是没价值，是介质错**——模型若把它理解成「没价值」，会在台账和事实库两边都丢掉。
2. **`event` ≠ 流水**——event 是「发生了就固定」的离散事件（一次来访、一次争吵）；流水是连续读数。区分点不是「是否天天发生」（睡眠天天发生，但它是读数、不是断言）。

一段对话可能一条都不该记——那就输出空列表。

## 4. 流水线总览

```
session_end（会话空闲 15min）
  └─ PostChatWorkerGroup.Process（现有 session_end 管线）
        └─ FactWorker.Process(sess)
              ① 抽取（LLM）：对话 → 候选事实 [{subject,kind,fields,content}]
              ② 逐候选串行：
                   归一化判重（代码）
                   → 向量检索（代码，top_k=5，无阈值）
                   → 决策（LLM，原生 FC）→ ADD / UPDATE / DELETE / NONE
                   → 立即写入 + changelog
```

**为什么拆两段**：检索是纯代码、不调模型——抽取产出 `content`，代码拿它查近邻，再把近邻喂给决策段。抽取与决策仍不能合并：决策的输入依赖检索结果，而检索结果依赖抽取产出。

**为什么逐候选串行、每条处理完立即写入**：这是正确性前提，不是优化。候选 1 已入库，候选 2 的归一化判重直接命中。若改成整批一起写，批内互不可见，两条相似候选会都判 ADD，库内真重复。

## 5. 抽取段（LLM）

输入只有 `<对话>`（**不再接收 `<已存在事实>`**——检索已在 09-21 降为纯代码，候选由下游决策段处理）。few-shot 全部放这一段。

### 5.1 定稿提示词

抽取段用**专用** system prompt，不复用 `postChatSharedSystemPrompt`（那条写死了「输入的消息均为用户角色的发言」，与事实抽取喂 user＋assistant 的事实矛盾；且改共享常量会动 memory／summary／state／profile 的缓存前缀）：

> 你是一个对话后处理助手。输入是一段用户与助手的对话记录，按时间顺序排列，每个 turn 带发生时间。

指令消息（常量）：

````
你是事实记录员。你的工作是从对话中抽取值得长期保留的事实。

你不做摘要，不写心得，不给建议。

## 输入

<对话>：用户与助手的一段对话，按时间顺序排列，每个 turn 带发生时间。
用户消息里的「今天（2026-09-21）」是系统补的日期标注：原词是相对说法，括号里是它的绝对日期。

## 铁律

1. 只依据用户的消息。助手说的、系统说的，一律不记为事实。用户没确认的内容，不记。
2. 抽的是事实，不是对话过程。谁问过、你答了什么、绕了几步，都不记。
3. 一次输出多条。一段对话里可能有若干条事实，逐条输出，不许合并成一条综述。
4. content 必须自足成句：脱离这段对话也能读懂。主体名（说的是谁）要写进句子；「她」「那个项目」「上次说的」一律换成确定的名词。
5. 时间只写绝对日期，格式 YYYY-MM-DD。不许出现「今天」「昨天」「下周」这类相对说法，也不许把系统标注的括号抄进 content。
6. 不确定就不记。记错的事实比漏掉更难清理。只写对话里出现过的内容，不许补齐、不许推断。
7. 助手的话不构成事实来源，除非用户确认。助手复述用户已知内容的，更不记。
8. 以最终陈述为准：用户改口、助手误判被纠正，只留最终版，中间态不产出多条。
9. 段内去重：同一事实在一段对话里被提两次，只产出一条。

## 每条事实的四个字段

- subject：这条事实关于谁／什么。取对话里真实出现的指称（人名、项目名、机构名），不要自造代号。
- kind：只有两个值，state 或 event。
- fields：可空。due_at（到期时间，YYYY-MM-DD）、has_doc_ref（文档 ID）。
- content：一句话，只放一个断言。

subject 取这句话真正在说的对象。通篇是「我」在讲，主体也可能是别人——这是最常见的错。

kind 只看一件事：会不会被后来的事实取代。
- state：当前状态、身份、关系、计划、对某事的当前判断。会被取代。
- event：一次发生的事、观点。发生即固定，只增不改。观点不许覆盖成「最新观点」。

fields：日期、金额、期限是字段，不是类别。计划 = state + due_at。
「到期／期限」写进 fields.due_at，content 里不再重复它；其余时间（某件事发生在哪天）用 YYYY-MM-DD 写在 content 的句子里。

## 什么该记，什么不该记

该记：结论、状态、身份与关系、计划、事件、观点、对外部事物的判断。
不该记：逐日读数（睡眠时长、步数、体重这类时序数据，走台账）、助手的话、对话过程、寒暄、零信息量的问答。
一段对话可能一条都不该记——那就输出空列表。

## content 是什么

content 是句子，不是要点列表；一条只放一个断言。

❌ {"subject":"老K小程序","kind":"state","content":"本人兼产品与开发、只做前端、9/21 演示"}
✅ {"subject":"老K小程序","kind":"state","content":"本人这次在老K的旅游案例展示小程序上兼产品与开发，原定位只做技术"}

## 样例

输入统一为「一个或多个 turn 的对话」，单条样例也写成只有一个 turn 的对话，不写成「一句话」。

例 1 · 多轮：跨轮拼装、以最终陈述为准
<对话>
  <turn role="user" at="2026-09-18 21:30">老K那个小程序你还记得吧，旅游案例展示加签证代办。</turn>
  <turn role="assistant" at="2026-09-18 21:30">记得，你提过初稿。现在到哪步了？</turn>
  <turn role="user" at="2026-09-18 21:31">还没动。我本来只做技术，这次可能产品和开发都我来。</turn>
  <turn role="assistant" at="2026-09-18 21:31">那得先定原型？</turn>
  <turn role="user" at="2026-09-18 21:33">嗯，想用工具生成 UI 提示词。9 月 21 号跟他对需求。</turn>
  <turn role="assistant" at="2026-09-18 21:33">21 号是交付日吗？</turn>
  <turn role="user" at="2026-09-18 21:34">不是，那天他回程，就当面演示一下。后台来不及就只做前端。</turn>
</对话>
输出：
{"facts":[
  {"subject":"老K小程序","kind":"state","fields":{"due_at":"2026-09-21"},"content":"本人这次在老K的旅游案例展示小程序上兼产品与开发，原定位只做技术"},
  {"subject":"老K小程序","kind":"event","content":"2026-09-21 不是交付日，本人与老K当面演示该小程序；后台来不及就只做前端"}
]}
不记：开场寒暄、助手那句误判与澄清提问。第 2 条是跨三轮拼出来的——日期、性质、退路缺一轮都不完整。

例 2 · 主体不是本人（通篇是「我」在讲，主体也可能是别人）
<对话>
  <turn role="user" at="2026-09-20 22:10">今天和配偶吵架了。我妈在群里说买了新电动车，配偶看见就问为啥要买，我说你要是不愿意我可以说你不让，然后就吵起来了，她把家群也退了。我对她们的沟通很紧张，有点 PTSD。买房、生孩子我都不想做了，只想躺平。</turn>
</对话>
输出：
{"facts":[
  {"subject":"配偶","kind":"event","content":"配偶因本人母亲在家族群聊里买新电动车一事与本人争吵，并退了家族群"},
  {"subject":"本人","kind":"state","content":"本人对母亲与配偶之间的沟通有创伤式反应"},
  {"subject":"本人","kind":"state","content":"本人不想买房生育了，只想躺平"}
]}

例 3 · 不产出事实
<对话>
  <turn role="user" at="2026-09-21 08:52">昨天十一点半多睡的，入睡不算顺利，今天睡到八点五十，4.5 分，头还微微疼。</turn>
</对话>
输出：{"facts":[]}
逐日读数，单条脱离序列无意义，走台账，不进事实库。

## 输出

JSON，一个键 facts：
{"facts":[{"subject":"老K小程序","kind":"state","fields":{"due_at":"2026-09-21"},"content":"本人这次在老K的旅游案例展示小程序上兼产品与开发，原定位只做技术"}]}
````

**content 规格（09-21 定）** —— content 是嵌入／注入／比对的三重单位，必须自足成句：主体名写进句子、一条只放一个断言、不用「、」串要点。时间口径：

- 「到期／期限」→ `fields.due_at`（ISO），content 不重复它；
- 其余时间（某件事发生在哪天）→ 用 `YYYY-MM-DD` 写在 content 的句子里；
- **禁相对时间词**（今天／昨天／下周）：它锚在说话时刻，事实库活几个月就失去意义；更危险的是归一化判重会因此**静默合并两条不同时间的事**；
- **禁把机器标注的括号抄进 content**：user 消息落库时被管线写成「原词（日期）」（`今天（2026-09-21）`）。

**输入渲染**：每个 turn 带 `at="YYYY-MM-DD HH:MM"`（北京时间，取自 `CreatedAt`）。user 消息喂**规范化后的 `Content`**——日期已被 `NormalizeDatesStep` 确定性解出，不让模型自己算相对日期；assistant 消息不经管线，靠 `at` 提供时间锚点。`Content` 为空的 turn（只发 tool call 的 assistant）跳过。

### 5.2 三条口径

1. 一次要能输出**多条**、各自带 subject／kind。
2. 拆分粒度**看主体与 kind 是否不同**，不按句子长度。
3. **钱的账进记账系统**，事实库只留事实。

### 5.3 实现要点

- 指令消息（常量）放**最前**，对话紧随其后——常量前缀命中 DeepSeek 前缀缓存。
- `SystemPrompt` 用 `factExtractSystemPrompt`（抽取段专用，见 §5.1；不复用 `postChatSharedSystemPrompt`）。
- `ChatSync` + `ParseFlashJSON` 解 `{facts:[...]}`。
- 采样温度走 `facts.temperature`（`*float64`，nil = 用模型默认；默认 0.2）——抽取是格式敏感任务，压低减少波动。
- 校验：`kind ∈ {state,event}`；`content` 非空且去空白后非空；条数上限 `facts.max_candidates`（默认 5），超出截断并记日志。
- 抽取产出**候选**，不落库；由决策段逐条处理。

## 6. 检索（纯代码）

对每条候选 `f`（带 `user_id` 过滤）：

1. `vec = Embedder.Embed(f.content)`。
2. `results = vectorStore.Search(vec, top_k=5, filter: metadata["type"]=="fact" && metadata["user_id"]==uid)`。
3. **不设阈值**（照 mem0），top5 全部交给决策段。
4. 近邻格式化成 XML：`<fact id="..." subject="..." kind="...">正文</fact>`（`id` 去掉 `fact:` 前缀）。**相似度不渲染给模型**——判据是「同一主体 + 同一断言位」，分数不参与；近邻已按相似度降序截断，顺序隐含强弱。

预算：每条候选 1 次向量查询 + 1 次 embedding。近邻的 subject／kind／content 直接从向量 metadata 取（upsert 时写入），**不回表**。

「断言位」仍是判据支点，只是由模型在第 ② 步判断，不再是检索键。

## 7. 决策段（LLM，原生 FC）

### 7.1 定稿提示词

> 下面两段由 `fact_worker.go` 的常量自动同步（改提示词 = 改 Go 常量，再重跑同步脚本）。

**system prompt**（`factDecideSystemPrompt`）：

```
你是一个事实库的写入决策助手。输入是一条候选事实和它在事实库里检索到的近邻事实。
```

**指令**（`factDecideInstruction`，放在 user 消息最前，候选块紧随其后）：

```
你是事实库的写入决策者。上游给你一条候选事实，以及它在事实库里检索到的近邻事实。
你要为这条候选选一个动作：新开一条、改一条、删一条，或者什么都不做。

## 输入

<candidate>：待写入的候选，字段 subject / kind / fields / content。
<neighbors>：按 content 向量相似度从事实库取回的近邻（不设阈值，可能一条都不相关）。
每个近邻写成 <fact id="..." subject="..." kind="...">正文</fact>，id 是它的唯一标识。

## 动作

- add_fact(subject, kind, content, fields?)：新开一条。
- update_fact(target_id, content, subject?, kind?, fields?)：近邻里有一条在回答同一个问题，答案变了或者更全。
- delete_fact(target_id) 加 add_fact：候选被近邻里某条明确否定。
- 什么都不调：与某条近邻同义，跳过。

## 判据

同一个问题 = 同一主体 ＋ 同一断言位。
库里有「本人只做技术」，候选是「本人兼产品开发」——同主体、同一断言位（他在这个项目里担任什么），答案变了 → update_fact。

update 还是 add，看是不是同一条事实的更完整版本：多了一件无关的新事 → add_fact；同一件事变了 → update_fact。

delete 只用于明确否定：「我不想再管那笔债了」否定「计划为家里还债」→ delete_fact；只是补充细节 → update_fact。

观点不许覆盖成「最新观点」。观点是 event，说出口就固定，只增不改。

近邻可能一条都不相关，也可能内容相近但不是同一件事。以判据为准，不要因为拿到了 id 就硬找一条来改。

## 输出

这条候选只选一个动作（delete 加 add 算一个组合），不许返回整库新状态。
写成 tool call。
target_id 只能取自输入里的 id，不许自造。
update_fact 的 content 必须写改写后的完整句子——自足成句、主体名写进句子，不是只写变化的那半句。
候选的 fields（如 due_at）与近邻旧值不同时一并给出；没有就不传。
```

### 7.2 实现要点

- 定义一个含 3 个 `FuncDef` 的 `llm.ToolDefinition`：

| 函数 | 参数 | 语义 |
|---|---|---|
| `add_fact` | `subject, kind, content, fields?` | 新开一条 |
| `update_fact` | `target_id, content, subject?, kind?, fields?` | 覆盖近邻中同一条事实（`content` 必须写改写后的完整句子） |
| `delete_fact` | `target_id` | 明确否定并删除近邻中某条 |

- `ChatSync` 带 `Tools`；**每条候选一次调用**（串行可见性的前提），`resp.ToolCalls` 为空 = `NONE`。
- **`target_id` 校验**：必须落在本次近邻 id 集内，否则丢弃该 tool call 并记日志（防自造 id）。
- 一次决策可返回多个 tool call（`DELETE(id) + ADD`）；`Arguments` 解不出、`kind`/`content` 缺失一律丢弃该 call 并记日志。
- `system prompt` 用**专用串** `factDecideSystemPrompt`（不是 `postChatSharedSystemPrompt`——那条描述的是「用户发言的时间序对话」，与「候选 + 近邻」的输入对不上）；指令（规则文本）+ 候选块作为单条 user 消息，无 few-shot，常量前缀在前以命中 provider 缓存。

## 8. 判重（两级）

| 层次 | 判据 | 处理 |
|---|---|---|
| 代码 | 归一化后相等／hash 一致 | 静默跳过 |
| 代码 | 向量 top1 ≥ `facts.dupe_threshold`（默认 0.95） | 静默跳过 |
| 决策段 | 其余全部 | `top_k=5` 近邻交模型（**不设阈值**，0.85–0.95 一带照常进决策） |

- **归一化** = 剥空白、标点、全半角差异与大小写。**保留数字与日期**（09-21 修正：原先写的「剥掉日期前缀」会让「和老K吵架」在不同日期说两遍时 hash 相同 → 静默丢事实）；`content_hash = md5(normalize(content))`。
- **归一化判重排在检索之前**——先把不花钱的做掉。
- 判重与幂等落在**写入侧（代码）**，指标不依赖模型的自觉；命中即静默跳过并计数。

## 9. 写入语义

每条候选按决策结果：

| 决策 | 动作 |
|---|---|
| `ADD` | 插入 `facts`（status=active）；embed + `vectorStore.Upsert` + `Embedding` 行。无 changelog。 |
| `UPDATE(id)` | 读旧值 → 更新 `content`／`content_hash`（subject/kind 若变一并更新；`fields` 传了就整块覆盖、没传保留旧值）→ 写 `fact_changelog(UPDATE, old, new)` → 重新 embed + upsert 向量（metadata 用新正文）。 |
| `DELETE(id)` | 软删 `facts.status=deleted` → 写 `fact_changelog(DELETE, old, "")` → 删向量 + 删 `Embedding` 行。 |
| `NONE` | 无动作。 |

- **向量同步写**：DB 行与向量在同一候选内同步完成，保证候选 i+1 的检索看得见候选 i（串行判重的前提）。facts 量小，同步开销可接受。
- **`source_msg_id`**：抽取输出不带 msg id，取本 session **末条 user 消息 id**（best effort），仅 UPDATE／DELETE 记入 changelog。
- **不可逆**：UPDATE／DELETE 原地覆盖，本次不做回滚能力。

## 10. 数据模型

> 存储层实现细化（表结构 / repo / 向量契约 / 启动预热 / 迁移接线 / 配置）见 [storage.md](./storage.md)。

### 10.1 `facts`

```sql
CREATE TABLE facts (
  id           VARCHAR(36) NOT NULL PRIMARY KEY,   -- uuid
  user_id      VARCHAR(36) NOT NULL,
  subject      VARCHAR(200) NOT NULL DEFAULT '',
  kind         VARCHAR(16)  NOT NULL,              -- state / event
  content      TEXT         NOT NULL,
  content_hash VARCHAR(64)  NOT NULL,              -- md5(normalize(content))
  fields       JSON         NULL,                  -- {"due_at":"...","has_doc_ref":"..."}
  status       VARCHAR(16)  NOT NULL DEFAULT 'active',  -- active / deleted
  created_at   DATETIME     NOT NULL,
  updated_at   DATETIME     NOT NULL,
  INDEX idx_fact_user_subject (user_id, subject),
  INDEX idx_fact_user_hash (user_id, content_hash)
);
```

```go
type Fact struct {
    ID          string    `gorm:"primaryKey;size:36"`
    UserID      string    `gorm:"index:idx_fact_user_subject;index:idx_fact_user_hash;size:36;not null"`
    Subject     string    `gorm:"index:idx_fact_user_subject;size:200;not null;default:''"`
    Kind        string    `gorm:"size:16;not null"`
    Content     string    `gorm:"type:text;not null"`
    ContentHash string    `gorm:"index:idx_fact_user_hash;size:64;not null"`
    Fields      *string   `gorm:"type:json"`
    Status      string    `gorm:"size:16;not null;default:active"`
    CreatedAt   time.Time `gorm:"autoCreateTime"`
    UpdatedAt   time.Time `gorm:"autoUpdateTime"`
}
```

- `user_id` 是库的边界，恒定，与 `subject` 是两层，不合并。
- `content` 是唯一参与检索的字段。

### 10.2 `fact_changelog`

```sql
CREATE TABLE fact_changelogs (
  id            VARCHAR(36)  NOT NULL PRIMARY KEY,   -- idgen（雪花 ID 字符串）
  user_id       VARCHAR(36)  NOT NULL,
  fact_id       VARCHAR(36)  NOT NULL,
  op            VARCHAR(16)  NOT NULL,               -- UPDATE / DELETE
  old_content   TEXT         NOT NULL,               -- UPDATE / DELETE 都必须有
  new_content   TEXT         NOT NULL DEFAULT '',    -- 仅 UPDATE
  source_msg_id VARCHAR(36)  NOT NULL DEFAULT '',
  created_at    DATETIME     NOT NULL,
  INDEX idx_factlog_fact (fact_id),
  INDEX idx_factlog_user (user_id)
);
```

### 10.3 向量

- 沿用 `Embedding` 表：`EntityType="fact"`，`EntityID=fact.ID`，`Embedding=EmbeddingToString(vec)`。
- 内存 `VectorStore` key：`fact:<id>`；metadata：`{type:"fact", user_id, subject, kind, content}`。
- `model/embedding.go` 新增常量 `EntityTypeFact = "fact"`。
- **预热**：`WarmupAll` 增加 facts 分支，启动时把 `status=active` 的 facts 向量加载进内存（只加载 active，软删的不进）。
- 无 chunk：facts 是单句，不需要 `chunk_builder`（`chunk_disabled_parents` 无需包含 fact，因为根本不建）。

## 11. 监控

复用 `llm_call_io` 这一套：装饰器在调用点注入场景元信息，写入 `llm_call_io` 表，后期再读它展示。

- `llmcost/types.go` 新增两个子类常量：
  - `SubFactExtract = "fact_extract"`（post_chat）
  - `SubFactDecide = "fact_decide"`（post_chat）
- `llmcost/io_audit.go` 的 `auditWhitelist[CategoryPostChat]` 加这两条，两段 LLM 调用都进审计。
- 调用点 `WithScenario`：抽取段 `{Category: post_chat, SubCategory: fact_extract, UserID, ConvID}`；决策段 `{..., SubCategory: fact_decide, RefType: "fact", RefID: <候选序号>}`。

## 12. 配置

新增顶层 `facts:`：

```yaml
facts:
  enabled: true            # 总开关
  top_k: 5                 # 决策段近邻数
  dupe_threshold: 0.95     # 向量 top1 静默跳过阈值
  max_candidates: 5        # 单次抽取候选上限
  temperature: 0.2         # 抽取/决策段采样温度；*float64，nil = 用模型默认
```

- `config.Config` 加 `Facts FactsConfig \`yaml:"facts"\``。
- 默认值在 `config.go` 默认段设置（`top_k=5`、`dupe_threshold=0.95`、`max_candidates=5`），合并逻辑按现有「非零值覆盖」；`temperature` 是指针，合并用 `if src != nil`（同 `llm.default_temperature`）。
- 后台总开关 `server.disable_background_tasks` 关闭时，`session_end` 整条管线不跑，facts 自动继承。

## 13. 旧数据兼容

**双写**：旧系统（记忆、笔记、主题）整体保持现状不动——照旧写、照旧注入；新系统同期上线，**只写入事实、不注入**。两边互不可见。

- 上线后双写一段时间（观察期 1–2 周），旧库整体下线；下线不做归档。
- 老记忆不带走：某段旧事日后被重新提起时，由新链路重新记录。
- 本轮不做迁移，不跨库去重。
- 记忆与笔记：照旧写、届时下线。

## 14. 代码接线

### 14.1 新增文件

> 前四个（model / repo）属存储层，细化见 [storage.md](./storage.md)。

| 文件 | 说明 |
|---|---|
| `server/internal/model/fact.go` | `Fact` 模型 |
| `server/internal/model/fact_changelog.go` | `FactChangelog` 模型 |
| `server/internal/service/fact_repo.go` | facts 数据访问 |
| `server/internal/service/fact_changelog_repo.go` | changelog 数据访问 |
| `server/internal/agent/fact_extractor.go` | 抽取段：提示词 + `ChatSync` + 解析 |
| `server/internal/agent/fact_worker.go` | `PostChatWorker` 包装 + 决策段（判重 + 检索 + 原生 FC + 写入），逐候选串行 |
| `server/internal/agent/fact_extractor_test.go` | 抽取解析／kind 校验 |
| `server/internal/agent/fact_worker_test.go` | 四分支 + 跳过 + 自造 id 拒绝 + 串行可见性 |
| `server/internal/agent/fact_normalize_test.go` | 归一化／hash 边界 |

> 决策段不单开文件：`FactWorker` 本身只做开关/场景/串行编排，很薄；把决策逻辑折进 `fact_worker.go`，与记忆模块的 `extractor.go` + `memory_worker.go` 两文件制一致。细化见 [extraction.md](./extraction.md)。

### 14.2 修改文件

| 文件 | 改动 |
|---|---|
| `model/embedding.go` | 加 `EntityTypeFact` 常量 |
| `app/init_db.go` | `AutoMigrate` 加 `&model.Fact{}`、`&model.FactChangelog{}` |
| `app/init_repos.go` | `Repos` 加 `Fact` / `FactChangelog` 字段 + 构造 |
| `agent/warmup.go` | 加 `warmupFactEmbeddings`，`WarmupAll` 调用 |
| `service/message_repo.go` | 新增 `ListChatMessagesBetween`（user + assistant，排 tool / internal） |
| `agent/post_chat_group.go` | `SessionSnapshot` 携带 user+assistant，加 `GetUserMessages()` / `GetAllMessages()` |
| `agent/summary_worker.go` / `memory_worker.go` / `state_worker.go` / `profile_worker.go` | 改用 `sess.GetUserMessages()`（行为不变） |
| `llmcost/types.go` | 加 `SubFactExtract` / `SubFactDecide` |
| `llmcost/io_audit.go` | 白名单加两条 |
| `config/config.go` | 加 `FactsConfig` + 默认值 + 合并 |
| `conf/config.yaml` / `config.dev.yaml` / `config.prod.yaml` | 加 `facts:` 段 |
| `appinit/agents.go` | 构造 extractor/worker，`FactWorker` 追加进 `sessionEndGroup` |

### 14.3 装配（`appinit/agents.go`）

```go
factExtractor := agent.NewFactExtractor(&agent.FactExtractorConfig{
    LLMClient:     llmClients.BgFlash,
    MaxCandidates: cfg.Facts.MaxCandidates,
    Temperature:   cfg.Facts.Temperature,
})
factDecider := agent.NewFactDecider(&agent.FactDeciderConfig{
    LLMClient:   llmClients.BgFlash,
    Temperature: cfg.Facts.Temperature,
})
sessionEndGroup := agent.NewPostChatWorkerGroup([]agent.PostChatWorker{
    summaryWorker,
    memoryWorker,
    stateWorker,
    profileWorker,
    agent.NewFactWorker(&agent.FactWorkerConfig{
        Extractor:     factExtractor,
        Decider:       factDecider,
        Enabled:       cfg.Facts.Enabled,
        FactRepo:      repos.Fact,
        ChangelogRepo: repos.FactChangelog,
        Embedder:      infra.Embedder,
        EmbeddingRepo: repos.Embedding,
        VectorStore:   infra.VectorStore,
        TopK:          cfg.Facts.TopK,
        DupeThreshold: cfg.Facts.DupeThreshold,
    }),
}, repos.Message, ephemeralDestroyer)
```

> 决策段不单开文件：`FactDecider` / 归一化 / 写入都折进 `fact_worker.go`（`FactWorker` 只做开关、场景与串行编排）。逐候选串行与四种写入的细节见 [decision.md](./decision.md)。

- facts 用 `llm_bg`（`llmClients.BgFlash`），与其余后台 worker 一致。
- ID 约定：facts 用 uuid(36)（对齐 `UserMemory`）；changelog 用 `idgen.Next()`（string，对齐 `LLMCallIO`）。

## 15. 测试

按 AGENTS.md「只影响后台低危任务」口径：`go build ./... && go test ./...` 全绿 + 针对性单测 + 真机跑一次聊天。

单测（仿 `profile_worker_test.go` 的 fake LLM + sqlite）：

- `fact_normalize_test.go`：写法差异（标点／空白／全半角／大小写）归一到同 hash；**日期或数字不同必须不同 hash**；空串／纯标点。
- `fact_extractor_test.go`：JSON 解析；`kind` 非法被拒；空列表；超 `max_candidates` 截断。
- `fact_worker_test.go`（抽取段）：`enabled=false` 不消费 LLM；场景标注 `fact_extract`；渲染含 assistant turn。
- `fact_worker_test.go`（决策段）：
  - ADD / UPDATE / DELETE / NONE 各一；
  - 精确 hash 命中跳过（**不花 LLM 的钱**）；
  - `top1 ≥ dupe_threshold` 静默跳过；
  - **`target_id` 不在近邻集内被拒**；`Arguments` 是坏 JSON 被丢弃且不 panic；
  - 单条候选的决策调用失败不拖垮后续候选；
  - 串行可见性：候选 2 的检索请求里出现候选 1 刚写入的 id。

真机验证：跑一次聊天 → 查 `facts` / `fact_changelogs` / `llm_call_io`（`fact_extract` / `fact_decide`）与日志无报错。

## 16. 后续（超出本次范围）

- **注入**：如何把 facts 注入主对话（本次不做，单独设计）。
- 旧记忆／笔记／主题整体下线（观察期后）。
- `subject` / `kind` 参与检索或注入。
- `fields`（`due_at`）注入与提醒联动。
- 回滚能力（changelog 已落 `old_content`，具备数据基础）。
- Admin 看板展示 facts 与 changelog。
- A 类（会话中即时记录）。
- 主题系统（整节挂起）。

## 17. 待拍决定

无（四个关键分歧已定：抽取段不接收候选、决策段用原生 FC、`fields` 加可空 JSON 列、向量循环内同步）。
