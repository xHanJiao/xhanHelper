# 对标 opencode：上下文 / Token 治理（prune / compaction / 子任务隔离）

> 范围：调研 opencode 如何压低上下文与 token，对照 xhanHelper 现状，并用真实数据判定
> 「prune」这条我们缺的机制到底值不值得做。
> 数据源：dev MySQL `xhan_helper_test`，2026-08-25 ~ 2026-09-22。查询口径见附录。

---

## 0. 结论摘要（TL;DR）

- opencode 靠**三道闸**压上下文：① 单次工具输出上限（超大落盘）；② **prune**（旧工具结果就地抹成占位符）；③ 溢出 **compaction**（LLM 摘要替换历史）。①② 自动、每个 step 后跑。
- 我方现状：**没有 prune，也没有就地 compaction**；等价机制是**子任务隔离**——文件/网页原始数据在子会话里，主对话只收结构化结论。
- **实测判定：prune 对我方是「二阶补充」，不是关键差异。**
  - opencode 默认 prune 阈值（保留最新 40k token、超出 >20k token 才清）套到我们身上 **0 命中**（主对话 0/605、子会话 0/436 请求达标）。
  - 走 prune 的收益主要在**延迟与上下文余量**；金额可忽略（即使压低阈值，约 **$0.3/月**）。
  - 我们主要靠「隔离（根本不让进）」而非「老化（进来再淡出）」拿到了收益；**隔离更彻底**。
- 若未来真要做，形态应为：**重放投影 + 保护 `task__run` + 阈值压到 ~40k chars + 优先子会话**（见 §6）。

---

## 1. opencode 怎么做（人话版）

### 1.1 prune：工具结果的「老化清空」

源码：`packages/opencode/src/session/compaction.ts:271-317`，调用点 `packages/opencode/src/session/prompt.ts:1338`。

**一句话：不是删消息，是把「已完成的工具结果内容」就地抹成一个占位符；原始数据仍在会话存储里，UI 照常可见，只是不再发给模型。**

常量（`compaction.ts:28-33`）：

| 常量 | 值 | 含义 |
|---|---|---|
| `PRUNE_PROTECT` | 40_000 token | 保留最新这么多 token 的工具输出 |
| `PRUNE_MINIMUM` | 20_000 token | 待清批量不到这个量就不动手（防抖） |
| `PRUNE_PROTECTED_TOOLS` | `["skill"]` | 技能产物永不清理 |
| `TOOL_OUTPUT_MAX_CHARS` | 2_000 | 仅 compaction 摘要输入的单条截断，不是 prune |

判定规则（从最新往旧扫，`compaction.ts:288-305`）：

1. **保护最近 2 个 user 轮**：`turns < 2 → continue`（刚做的操作不能失忆）。
2. **`skill` 工具产物跳过**。
3. 累积所有已完成工具输出的 token 估算，累计 ≤ 40k 保留。
4. 超出 40k 的部分记为待清。
5. **候选总量 > 20k 才真清**。
6. 遇到上一次 compaction 摘要边界 → `break`（摘要之前归摘要管）。
7. 遇到已清空的 part → `break loop`（只清「40k 边界往外的一整段」，不回头零敲碎打 → 保证被清区域稳定、不再反复破坏缓存）。

清理动作：`part.state.time.compacted = Date.now()` 并持久化；组装请求时替换（`message-v2.ts:293-296`）：

```
part.state.time.compacted ? "[Old tool result content cleared]" : 原内容
```

被清结果的图片附件也一并不发（`stripMedia`）。开关：默认**开**，`OPENCODE_DISABLE_PRUNE=1` 或 `compaction.prune=false` 关闭（`config.ts:596-597`）。

**它不看单条长度**：不管 200 字还是 200KB，只要落在 40k 保护线之外就被清。因此它与「按大小截断」正交。

### 1.2 compaction：溢出时的 LLM 摘要

同一文件 `compaction.ts:319+` 与 `overflow.ts`：超上下文就调 LLM 出结构化摘要（Objective / Details / Work State / Next Move / Relevant Files），保留 recent tail（默认 8k、2k–15k）。触发有自动（每步后判溢出）与手动 `/compact`。**重武器**：有损、要 LLM 调用。

### 1.3 单次输出上限

