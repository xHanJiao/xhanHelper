# 记忆 facts 逐字化（原话摘录 + 硬校验 + 覆盖）

> 版本：v1｜日期：2026-10-16｜状态：已实现（review 收口后），待真机验收
>
> 上位：《记忆质量 · 第一轮》（`docs/design/memory-quality/design.md`）——那一轮治的是「该不该写」，
> 这一轮治的是「写下来的 facts 是什么」。
> 参照：`docs/design/memory-note-rework/record-loop-prompt.md`（决策循环 prompt 契约）
> 估算（开工）：5 面 / 2 缝（2 需真机探针）/ 2 次真机往返 / 1 个待拍决定 · ~3h
> 估算（定稿）：5 面 / 2 缝（2 需真机探针）/ 2 次真机往返 / 0 个待拍决定 · ~3h

## 1. 问题

第二轮回收（2026-10-16，线上数据只读）确认：`user_memories.facts` 栏装的是 **LLM 自己的措辞**，
不是用户的话。三个层次：

**① 按设计就是概括，不是原话。** 三处措辞都写着「支持细节」：

- 抽取段 output schema：`"facts":["支持细节1"]`
- `record_memory` 工具参数说明：`支持细节（日期/数字/金额/要点），可选`
- `model.UserMemory.Facts` 注释：`支持细节列表（JSON 数组字符串）`

**② 会编。** 两个可证伪的实例（都取自 2026-10-14 一条 post_chat 记忆）：

- `"西安薪资门槛自定 18k+，北京飞书月薪 34k 是心理锚"` —— `34k` 在用户消息里只出现在 09-12
  （飞书 offer 那轮），与本次对话无关；`18k` 也不是「自定门槛」，是用户拿鲁特富莱（LotusFlare）西安的
  实际水平做参照，他还专门纠正过「18k 那份不是北京的，就是西安的」。
- `"行李和落脚点由 CJ 计划 11 月上旬回西安看房签约"` —— 全库搜「看房/落脚/签约」零命中。
  原文是「**我**这边帮忙打包好东西，回去那边先住酒店式公寓之类的」。凭空造的。

根因：抽取段的输入是 **user + assistant 完整对话**（助手的长表格/列表都在场），
而助手产出的就是结构化归纳——模型抄它比抄用户的口语更"顺手"。

**③ 结构上单调膨胀、新旧矛盾并列。** `mergeFacts` 只按**精确字符串**去重、且只增不减。结果
inline 的「职业规划」一条记忆挂了 **54 条 facts / 1910 字**：同一件事 5 种措辞并列，
「用户自述最想回西安 gap」「用户明确表示很想要这份 offer」互相打脸。
inline 平均 11.3 条（60 条超 10 条、9 条超 25 条），而 `memory_value` 始终是 98–148 字一句话。

**影响面**（决定优先级的依据）：`facts` **不进检索、不进向量、不进任何 prompt**——它唯一的消费者是
用户自己（原生客户端记忆详情页 `MemoryDetailOverlay.swift`；Vue 前端没读它）。
所以它的全部价值是"给人看"，看不准就是负资产。

## 2. 目标语义

| 字段 | 是什么 | 谁产生 |
|---|---|---|
| `topic` | 主题词 | topic_namer（确定性 + 一次 LLM 定稿） |
| `memory_value`（summary） | **系统的概括**，自足成句 | LLM |
| `facts` | **用户原话摘录**，逐字、可核验 | LLM 摘抄，服务端硬校验 |

一句话：**概括归 summary，出处归 facts。** 两者分工不重叠，facts 不再是"第二份 summary"。

## 3. 方案

### 3.1 prompt 改口径（三处）

- `recordLoopInstruction`（`agent/record_loop_types.go`，**逐字镜像**在 `record-loop-prompt.md` §2，由
  `TestRecordLoopInstructionMirrorsDoc` 钉住——口径以那两处为准，本节只记「改了什么」）：
  - `facts` 的定义：从「支持这条 summary 的具体细节」改成「**用户原话的逐字摘录**」，
    并限定只能从 `<candidate>` 给出的原话里原样摘取（循环看不到用户消息原文，基准只能是候选）。
  - `update` 段：从「只写新增的细节（与已有去重合并、只增不减）」改成「写**本轮候选带来的原话**、整体覆盖旧值」，
    见 §3.3。
