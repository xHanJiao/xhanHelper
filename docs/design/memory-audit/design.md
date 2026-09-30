# 记忆命中计数重定义 + 资源区配额调整 — 设计文档

> 版本：v1｜日期：2026-09-29｜状态：设计定稿，待开发
>
> 缘起：记忆系统上线后的回收验证，见 §1 的实测数据
> 参照：`docs/design/memory-note-rework/design.md`（记忆/笔记改造）、`docs/design/phase-10/memory-retrieval-enhancement.md`（资源区）、`docs/design/memory-system-improvements.md`（改进方向）
> 估算（开工）：3 面 / 2 缝 / 2 次真机往返 / 4 个待拍决定 · ~5–9h
> 估算（定稿）：4 面 / 3 缝 / 2 次真机往返 / 0 个待拍决定 · ~4–8h

---

## 一、人话版：这件事到底是什么

### 1. 缘起：回头看了一眼记忆系统跑得怎么样

记忆系统（`user_memories` 上那套抽取 + 向量检索 + 资源区注入）上线一段时间了。我们拿
最新一份本地 prod 备份（`xhanHelper-prod-20260929-205513.tar.gz`）做了只读回收，结论先摆在这：

- **新写入链路（`inline` / `post_chat`）是 2026-09-24 上线的**：那天起 `memory_extract`
  调用量从每天 ~5 次跳到 90+ 次，同日 `notes` 彻底停止新增。
- **记忆产量涨了 ~2.8 倍**（日均 6.5 → 18.5 条），**笔记归零**（日均 7.9 → 0）。
- 但把「记忆 + 笔记」合起来看，**每条用户消息产出的捕获量几乎没变**（0.256 → 0.239）。
  也就是说，这次上线更像是一次**形态切换**（显式笔记 → 自动记忆），而不是知识捕获总量
  的提升。文档没受影响。

回收里暴露了两个问题，这就是本文要修的：

1. **`confirm_count` 这个「记忆被用了多少次」的计数器是坏的**——它记的是「候选排过名」，
   不是「真的发给了模型」，而且全系统没人读它（详见 §2.1、§2.2）。
2. **资源区配额还停在「笔记为主」的旧假设上**——`quota_note=5 / quota_memory=3`，
   可现在笔记基本不写了、记忆成了主角（详见 §2.4）。

### 2. 为什么 `confirm_count` 靠不住

一句话：**它记的是「有几次排名靠前」，不是「有几次真的给了模型」。**

翻代码看到，这个数被三处写，而且语义还不一样：

| 谁写 | 在哪 | 记的是什么 |
|---|---|---|
| 检索 | `agent/retrieval/memory_source.go:27` 的 `AfterFetch` → `BatchUpdateConfirm` | 记忆进入了检索候选 |
| 提取决策 | `agent/record_loop.go:158` 的 `confirmTopNeighbor` | 候选被判为「无新内容」，对最高分近邻「再次确认」 |
| 新建 | `agent/record_loop_persist.go:30` 置 1 | 初值 |

坑有两层：

- **记早了**。`AfterFetch` 是在 `entity_source.go:166` 触发的，而**新鲜度阈值过滤在 170、
  条数裁剪在 201** ——也就是说，一条记忆被检索成候选、随后又被阈值刷掉或被 `limit` 裁掉，
  它照样 +1。这正好把「没进上下文」的也算成了命中。
- **语义混**。「被检索排到前面」和「被重申」被塞进同一个数里，分不开。

更尴尬的是：**全仓库没有任何读取方消费它**（`rg` 只有测试引用），客户端卡片也不显示。
所以它现在是「只写不用」，既不准、也没在影响任何东西。

### 3. 我们打算怎么改

- 把 `confirm_count` 的意思改成一句干净的话：**「这条记忆被当轮新召回、并且真的进了
  发给模型的最终上下文」的次数**。历史脏值全部清零，从新链路重新开始累计。
- 把资源区配额从「偏笔记」调成「偏记忆」——记忆条短（p75 ≈ 62 字）、笔记条长
  （p75 ≈ 272 字），这么调是**净省 token** 的。

---

## 二、技术方案

### 2.1 `confirm_count` 的新语义（口径 B：命中并注入）

**定义**：`confirm_count` = 一条记忆**被当轮新召回（fresh）、且成功进入本轮最终注入上下文
（资源区 budget 之后仍在）**的次数。

> **口径选择留档**：还讨论过另一个口径 A——「最终资源区里所有记忆，每轮 +1」（含跨轮携带）。
> A 的语义是「这条记忆在模型上下文里待了几轮」，好处是能用离线备份解析 `xml_context` 的
> `<memories>` 完全对账；但它把资源区的**跨轮携带**也算进去，一条记忆只要没被 `quota_memory`
> 从头部挤出，就会一直累积，衡量的是「在场时长」而非「命中」。
>
> 这里选 **B**，因为我们要的是「这条记忆到底被检索到、被用上了没有」——`confirm_count` 原本
> 想干的就是这件事。代价是 **B 无法用存量离线数据对账**（`xml_context` 里不标记哪条是本轮新
> 召回），只能依靠新的计数本身 + 单测。这是明确接受的取舍。