`read` 等工具自身有 `MAX_LINES / MAX_BYTES` 上限，超限落盘并给提示。这是**源头**限制，与 prune（历史维度）无关。

### 1.4 分工

| 机制 | 成本 | 时机 | 有损性 |
|---|---|---|---|
| 单次输出上限 | 无 | 工具执行时 | 单条截断 |
| **prune** | 极低（无 LLM） | 每 step 后自动 | 旧工具结果**整体清空**（原文留存） |
| compaction | 高（LLM 读+摘要） | 溢出 / 手动 | 历史被摘要替换 |

---

## 2. 我方现状

### 2.1 历史重放：整轮无差别重放

`internal/controller/chat.go:262-308`：每轮按 `computeWindowSize` 取最近 N 轮，`ListRecentRounds`
（`internal/service/message_repo.go:426`）→ `buildRoundsFromUserMsgs`（`:470`）**逐轮无 Limit** 拉取，
注释明写「巨型 ReAct 轮可达 150+ 条」。这些消息经 `FlattenRound` 全部重放。

### 2.2 唯一护栏：按大小截断（只在重放）

- `internal/agent/orchestrator.go:2024` `BuildMessagesForFlash`：工具结果按
  `max_tool_result_chars`（`conf/config.yaml:34` = **20000**，head+tail 截断）截断。
- `internal/agent/orchestrator.go:1683-1700` `collectToolResultsNative`：**当前轮 ReAct 内工具结果原样入上下文，不截**。
- 客户端工具另有注入预算 `clientToolInjectBudgetBytes = 20000`（`internal/agent/client_tools.go:48`）。

### 2.3 窗口如何退出

`computeWindowSize`（`chat.go:650-688`）：窗口大小存最后一条 user 消息上，从
`history_rounds=10` 每轮 +1；到 `max_window_rounds=800` **腰斩到 400**；超过
`session_gap_minutes=15` 重置为 1、不重放历史。

→ 一条旧工具结果离开上下文只有三条路：**被腰斩 / 开新 session / 超 20k 被截**。**没有「随时间淡出」机制。**

### 2.4 明确：无 prune

`rg -in "prune|compacted|cleared" internal/` 未命中任何工具结果治理逻辑。

---

## 3. 数据实测

口径：解析 `messages.request_json` 里**实际发给模型**的 messages 数组（`role='assistant'` 持有该请求），
按 `role` 汇总 `content` 字符数；`chars / 4 ≈ token`（同 opencode `Token.estimate`）。范围 2026-08-25 起。

### 3.1 工具结果是 messages payload 的绝对主体

| | 请求数 | 工具结果 chars | messages 总 chars | 工具占比 | 单请求平均 |
|---|---|---|---|---|---|
| 主对话（`parent_id IS NULL`） | 605 | 33.0M | 39.4M | **83.8%** | 54.5k |
| 子 agent（`parent_id NOT NULL`） | 436 | 21.5M | 23.2M | **92.5%** | 49.2k |

主对话角色构成：tool 33.0M / system 2.7M / assistant 2.3M / user 1.4M。
（注：`tools` schema 作为独立 API 参数，不在此统计内。）

### 3.2 单请求工具结果分布（主对话）

| p50 | p75 | p90 | p99 | max | avg |
|---|---|---|---|---|---|
| 29,700 | 73,601 | 177,329 | 235,264 | 236,727 | 54,538 |

### 3.3 opencode 默认阈值下的触发率：0

opencode 触发条件换算（保留 40k token = 160k chars，需超出 >20k token = 80k chars）：
**一次上下文里累计工具结果 > 240k chars** 才会动手。

| | 请求数 | >160k chars | >240k（会触发） | >80k |
|---|---|---|---|---|
| 主对话 | 605 | 70 | **0** | 142 |
| 子 agent | 436 | 12 | **0** | 124 |

→ **默认阈值对我们永不触发。** 根因见 §4。

### 3.4 压低保留线的可清量（主对话 33M / 子会话 21.5M）

| 保留线 | 主对话可清 | 子会话可清 |
|---|---|---|
| 80k chars（~20k tok） | 10.4M（31%） | 4.9M |
| 40k chars（~10k tok） | 18.6M（56%） | 11.4M |
| 20k chars（~5k tok） | 24.7M（75%） | 15.9M |