- 抽取段（`agent/extractor.go` 的 `buildMemoryInstructionXML`）：rules 加一条逐字约束，
  OutputFormat 的示例改成原话形态，并点明 summary/facts 的分工。
- `promptbuilder.MemoryDefinition`（全链路唯一定义）+ `tool/record_memory.go` 的参数说明：同口径。

### 3.2 硬校验 `verifyVerbatimFacts`

```
verifyVerbatimFacts(facts, userContents []string) (kept []string, dropped int)
```

- 基准 = **该次抽取的用户消息原文**（助手的话不算来源）。
- 判据 = 每条 fact 是**某一条**用户消息的子串。
  - **按单条比、不拼接**：拼接会放过「两条消息各取半句接起来」的伪造。
  - **先去空白再比**：模型照抄长文本常改换行/空格，空白差异不构成「改写」；改字/改标点/换词仍会命中不了。
- 不中即丢，**只丢片段不丢候选**（summary 才是主内容，facts 允许为空）。
- 日志：抽取段 `[memory] facts 逐字校验：丢弃 N 条非用户原话片段`；inline `[record_loop] inline facts 逐字校验：丢弃 N 条`。
  **丢弃率就是 facts 的「扭曲率」指标**——这是这次改造顺带拿到的可观测性。

落点两处：

| 入口 | 基准来源 | 额外查询 |
|---|---|---|
| post_chat | 会话快照的 user 消息（`ExtractCandidates` 已拿到 `msgs`） | **零** |
| inline | `MsgRepo.ListRecentUserMessageContents(convID, 50)`（只 `Pluck("content")`，不拉 `xml_context`/`request_json`） | 一次 |

抽取段顺手把 facts 截到 `memoryFactsMaxCount`（工具侧上限）：超出的条数会被决策循环的工具打回，白烧一轮它的轮数预算。

inline 需要 convID，所以：`record_memory.Execute` 传 `ec.ConvID`，
`RecordDeps` 加 `MsgRepo`，appinit 装配补一行。

**取不到基准时丢弃全部 facts**（convID 空 / DB 报错）：facts 是可选字段，但
「存下来的 facts 一定是用户原话」是契约，查不了就不能放行。记日志，不向上抛。

### 3.3 update 改「覆盖」

`record_loop_write.go`：`facts := mergeFacts(parseFactsPtr(old.Facts), a.Facts)` → `facts := filterWrittenFacts(a.Facts)`。
删掉 `mergeFacts` 与 `parseFactsPtr`（后者随之无消费者）及其单测。

语义：facts 表示「这条记忆**当前**的出处」，不是「历史出处合集」。旧出处随 summary 被改写而作废。
**因此 `update_memory` 要求的是「本轮候选带来的原话」，而不是「当前完整出处」**——旧 facts 从不进
`<neighbors>` / `<topic_records>`（那两处只渲染 `MemoryValue`），模型看不到旧的就写不出「完整」，
要它写「完整」等于逼它编。这是与覆盖语义配套的措辞（review 的 P2-2）。

### 3.4 单条 fact 上限 100 → 200 字

用户一句话常超 100 字（例：「我本来的节奏是 11.25 离开北京，现在还没怎么做准备，从日本回来以后…」）。
卡太紧会让模型反复「超长被拒」，白烧决策循环的轮次预算。

### 3.5 抽取段温度 = 0

抽取要的是**逐字抄录**，采样温度越低越少改写。做法：

- 新增装饰器 `llm.WrapTemperature(inner, temp)`：`req.Temperature == nil` 时填默认值，
  显式传了的原样透传（`ChatWithRawJSON` 是原始 body，注入不了，透传）。