（事实核对：资源区确实是**逐轮滚动携带**的——`chat.go:678-686` 把上一条 user 消息的整个
`XMLContext` 作为 `PrevUserXML`，`orchestrator.go:775-779` 与本轮 fresh 合并；间隔超过
`session_gap_minutes`（30 分钟）开新 session 时重置，见 `chat.go:667-670`。所以携带是真实存在的。）

### 2.2 写入路径改造

**① 删掉误计源**（关键修复）

- `agent/retrieval/memory_source.go:27-29`：移除 `AfterFetch`（它是在过滤前计数的元凶）。
- 顺带清理已无使用者的字段与调用块：`EntityConfig.AfterFetch`（`entity_source.go:29,79`）
  及 `entity_source.go:166-168` 的触发块；并删掉 `note_source.go:27`、`summary_source.go:31`
  里显式的 `AfterFetch: nil`。（核对过：`AfterFetch` 只有 memory 源非空。）

**② 新写入点：在「确定最终注入集」之后计数**

- `agent/orchestrator.go`：`Orchestrator` 新增 `memRepo *service.UserMemoryRepo`
  （struct 见 `orchestrator.go:153`），`OrchestratorConfig` 新增 `MemRepo`（`:209`），
  在 appinit 装配处注入。
- `buildUserMessage` 里，`BudgetResourceZone(...)`（`orchestrator.go:780`）之后，收集最终
  `zonePkts` 中 `Type == memory` 的 ID，与本轮 `zoneFresh` 中 `Type == memory` 的 ID 求交集
  → 去重 → 异步 `memRepo.BatchUpdateConfirm(context.Background(), ids)`。
- 非资源区分支（`resourceZoneEnabled == false`）从 `packets[TypeMemory]` 收集，保证两条路径都覆盖。
- 错误处理遵循项目规范：异步 goroutine 内失败 `log.Printf` 降级，不打断主流程（等同原
  `AfterFetch` 的处理方式，但不静默）。

**③ 删掉「重申」的计数**

- `agent/record_loop.go:158`：移除 `UpdateConfirm(ctx, best.ID)` 调用；**保留** `out.Action = "confirm"`
  的决策返回（主 LLM 仍需要知道「这条没新增、是重申」）。
- `service/user_memory_repo.go`：若单数版 `UpdateConfirm` 无其他调用者则删除；`BatchUpdateConfirm`
  保留（新写入点使用）。

**④ 模型字段**

- `model/user_memory.go:28`：`ConfirmCount` 的 gorm default `1` → `0`；注释改为
  「被当轮新召回且注入最终上下文的次数」。
- 移除新建记忆时的初值：`agent/record_loop_persist.go` 不再置 `ConfirmCount=1` /
  `LastConfirmedAt=now`（新记忆从 0 起，只有真正被注入才 +1 并盖 `last_confirmed_at`）；
  随之删掉已无使用者的 `timeNow()`（`record_loop_params.go`）。
- **保留列名 `confirm_count` 与 `int` 类型**：因为采用「置 0」而非「置 NULL」，不需要把字段
  改成指针，也不牵动 API / 客户端。改名 `hit_count` 需要额外迁移，本次不做（留作后续）。

### 2.3 历史脏值清零（只清本地 prod）

- 新增 `cmd/memory-reset-confirm`（仿 `cmd/topic-clean`）：默认 **dry-run**（打印将影响的条数），
  加 `-apply` 才执行 `UPDATE user_memories SET confirm_count = 0`。
- 只针对**本地 prod SQLite**（`~/Library/Application Support/xhanHelper/xhan_helper.db`）。
  云端 MySQL 本次不清（如需另跑）。`last_confirmed_at` 不动（避免指针化）。
- 迁移必须走 dry-run 先行，确认条数后再 `-apply`。

### 2.4 资源区配额调整（保守组，`conf/config.yaml`）

| 配置 | 现值 | 新值 | 理由 |
|---|---|---|---|
| `quota_memory` | 3 | **6** | 记忆已是主角，容量翻倍 |
| `budget_memory` | 300 | **600** | memory p75 ≈ 62 字，容纳 6 条 + XML 开销 |
| `quota_note` | 5 | **3** | 笔记基本停写，只留最相关的 |
| `budget_note` | 1000 | **700** | note p75 ≈ 272 字，约 2 条 |
| `budget_chars` | 4500 | 4500（不变） | per-type 之和仅 3600，有余量；本次不需要动总帽 |

- 只改 `conf/config.yaml`（base）。`config.dev/prod/local.yaml` 未覆盖 `resource_zone`，无需动。
- 代码里的 `DefaultZoneQuotas` / `DefaultZoneBudgets`（`resource_zone.go:32,48`）**不改**——它们只在
  配置缺省时兜底，改了会让「配置值」和「代码默认」更难解释。单测因此基本不受影响。
- 硬约束：`quota_memory` 的实际上限受 `retrieval.memory.limit = 8` 约束，设 >8 无意义。

### 2.5 测试