**钱账**：按 40k 线全清，主对话 ~18.6M chars ≈ 4.65M token/月；DeepSeek hit 价 $0.07/M
（phase-10 口径）→ **约 $0.3/月**。故收益只能定位在延迟与余量。

### 3.5 谁贡献的

主对话（2026-09-15 起，`task` 上线后）——**已无 fs / browser**：

| 工具 | chars |
|---|---|
| `task__run`（子任务结论） | 1.14M |
| `document__get_document` | 0.62M |
| `document__query_documents` | 0.24M |
| `note__semantic_search` / `note__query_notes` | 0.19M / 0.18M |
| `document__list_documents` | 0.16M |
| `message_search__search_by_time` | 0.12M |
| `unified_search__keyword_search` / `semantic_search` | 0.08M / 0.05M |

子会话（2026-08-25 起）——**fs 系是绝对大头**：

| 工具 | 调用 | chars | 平均 |
|---|---|---|---|
| `fs__read` | 2149 | 10.44M | 4,857 |
| `fs__grep` | 1326 | 4.91M | 3,706 |
| `fs__glob` | 498 | 1.24M | 2,481 |
| `browser__agent_browser_read` | 438 | 0.40M | 914 |
| `browser__agent_browser_open` | 691 | 0.20M | 285 |
| 其余（search / shell / profile） | — | <0.2M | — |

---

## 4. 对比结论：隔离 vs prune

**子任务化是「上游隔离」，prune 是「下游老化」——两者解决同一问题，但隔离更彻底。**

| 机制 | 位置 | 作用 | 我方 |
|---|---|---|---|
| 子任务化 | 进主上下文**之前** | 原始大块数据（文件/网页）→ 结论；20KB×N 永不入主循环 | ✅ 已做 |
| prune | 进入之后**随时间** | 已进入的旧工具结果过一段时间抹掉 | ❌ 未做 |

子会话里的 `fs__read + fs__grep + fs__glob ≈ 16.6M chars` **只存在于子会话**，
主对话只收到 `task__run` 结论（1.14M）。**这正是 prune 在主循环里本该收到的量，已被隔离提前吃掉。**

**为什么 opencode 需要 prune 而我们不太需要**：opencode 主循环**直接读大文件**（单次可达 2000 行 / 50KB），
所以它必须靠 prune 把历史里堆积的大块清掉；我们选了子任务边界，从源头挡住了。**「opencode 靠 prune 省 token」不能直接平移。**

**不完全等价，残余缺口**（prune 仍可能有意义的地方）：

1. 主对话里**已经进来的**东西：`task__run` 结论、`document__get_document` 窗口、检索结果——整个窗口存活期一直挂着。
2. **子会话内部**隔离帮不上忙：fs 读的 16.6M 全在子会话，命中率也低（子 agent 45–73% vs 主对话 80–88% → 更多 miss 计费）。但子会话通常一次跑完、短命，优先级低。

**保护名单**：opencode 用 `PRUNE_PROTECTED_TOOLS=["skill"]` 保护技能指令。我们的对应物应是
**保护 `task__run`**——主 agent 之所以派子任务，正是因为手里没有原始数据；清掉结论等于逼它重跑。

---

## 5. 影响评估

### 做了 prune 的收益
1. 请求体积/延迟下降（重 agentic 会话不再每轮携带几十万字符旧结果）。
2. 给长会话留上下文余量、避免撞墙（当前 1M 尚远，属预防性）。

### 代价 / 风险
1. **钱不一定省，甚至可能亏**：DeepSeek 91% 命中，被抹的是 1/4 价（hit）token；抹除让清理点之后整段位移 → 该次请求多付一次 miss（≈ hit 的 4 倍价）。与 `phase-10` §5 对 compaction 的推演同构（ROI 仅 ~0.45–0.55）。prune 比 compaction 便宜（无 LLM 读+摘要），但断前缀那笔账仍在。
2. **语义有损**：被清的原文模型看不到，后续引用只能重查或答不上。
3. **框架约束**：原生 FC 下 tool 消息与 `assistant.tool_calls` 配对（`sanitizeOrphanToolMsgs` / `sanitizeOrphanToolCalls`，`orchestrator.go:2100/2136`），**绝不能删消息，只能替换 Content**。
4. **和 `max_tool_result_chars` 是同一维度两种粒度**（按大小截断 vs 按年龄清空），最终应合成一个「工具结果生命周期策略」。