- 配置 `server.memory_extract_temperature`（`*float64`，**nil = 不干预**；0 是合法值，
  所以合并判据用 `!= nil` 而不是 `> 0`）。
- **只套在抽取段这一处注入点**（`appinit/tools.go`）。

为什么不给整个后台族（BgFlash）统一归零：后台客户端是共用的（抽取/决策循环/摘要/画像/主题/秘书
都用它），一刀切会改动一堆没验证过的链路。收窄到抽取段是够的——**抽取段是 facts 的产地**，
决策循环只是搬运。

**但「搬运」不是自动成立的**：决策循环是**第二次独立采样**，它有可能改写、拼接、或凭 summary
补一句。所以契约不能只靠 prompt，末端必须有收口——见 §3.6。温度只是第一道（让它少改写），
收口才是保证。

**已知坑**：`doubao.go` / `claude.go` 不读 `Temperature`（只有 `deepseek.go` / `ark.go` 写进 payload），
换 provider 会静默失效。当前 provider 是 deepseek。

### 3.6 写入侧收口（`filterWrittenFacts`）——这是真正的保证

`Decide` 把入口已校验过的候选原话存进 `recordLoopState.candFacts`；`saveMemory` / `updateMemory`
在落库前把模型写进来的每条 fact 过一遍**同样的逐字判据**（去空白后必须是某条候选原话的子串），
不中即丢 + 记 `[record_loop] <source> facts 写入校验：丢弃 N 条`。

为什么非有不可：入口的 `verifyVerbatimFacts` 校验的是**候选**，而真正落库的是决策循环那次采样的
tool call 参数。少了这道，「facts 一定是用户原话」在最末端就是空的（review 的 P1-1）。
纯内存、零额外查询；`facts` 为空时直接短路。

副作用：循环若把**两条候选原话合并/改写**成一条，会被判赝品丢掉——这正是要的行为，且有日志可观测。

## 4. 存量数据

**不动。** 存量 inline 137 + post_chat 183 条的 facts 都是错的，但历史对话不可靠地重建；
清了也回不来。只保证**新写入**干净。用户已确认此取舍。

## 5. 验收

- 单测：`verifyVerbatimFacts` 九种情形（命中/截断/改写/助手的话/跨消息拼接/空片段/空白归一化/混合保序/trim）；
  抽取段端到端丢非原话、留原话；**写入侧 `filterWrittenFacts`**（截断留 / 改写丢 / 候选无原话时一律写不进去）；
  **`verifyInlineFacts` 三分支**（命中丢弃 / convID 空 / 查库失败）；
  **`update` 覆盖在 DB 层的落地**（nil facts → 旧值被写成 NULL，`service/user_memory_repo_test.go`）；
  `WrapTemperature` 填默认值 / 不覆盖显式值 / 不跨调用别名；配置合并 0 能覆盖。
- 镜像：`TestRecordLoopInstructionMirrorsDoc` 同时钉 `recordLoopInstruction` 与 `MemoryDefinition` 两份。
- `go build ./... && go test ./...` 全绿。
- 真机 1：`make dev` 跑一次会话 → 看日志 `facts 逐字校验：丢弃 N 条`，且库里新记忆的 facts
  能在源会话的 user 消息里逐字搜到。
- 真机 2：抽取段的 LLM 请求里 `temperature=0`，而摘要/画像的请求仍是厂商默认
  （开 `debug_persist_json` 看 request，或看 `llm_call_ios`）。

## 6. 回滚

- 温度：删 `server.memory_extract_temperature` 那一行即回到现状。
- 逐字校验：`ExtractCandidates` / `RecordInline` / `filterWrittenFacts` 三处删调用即回到「不过滤」。
- 覆盖：改回 `mergeFacts` 需要恢复函数（git 可查）。

## 7. 风险

1. **post_chat 的 facts 会大面积变空或骤减**——现有 facts 大多是助手综合出来的，
   逐字校验必拦。facts 从「细节层」退化成「出处层」，一条记忆可能只剩 summary。**这是本方案的代价，不是副作用。**