- **新增 `agent` 层单测**（钉住口径 B）：
  - fresh 记忆命中但被 `quota` 或 `budget` 裁掉 → **不** +1；
  - fresh 记忆进入最终资源区 → +1；
  - 仅由 prev 携带（非本轮 fresh）的记忆 → **不** +1。
- 复核既有测试是否受影响：`resource_zone` 相关单测用的是代码 `Default*`，本次不改默认值，
  预计无碍；`memory_source_test.go` 若依赖 `AfterFetch` 计数需同步调整。

---

## 三、影响面

- **检索层**：`memory_source.go` 不再有副作用；`EntitySource` 少一个 hook。
- **编排层**：`Orchestrator` 新增依赖 `MemRepo`（appinit 装配）。
- **决策循环**：`record_loop.go` 少一次 DB 写；返回给主 LLM 的 `action` 不变。
- **客户端**：`UserMemory.confirmCount` 字段保留（当前未展示），值会从旧值变为 0 起重新累计，无 UI 影响。
- **配置**：资源区各类型条数/预算变化，会改变每轮注入内容与 token 量（保守组净 token 基本持平）。

## 四、验证

1. `cd server && go build ./... && go test ./...` 全绿。
2. `cmd/memory-reset-confirm` 先 dry-run 看条数 → 确认后 `-apply`（只本地 prod）。
3. 本地 prod 跑一轮聊天（**注意：这是真实数据，写 `confirm_count` 属预期功能行为**），
   核对：只有「本轮新召回且出现在该轮 `xml_context` 的 `<memories>` 里」的 id 被 +1，
   被裁掉的、仅携带的都没动。
4. 分支：改动落 `main`。

## 五、明确不做（本次）

- **不改列名** `confirm_count` → `hit_count`（需额外迁移，后续再说）。
- **不清云端 MySQL** 的 `confirm_count`（本次只清本地 prod）。
- **不动时间戳混存问题**——回收时发现 `created_at` 有 `+00:00`(UTC，旧 MySQL 写入) 与
  `+08:00`(本地 SQLite 写入) 混存，导致按文本比较/按天聚合在边界最多差 8 小时
  （实测 `'2026-09-23 20:00:00+00:00' >= '2026-09-24 00:00:00+08:00'` 返回假，但它真实是
  09-24 04:00 北京）。这是独立问题，另开一票（根治方向：MySQL DSN 加 `loc`，或一次性把旧行
  归一）。
- **不做「被引用率」**（LLM 判回答是否用到某条记忆）——成本高、判读不稳，本次不碰。
- **不做 App 记忆卡片 👍👎 打分**——那是下一步单独需求（后端加评分列 + 接口、客户端卡片按钮，
  SF Symbols `hand.thumbsup` / `hand.thumbsdown`）。

## 六、风险与回滚

- **本地 prod 是真实数据**：迁移只改一个计数列，且已确认；仍以 dry-run 先行，异常可再置 0。
- **配额调整**改变注入内容：如观感变差，回退 `conf/config.yaml` 四行即可（无 DB 影响）。
- **取舍已声明**：口径 B 无法离线对账；若日后想要可对账的「在场轮次」，可另加一个
  `inject_count`（口径 A）而无需推翻本次改动。

---

## 附：本次回收验证的原始数据（2026-09-29 备份，只读）

**上线时间点**：2026-09-24（`inline` 首条 09-24 03:11、`post_chat` 首条 03:28；`memory_extract`
调用量跃升；`notes` 同日停写）。

**近 7 天（09-22 ~ 09-29）记忆提取数量**

| 日期 | inline | post_chat | 小计 |
|---|---|---|---|
| 09-22 | 0 | 0 | 0（旧 `conversation` 残留 4）|
| 09-24 | 4 | 10 | 14 |
| 09-25 | 24 | 6 | 30 |
| 09-26 | 23 | 9 | 32 |
| 09-27 | 11 | 7 | 18 |
| 09-28 | 6 | 8 | 14 |
| 09-29 | 0 | 3 | 3（未过完）|
| **合计** | **68** | **43** | **115** |

**上线前后对比**（before 08-25~09-23 / after 09-24~09-29）

| 指标 | 上线前(30天) | 上线后(6天) |
|---|---|---|
| 记忆 | 195（6.5/天）| 111（18.5/天）|
| 笔记 | 236（7.9/天）| 0 |
| 文档 | 124（4.1/天）| 35（5.8/天）|
| 记忆+笔记 / 对话 | 0.984 | 1.028 |
| 记忆+笔记 / 用户消息 | 0.256 | 0.239 |

**成本**（09-24~29，`llm_cost_details`）：`memory_extract` 522 次 / 1.80M token（缓存命中 71%），
`record_memory`（决策循环）426 次 / 1.86M token；记忆流水线合计 ~3.7M token ≈ 主聊天 `re_act_loop`
（62M）的 6%，摊到 111 条新记忆 ≈ 8.5 次 LLM 调用/条。

**读数陷阱（存档）**：`confirm_count` 不可当命中率（本文主题）；`created_at` 时区混存导致按天
聚合边界最多 ±8h；after 窗口仅 6 天且含上线首日疑似补跑，趋势不代表稳态。