---

## 6. 建议

**暂不做。** 理由：默认阈值 0 命中；金额收益可忽略；主要收益（大块数据不进主循环）已由子任务隔离拿到。

**若未来要做**（长会话上下文/延迟成为问题时），落地形态：

1. **重放投影**，不动 DB：在 `BuildMessagesForFlash`（唯一历史重放入口，`chat.go:308` 调用）内
   只替换 Content，绝不删消息——与资源区 B1「简化重放」同一套路。
2. **保护名单**：至少保护 `task__run`（子任务结论）；可考虑保护 `document__get_document`（用户有意读的）。
3. **阈值**：字符口径（避免 tokenizer），压到 ~40k chars 保留线（opencode 的 160k 对我们过高）。
4. **优先级**：先子会话（ROI 更高、命中率更低），后主对话。
5. 与 `max_tool_result_chars` 合并考虑，避免两套参数打架。

---

## 附录

### A. 复现查询（只读）

数据源：`server/conf/config.dev.yaml`（dev MySQL `xhan_helper_test`，host 127.0.0.1:3306，user `xhan`）。

工具结果占比与分布（主对话版，子会话把 `c.parent_id is null` 改成 `is not null`）：

```sql
with t as (
  select m.id,
    sum(case when j.role='tool' then char_length(coalesce(j.content,'')) else 0 end) tool_chars,
    sum(char_length(coalesce(j.content,''))) total_chars
  from messages m
  join conversations c on c.id=m.conversation_id and c.parent_id is null
  join json_table(m.request_json, '$.messages[*]'
    columns(role varchar(20) path '$.role', content longtext path '$.content')) j
  where m.request_json is not null and m.created_at>='2026-08-25' and m.role='assistant'
  group by m.id
)
select count(*) reqs, round(avg(tool_chars)) avg_tool,
       round(100*sum(tool_chars)/sum(total_chars),1) tool_pct
from t;
```

按工具名归集（需 join 回 assistant 的 `tool_calls_json`）：

```sql
select jt.name, count(*) n, round(sum(char_length(t.content))/1e6,2) chars_M
from messages t
join conversations c on c.id=t.conversation_id and c.parent_id is null
join messages a on a.conversation_id=t.conversation_id
     and a.role='assistant' and a.tool_calls_json is not null
join json_table(a.tool_calls_json, '$[*]'
  columns(id varchar(64) path '$.id', name varchar(60) path '$.function.name')) jt
  on jt.id = t.tool_call_id
where t.role='tool' and t.created_at>='2026-09-15'
group by jt.name order by chars_M desc;
```

### B. 口径说明

- 统计的是 `messages.request_json` 中 messages 数组的 `content` 字符数：assistant 的 `tool_calls` 参数、
  以及作为独立 API 参数的 `tools` schema **均未计入**（故实际 prompt 比此更大）。
- `llm_call_ios` 只审计 `ChatSync`（post_chat），**不含流式主对话**，故本次改用 `messages.request_json`。
- 字符→token 用 `chars / 4`（对齐 opencode `packages/core/src/util/token.ts`）。

### C. 代码引用

opencode（`../opencode-dev`）：
- prune 实现 `packages/opencode/src/session/compaction.ts:271-317`；常量 `:28-33`
- prune 调用 `packages/opencode/src/session/prompt.ts:1338`
- 占位符渲染 `packages/opencode/src/session/message-v2.ts:293-296`
- 开关 `packages/opencode/src/config/config.ts:596-597`

xhanHelper：
- `internal/agent/orchestrator.go:2024`（历史重放 + 20k 截断）、`:1683`（当前轮不截）、`:2100/2136`（孤儿 tool 清洗）
- `internal/controller/chat.go:262-308`（重放入口）、`:650-688`（窗口与腰斩）
- `internal/service/message_repo.go:426/470`（整轮无 Limit 拉取）
- `conf/config.yaml:34/76/79/80`
- 相关既有文档：`docs/design/phase-10-token-optimization.md`（token 治理主文档）