2. **`update` 会清掉这条记忆原有的全部出处**——这是「覆盖」语义的直接推论，不是意外：
   哪怕只是改一个日期，旧 facts 也被整体替换成「本轮候选带来的那几条」。要「旧出处不丢」就得放弃覆盖
   （见 §3.3 的取舍）。用户已知情并选择了覆盖。
3. **模型抄不准就丢**——短期 drop 率不会低，可观测（那正是扭曲率）。
4. `<topic_records count>` 仍含 777 条存量记忆（`sameTopicRecords` 不带 source 过滤），
   模型看到的大数字主要来自旧管线。本轮不动，已挂账。
5. post_chat 的其它噪声（会话过程/行程细节/助手能力）不在本轮范围，见
   `docs/design/memory-quality/design.md` §6 与文末后续变更。
6. 存量 137 + 183 条脏 facts 不动，但客户端记忆详情页（`MemoryDetailOverlay.swift`）无差别展示，
   「facts = 用户原话」这个心智只对新数据成立，界面上没有区分。若将来造成困惑，单开一票。

## 后续变更

- **2026-10-16（review 收口）**：外部 review 提了 P1 一条、P2 四条、P3 五条，逐条核对**全部成立**，
  另自查出同族的第六条。已改：
  1. **（P1）落库这点不校验** → 新增 §3.6 `filterWrittenFacts`。原先的论证只在「循环逐字搬运」时成立，
     而循环是第二次独立采样；这是本票唯一的实质保证缺口。
  2. **（P2，用户拍板）** update 要求的「当前完整出处」模型看不到（旧 facts 从不进 prompt）→
     按 **覆盖语义** 把措辞改成「本轮候选带来的原话」，见 §3.3。
  3. **（P2）抽取段规则 7「相对时间转绝对日期」与规则 2「逐字」打脸** → 两条都限定为
     「只作用于 summary；facts 一个字都不许改」。不改的话「扭曲率」指标会混进自己造的丢弃率。
  4. **（自查，reviewer 没提）决策循环的工具定义是第三个事实来源，且与新 prompt 打架**：
     `record_loop_tools.go` 里 `save_memory.facts` 仍写「0~10 条细节，每条 ≤100 字」、
     `update_memory` 仍写「会自动与已有去重合并，只增不减」。工具描述是模型对参数语义的第一手说明，
     比 system prompt 的长文更容易被照办 → 三处改成同口径，数字改由 `memoryFactsMaxCount` /
     `memoryFactMaxLen` 插值，不再手写。
  5. **（P3）** 抽取段顺手截到 facts 上限；`model.UserMemory.Facts` 注释改口径；
     镜像测试把 `MemoryDefinition` 一起钉住（它此前漂过一次）；本文档三处与实现不一致（方法名/日志前缀/状态行）改正。
  6. **（P2）** 补测：写入侧过滤、`verifyInlineFacts` 三分支、`update` 覆盖在 DB 层落地（nil → NULL）。
  7. **（P3）** 温度不加取值范围校验（与既有 `default_temperature` 风格一致）；doubao/claude 静默失效已在 §3.5 记录。
- **2026-10-16（第三轮 review：四条 P3）**：① §3.1 里 update 的措辞还是旧版（同一文档前后打架）→
  改成**不复述 prompt 原文**、只说「改了什么」并指向被测试钉住的镜像，这类漂移从此不会再发生。
  ② `verifyVerbatimFacts` 的契约注释只描述了「用户消息」一种基准 → 参数改名 `sources`，
  写明两个调用方两种含义（用户消息原文 / 已校验的候选原话）。③ `verifyInlineFacts` 的测试用魔法数
  `Status: 2` → 改 `model.MsgStatusDeleted`，并补 `MsgStatusHidden(3)`（隐藏控制轮同样不该当基准，且更常见）。
  ④ 指令散文里的 5/150/200/10 无法插值（const 插不进），与常量联动只能靠人 → 在 const 注释里写明
  「改常量请同步散文」，不上机制（工具描述那侧已是插值）。
