# 用户画像 v2 · 设计

> 日期：2026-11-20 | 状态：**P0 + P1 + P2 已实现**（目标状态机只评估未建表，见 §9）
> 上级文档：`docs/design/facts-system/`（事实系统，profile 的 storage 部分沿用它）
> 人话版：[人话版.md](./人话版.md)（审阅用，不含实现细节）
> 估算（开工）：15 面 / 2 缝（0 条需先建探针）/ 3 次真机往返 / 4 个待拍决定 · 9–16h
> 估算（定稿）：见 §5 分期，P0 4–6h（含字段搬家）/ P1 5–8h / P2 2–4h；老数据重建另计 2–4h（+ 人工审 diff 0.5–1h）

## 0. 一句话结论

画像现在把**身份**（永不改）、**偏好**（慢慢改）、**近况**（天天改）塞进同一个"整字段替换"的槽里，且没有时间、没有证据、没有预算保护、用户看不到也改不了。v2 做四件事：**按变化速度分层、给每条事实加时间和证据、修注入预算的丢弃顺序、把画像开放给用户改**。

---

## 1. 现状（已逐条核对代码）

| 环节 | 实际实现 | 位置 |
|---|---|---|
| 存储 | `user_profile_facts`：`field / value / status(active·outdated) / batch_id / created_at` | `model/user_profile_fact.go` |
| 更新语义 | **整字段替换**：LLM 每次输出该字段完整列表，没提到的即判过时；旧行标 outdated | `agent/profile_worker.go:378` |
| 抽取输入 | **只有本 session 的 user 消息** + 现有 active facts。不看 assistant、不看事件、不看历史 | `agent/profile_worker.go:590` |
| 抽取时机 | session_end（post-chat 组），与 event worker 并行独立跑 | `appinit/agents.go:163` |
| 注入 | profile + summary 进 **system prompt 稳定上下文**，每轮全量注入 | `agent/orchestrator.go:788` |
| 预算 | profile **1600 字**（per-type），profile+summary 总帽 2100 | `config/config.go:902` |
| 超限策略 | **按字段整块丢弃**：`list = list[1:]` 从头部砍 | `agent/ctxflow/resource_zone.go:494` |
| 字段顺序 | 按字段名 UTF-8 码位升序 | `agent/orchestrator.go:1005` |
| 配额 | 单条 30–80 字 / 字段 1–10 条；违规回灌重生成 2 次，之后截断保留前 N 条 | `profile_worker.go:347` |
| 事实时间 | 无 `observed_at`；`created_at` 在每次整字段重写时被刷新（保留的旧事实也重插） | `service/user_profile_fact_repo.go:37` |
| 有效期 | 无。`outdated` 只有状态没有时间，看不出某事何时结束 | — |
| 证据 | 无计数、无来源、无置信度 | — |
| 用户可见性 | **无任何 API、无任何 UI**（`/api/facts` 仅存在于 `facts-panel` 设计文档，未实现） | 实测 grep 无路由 |
| 人物 | 无实体表，纯字符串塞在「重要关系」：10 条 × 80 字 | `profilefield/profilefield.go:94` |
| facts 系统（`Fact` 表） | 未接线（无 FactWorker），profile facts 是唯一在跑的写入通道 | `appinit/agents.go` |
| 事件系统 | 已存在：`Event{Name, Summary, LastMemoryAt}`，无状态位 | `model/event.go` |

### 1.1 已确认的缺陷（P0 必须先修）

**注入按字段名码位排序 + 超预算从头部砍 = 保留最吵的三块、扔掉全部身份信息。**

字段名按码位升序：`出生日期(51FA) · 性别(6027) · 称呼(79F0) · 籍贯(7C4D) · 职业领域(804C) · 补充说明(8865) · 财务概况(8D22) · 重要关系(91CD) · 长期目标(957F) · 饮食习惯(996E)`。1600 字预算下 `list[1:]` 会先砍头部，于是**码位最大的三个字段永远最后被砍**——这与线上实拍的注入结果（只显示重要关系 / 长期目标 / 饮食习惯，约 1450 字 + 标签 ≈ 1600）完全吻合。

即：模型当前很可能完全看不到 称呼 / 性别 / 出生日期 / 籍贯 / 职业领域 / 财务概况 / 补充说明。

### 1.2 由此产生的三个行为问题

1. **事件被写成属性**：`11-13青岛鲜啤`、`喝了打200块`、`10-17东京` 这类带日期/金额/地点的条目挤在「饮食习惯」里 —— 字段是 list + "原样保留旧事实"，天然变成时间线。
2. **否定不生效**："11-14起泡酒为例外" 与 "已决定戒酒" 同时 active —— 没有逐条失效，只靠 LLM 每次自己判断，它判断不动。
3. **画像永不衰减**：一条事实只有"下一次该字段被重写且 LLM 主动省略"一条出路。没有 TTL、没有复审。

---

## 2. 已拍决定

| # | 决定 | 结论 |
|---|---|---|
| 1 | 「补充说明」去留 | **删**。它的本质是"没有类型"，只会变垃圾桶；内容由「协作偏好 / 硬约束 / 饮食偏好」接住 |
| 2 | 目标本次做到哪 | **只瘦身**：`长期目标` 缩到 3 条纯目标，行程/立场/历史赶去记忆与事件。状态机留到 P2 |
| 3 | 用户编辑权限 | **改 / 删 / 新增全给**，用户新增标 `source=user`；用户删除记为 `rejected`，LLM 永不复活 |
| 4 | 每周重写触发 | **cron 每日节拍 + 条件判定**：距上次重写 ≥7 天，且期间新增事件 ≥3 或新增会话 ≥5 |
| 5 | 「重要关系」本次动不动 | **完全不动**：容量 10 条 × 80 字保持现状。未来直接换成事件/实体那套新机制，现在瘦身等于白做 |
| 6 | 用户编辑入口放哪 | **macOS 设置页**（不走右侧栏）。右侧栏是"看内容"的地方，改画像属于设置 |
| 7 | 画像预算 | **1600 → 3000 字**。单字段上限不变，多给的是"字段数变多 + 关系维持现状"的空间 |

---

## 3. 目标字段表

字段按「变化速度」分三层。`Layer` 决定注入顺序、预算优先级和抽取门槛，是 v2 的核心概念。

| Layer | 字段 | 形态 | MaxLen | MaxFacts | 相对现状 |
|---|---|---|---|---|---|
| **A 身份**（永不丢） | 称呼 | single | 10 | 1 | 不变 |
| | 性别 | single | 2 | 1 | 不变 |
| | 出生日期 | single | 10 | 1 | 不变 |
| | 籍贯 | single | 30 | 1 | 不变 |
| | 常驻地 | single | 30 | 1 | **新增** |
| **B 协作偏好**（永不丢） | 协作偏好 | list | 40 | 5 | **新增**（沟通风格 + 硬性指令合为一栏） |
| **C 硬约束**（永不丢） | 硬约束 | list | 50 | 5 | **新增**（过敏 / 忌口 / 健康 / 宗教 / 法律身份限制） |
| **D 近况基本盘**（有预算才进） | 职业阶段 | list | 50 | 4 | 由「职业领域」改名收紧 |
| | 家庭结构 | list | 40 | 3 | **新增**（骨架：已婚 / 父母在焦作 / 弟有一子） |
| | 财务轮廓 | list | 40 | 3 | 由「财务概况」收紧 |
| **E 偏好**（有预算才进） | 饮食偏好 | list | 40 | 4 | 由「饮食习惯」拆分（**只留"喜欢/不喜欢"**） |
| **F 骨架**（有预算才进） | 重要关系 | list | 80 | 10 | **完全不变**（未来换事件/实体机制，本期不动） |
| | 长期目标 | list | 50 | 3 | 缩到 3 条纯目标 |
| **删除** | 补充说明 | — | — | — | 见决定 1 |

说明：

- 不新增「语言」字段：单用户场景无价值。
- 「居住与搬迁」不单独设字段：当前城市进 `常驻地`，搬迁计划属于目标，进 `长期目标` / 未来状态机。
- 人物关系**结构与容量都不动**（10 条 × 80 字）。用户已定：未来直接换成"事件 + 实体"那套新机制，本期动它等于白做，还会污染未来的迁移。只保留 §4.2 的抽取硬规则（禁时间锚、禁代词）对它同样生效 —— 这一条不改结构，但能立刻把"10-24 头疼强撑见小吴"这类流水账挡在门外。

### 3.1 预算重算

维持「重要关系」10×80 之后，新上限的最坏总长：

| 层 | 最坏字数 |
|---|---|
| A 身份（称呼 10 + 性别 2 + 生日 10 + 籍贯 30 + 常驻地 30） | 82 |
| B 协作偏好 5 × 40 | 200 |
| C 硬约束 5 × 50 | 250 |
| D 职业阶段 4×50 + 家庭结构 3×40 + 财务轮廓 3×40 | 440 |
| E 饮食偏好 4 × 40 | 160 |
| F 重要关系 10×80 + 长期目标 3×50 | 950 |
| 小计 | **2082** |
| 每字段 XML 标签开销（13 字段 × ~30） | +390 |
| 合计 | **≈ 2472** |

但这是"13 个字段全部写满"的极端值；真实数据里通常有 3–5 个字段为空（空字段不注入）。日常量级在 1200–1800 字。

因此 `budget_profile` 从 1600 提到 **3000**（`conf/config.yaml:172` + `config/config.go:902` 默认值 + `ctxflow.DefaultZoneBudgets`）：

- 3000 覆盖最坏情况（2472）仍有 ~500 字余量，以后加字段不必再改预算
- 便于理解的口径：预算单位是**字符数（rune）**不是 token。3000 个中文字约 2000 token，且这段进的是 **system prompt 前缀**，DeepSeek 前缀缓存命中后基本不重复计费；真正的成本是首次构建，不是每轮
- 稳定上下文总帽 = profile + summary = **3400**，不与 user 消息侧的资源区总预算冲突

---

## 4. 机制

### 4.1 分层排序 + 从尾部丢弃（P0）

- `ProfileFieldDef` 增加 `Layer int`（A=0 … F=5），排序键 = `(Layer, Field)`，保证**确定性**（缓存友好）且身份字段永远在最前。
- `BudgetResourceZone` 对 `TypeProfile` 改为**从尾部丢弃**（丢 E/F 层，保 A/B/C 层）；总预算兜底阶段同理。其余类型保持原逻辑。
- 因为每字段在写入时已受 `MaxLen × MaxFacts` 约束，单字段长度天然有界，不需要"条级截断"这种新机制。
- 两处排序要一起改：`orchestrator.buildProfilePackets`（生成）与 `ctxflow.sortPacketsByType`（解析历史 XML 时）——统一走 `profilefield.SortKey(field)`，避免两处漂移。

### 4.2 抽取硬规则（P0，纯 prompt）

在 `profileRules` 与冷启动 prompt 里加三条：

1. **禁时间锚**：写进画像的条目**不得出现具体日期、金额、地点组合**。出现即说明这是事件，整条不写。示例（反例）：`11-13青岛鲜啤`、`10-17东京喝三杯`、`喝了打200块`。
2. **禁代词与时间参照**：每条必须能脱离上下文独立成立；不写"以前""上周""某天"。
3. **同字段互斥取最新**：语义互斥时只保留最新一条（"已戒酒" 覆盖 "喝酒"），不并存。

P0 阶段证据门槛只能靠 LLM 自判（"该信息是否为用户明确自陈，或在本段对话中重复出现 ≥2 次"）；P1 用库里的计数严格落实。

### 4.3 事实时间与证据（P1，一次迁移）

`user_profile_facts` 加列（纯加法，`AutoMigrate` 自动建列，启动时一次性回填）：

| 列 | 含义 | 回填 |
|---|---|---|
| `observed_at` | 首次被观察到 | `created_at` |
| `last_seen_at` | 最近一次被证实 | `created_at` |
| `evidence_count` | 被独立提及次数 | 1 |
| `source_msg_id` | 最近一次来源消息 | 空 |
| `source` | `inferred` / `user_stated` / `user` | `inferred` |
| `invalidated_at` | 失效时间（替代裸 `status=outdated`） | 空 |

`status` 增加取值：`pending`（观察中，**不进注入**）、`rejected`（用户否决，永不复活）。`GetActiveByUser` 只取 `active`，所以新状态天然不进上下文，无需改查询。

回填写在 `app/init_db.go` 的 `AutoMigrate` 之后，仿 `purgeLegacyPlaintextTokens` 的做法，幂等（`WHERE observed_at IS NULL`）。

**证据门槛（晋升规则）**：

- 用户明确自陈 → 直接 `active`，`source=user_stated`。
- LLM 推断 → 先 `pending`；同字段内再次被提及时 `evidence_count+1`、`last_seen_at=now`；`evidence_count ≥ 2` 时晋升 `active`。
- 匹配方式：归一化后精确匹配 + 包含关系（不做向量，避免为一个字段接一套 embedding 链路）。

**互斥失效**：LLM 判定新事实与旧事实互斥时，旧行 `invalidated_at=now`、`status=outdated`，不物理删。

### 4.4 用户编辑与锁定（P1）

- 用户新增 → `active` + `source=user`；用户编辑 → 改 `value`，保留 `source=user`。
- 用户删除 → `status=rejected`（不物理删）。LLM 后续输出若与该行归一化相同 → 丢弃。
- **用户行豁免整字段替换**：`applyExtracted` 在替换前把旧的 `source=user` 条目取出，若 LLM 输出中未包含，则原样重新插入（仍 active）。这样"整字段替换"的简单语义不用改，用户手写的条目也不会被 LLM 冲掉。
- API：
  - `GET /api/profile` → 按 Layer 分组返回（含 `last_seen_at`、`source`、来源会话标题）
  - `POST /api/profile/facts` → 新增
  - `PATCH /api/profile/facts/:id` → 改
  - `DELETE /api/profile/facts/:id` → 置 rejected
- 客户端（macOS）：**设置页**新增「关于我」（不走右侧栏）。按层分组，每条显示"最近证实 11-15 · 来自「…」"，可编辑可删可新增。样式沿用 `docs/design/mac-apps/settings-sheet-style.md` 的 `SettingsCard` / `KeycapView`，不新造一套设置页外观。

### 4.5 每周整体重写（P2）

增量（现有 session_end）负责及时，重写负责收敛。

- 触发：`CronRunner` 每日节拍检查 → 距上次重写 ≥7 天 **且**（新增事件 ≥3 或新增会话 ≥5）。
- 输入：近 7 天事件（`Name + Summary`）、现有画像全量、高频记忆 top N。
- 输出：整份画像（同 JSON 格式），允许合并/压缩/删除。
- 与增量的关系：重写直接整字段替换，`source=user` 行同样豁免。
- 失败降级：只打日志，不阻塞；下次节拍重试。

关键点：**重写的输入里必须有事件**。现在抽取只看对话字面，所以只能反复抄日期；有了事件聚合，"喝过几次啤酒"和"戒酒"才能被正确归并。

> 实现（2026-11-20）：`internal/agent/profile_consolidate.go`。到期状态存
> `profile_rewrite_states`（每用户一行：last_run_at / 上次输入计数 / last_status / last_error）；
> 触发常量写死（7 天 / 3 事件 / 5 会话）。`cmd/profile-rebuild` 退化为它的 `--force` 薄壳
> （同一份 prompt，不再两套）。

### 4.6 目标状态机（P2，仅预留）

`长期目标` 的最终形态是带状态的对象（`open / waiting / done / dropped` + `deadline` + `next_step`），并与 plan/todo 同源。本期只做字段瘦身，**不新建表**；等 P2 评估时再决定是并入 plan 体系还是独立成表。

> 评估结论（2026-11-20，P2 收尾时）：**不新建表，并入 plan 体系**。理由：
> ① `ConversationPlan` + `PlanStep` 已经提供 `pending/in_progress/done` 三态、顺序与勾选，
> 缺的只是 `deadline` 与 `dropped`，而这两项都能用 `PlanStep` 扩展字段表达；
> ② 长期目标与"这周要做什么"本来就是同一根轴上的粗细两端，独立成表立刻要写双向同步；
> ③ 目标目前只有 3 条、全部是方向性陈述（回西安 / 买房后生育 / 保持阅读），没有排期语义，
> 为它建状态机是纯投机。等真的出现"要跟进度、要截止日"的目标（比如求职时间线）再动手。
> 届时改 `PlanStep`（加 `deadline` + `dropped`），画像侧只保留"目标文本"，不复制状态。

---

## 5. 分期与改动清单

### P0 —— 立竿见影（4–6h）

| 文件 | 改动 |
|---|---|
| `internal/profilefield/profilefield.go` | 字段表按 §3 重写；加 `Layer` 与 `SortKey()`；删「补充说明」 |
| `internal/profilefield/profilefield_test.go` | 字段清单断言同步 |
| `internal/agent/ctxflow/resource_zone.go` | `budgetPerType` + 总预算兜底：profile 改从尾部丢；`sortPacketsByType` 用 `SortKey` |
| `internal/agent/ctxflow/resource_zone_test.go` | 新增：13 字段满配超预算时丢掉的是 E/F 层，A 层保留 |
| `internal/agent/orchestrator.go` | `buildProfilePackets` 按 `SortKey` 排序，并加白名单过滤（非白名单字段不再注入，兜住漏搬的旧字段） |
| `internal/agent/profile_worker.go` | `profileRules` 加三条硬规则；冷启动 prompt 同步；新增"清理非白名单字段"的幂等启动步骤 |
| `internal/app/init_db.go` | 字段搬家 SQL（§6.1）+ 非白名单字段置 outdated |
| `internal/config/config.go` + `conf/config.yaml` | `budget_profile` 1600 → 3000 |
| `internal/agent/subagent.go:177` | profile 工具描述里的字段举例更新 |
| `docs/design/facts-system/*` | 若提及字段清单，同步 |

验收：

- `go build ./... && go test ./...` 全绿
- 真机跑一次聊天 → prompt 调试页 `<profile>` 内 `item` 数量 = 非空字段数，顺序 A→F，且 `称呼` 类身份字段必须在
- 手动构造一条含日期的候选（如"11-13青岛鲜啤"）→ 提取结果中不出现

### P1 —— 时间、证据、可见性（5–8h）

| 文件 | 改动 |
|---|---|
| `internal/model/user_profile_fact.go` | 加 6 列 + 3 个 status 常量 |
| `internal/app/init_db.go` | 启动回填 `observed_at / last_seen_at / evidence_count` |
| `internal/service/user_profile_fact_repo.go` | 新增 `Promote`/`Invalidate`/`Reject`/`ListByLayer`；`ReplaceFieldFacts` 支持保留 `source=user` 行 |
| `internal/agent/profile_worker.go` | 证据门槛、互斥失效、用户行豁免、rejected 过滤 |
| `internal/agent/profile_worker_test.go` | pending 不注入 / 二次提及晋升 / rejected 不复活 / user 行豁免 |
| `internal/controller/profile.go`（新）+ `router.go` | 4 个 API |
| `client/xHelper/xHelperMac/ProfileSettingsSection.swift`（新）+ `LocalSettingsViews.swift` | 设置页「关于我」分组（`SettingsCard` + `SettingsStyle`）+ ViewModel + APIClient 方法 |
| `cmd/profile-rebuild/`（新） | 一次性重建（§6.3）：备份 dump + dry-run diff + `--write` 落库 |

验收：迁移后任取一个用户，`observed_at` 非空且等于旧 `created_at`；面板能改一条、删一条，重跑一次 session 后删除项不复活。

### P2 —— 收敛（2–4h）

| 文件 | 改动 |
|---|---|
| `internal/agent/profile_consolidate.go`（新） | 每周重写 worker |
| `internal/appinit/agents.go` + `cron` | 接线 + 条件判定 |
| `internal/agent/profile_worker_test.go` | 幂等、失败降级 |

---

## 6. 老数据修复

当前只有一个用户，所以不做通用批量迁移，也不分页：一条命令、跑前先看 diff、跑完人工验。分三步，顺序不能反。

### 6.1 第一步：字段搬家（随 P0 上线，纯 SQL）

只搬格子，不清内容。**必须与 P0 同步**，否则旧字段名会继续注入（`buildProfilePackets` 不校验白名单，新字段反而永远空）。

| 旧字段 | 新字段 | 处理 |
|---|---|---|
| 职业领域 | 职业阶段 | 改名；**内容一行不动**（配额收敛交给下一次 session_end） |
| 财务概况 | 财务轮廓 | 改名；同上 |
| 饮食习惯 | 饮食偏好 | 改名；整条搬（含日期的也先搬，第三步统一清） |
| 补充说明 | — | 置 `outdated`（不物理删） |
| 其他 | — | 不动 |
| **重要关系** | — | **整字段跳过，一个字节不动**（等未来的事件/实体机制） |

实现：启动时幂等 SQL（仿 `purgeLegacyPlaintextTokens`，写在 `app/init_db.go`）。不用一次性迁移框架。

**有意不截断/不裁条**：启动迁移只做「改名 + 非白名单字段退场」，改写内容会让旧文本不可恢复；超配额条目留给下一次 session_end 的提取按新配额重写，期间多几条注入无害（预算 3000 兜得住）。

### 6.2 第二步：全量备份（跑重建之前，必须）

整字段替换 + `CleanupOutdated` 只留最近 3 批 —— 重建不满意时旧值可能已被物理删除，回不来。

```
server/data/profile-backup-<user8>-<YYYYMMDD-HHMMSS.纳秒>.json   // BJ 时间
  { db, user_id, exported_at, facts: [...] }        // active + outdated + rejected 全量
```

文件名带 user 与纳秒：`profile-rebuild` 与每周重写都落这个目录，秒级命名在多用户连续跑时会互相覆盖。

`server/data/` 已 gitignore。这份文件同时充当"隔离区"：被剔掉的事件型条目全在里面，未来做人物/事件机制时是原料，现在不造导入管线。

### 6.3 第三步：一次性重建（P1 落库列之后，手工跑）

新增 `cmd/profile-rebuild`，风格同 `cmd/entity-gc`：**默认 dry-run**，`--write` 才落库，prod 库需 `CONFIRM_PROD=yes`。

**输入取证顺序**（不能让旧画像当主体）：

1. 事件 Top 10（`Name + Summary`）
2. 近期记忆 Top N
3. 旧画像，明确标注"其中可能混有事件，凡带日期/金额/地点的整条丢弃"

**dry-run 输出字段级 diff**（人工逐条审）：

```
饮食偏好
  [保留] 偏好带气、无糖、无咖啡因饮料
  [丢弃] 10-17东京笕塚烧鸟店喝三杯380ml札幌啤酒共1140ml   ← 日期/地点/容量
  [丢弃] 以前330ml就微醺，现2000ml才晕                    ← 时间参照 + 身体反应史
  [丢弃] 喝了打200块                                      ← 金额
硬约束
  [新增] 已戒酒 / 喝酒后头晕
常驻地
  [新增] 北京（计划回西安）
长期目标
  [保留] 回西安定居，看重生活质量与子女教育
  [丢弃] 明年与 CJ 去意大利 7 天，预算 3-4 万              ← 有起止时间的行程 = 事件，不是目标
  [丢弃] 2023年法国12-13天5万                             ← 历史不是目标
  [改写] 西安租两室一厅预算3000元内 → 计划回西安，租房预算 3000/月
```

**写入时打标**（依赖 P1 的新列；2026-11-20 按实现修订）：

- `source = migrated`（一次性重建）/ `source = rewrite`（每周重写），面板可一眼区分
- 直接以 `UserStated=true` 对账 → 新行落 `active`，不受证据门槛打回 `pending`；`evidence_count` 从 1 起算
- `observed_at` = 本次重写时间（不是备份里的 `created_at`：那个值被历次整字段重写刷新过，不可信）
- `last_seen_at` = now
- `重要关系` 仍整字段跳过，LLM 不许碰
- `source=user`（用户手写）与 `status=rejected`（用户删过）行由 `ReconcileFieldFacts` 保护，重写不越权

### 6.4 两个必须接受的近似

1. **`observed_at` 只能是近似值**。历史 `created_at` 被每次整字段重写刷新过，同字段所有事实共享"最近一次重写时间"。另一条路是从同字段 `outdated` 行里找相同文本的最早 `created_at`，但 LLM 每次改写措辞、命中率极低 —— **不做**。
2. **重建后画像会一次性变薄**，这是预期。之后靠 P2 每周重写 + 证据门槛长回来，不再造第二套常驻迁移逻辑。

### 6.5 工时

备份 + 字段搬家 ≈ 1–1.5h（并进 P0）；`profile-rebuild` ≈ 2–3h；人工审 diff ≈ 0.5–1h。

---

## 7. 不做的事

- 不做人物实体表 / 关系图，**也不改「重要关系」的容量与结构**（用户已定：未来直接换成事件/实体机制，现在动它是白做）。本期只有 §4.2 的抽取硬规则对它生效。
- 不做画像的向量检索或语义召回（字段数有限，全量注入足够）。
- 不做多用户隔离以外的东西（当前是单用户场景）。
- 不改 `Fact` 表（facts-system 那套未接线的系统保持原样，不在本期合并）。
- 不做目标状态机（P2 只评估，不实现）。

## 8. 风险

| 风险 | 影响 | 对策 |
|---|---|---|
| 收紧 MaxLen 后，旧条目在下次重写前仍是旧值 | 面板短期仍显旧内容 | P0 的"清理非白名单字段"只处理被删字段；其余靠下一次 session_end 自然重写 |
| 删「补充说明」可能丢掉确实有价值的杂项 | 信息损失 | 存量标 `outdated`（不物理删），可随时回滚；新字段覆盖大部分场景 |
| 证据门槛（P1）会让画像短期内变薄 | 主观感受"变笨" | 冷启动与用户自陈不受门槛限制；`pending` 只是不注入，数据仍在 |
| 用户行豁免逻辑与整字段替换耦合 | 可能出现重复行 | 写入前按文本归一化去重；单测覆盖 |
| 定时重写与增量并发写同一字段 | 后写覆盖先写 | 同一用户串行（复用现有 worker 组内串行语义），重写前重新读一次 active |
| 「重要关系」保持 10×80，最多占 800 字预算 | 其余字段被压 | 预算提到 3000 后最坏仍有余量；它本来也是未来要整体替换的字段 |
| 预算提到 3000 增加 prompt 成本 | 首次构建 +~2000 token | 这段在 system prompt 前缀里，命中前缀缓存后不再重复计费 |
| 旧字段名不搬，会与新字段并存并持续注入 | 画像里同时出现「饮食习惯」「饮食偏好」 | P0 必须含字段搬家（§6.1），且 `buildProfilePackets` 增加白名单过滤兜底 |
| 备份 JSON 含真实个人数据 | 误提交进仓库 | 固定落 `server/data/`（已 gitignore），文件名带日期，不放进 `docs/` |
| 重建是写 prod 库的一次性操作 | 不可逆 | 默认 dry-run；`--write` 需 `CONFIRM_PROD=yes`；先做 §6.2 备份 |

## 9. 后续变更

### 2026-11-20 · 实现记录（P0 + P1）

已按本方案落地 P0 与 P1；P2 未做。实现与文档的差异与补充：

| 项 | 落地方式 |
|---|---|
| 证据门槛的「用户自陈」判定 | 不做 LLM 打标（会改输出格式、抬高解析回归风险）。改为：事实文本（去空白/小写归一）出现在**本 session 用户消息**里 → 判定用户自陈，直接 `active`；否则进 `pending`。冷启动与启动迁移视为全部自陈（事实来自历史记忆）。 |
| 对账写入 | 新增 `UserProfileFactRepo.ReconcileFieldFacts`（单字段集合式读写，无 N+1）。`ReplaceFieldFacts` 退化为它的全自陈包装，用户行 / rejected 行保护只在一处。 |
| 老数据重建打标 | 新增 `MarkBatchSource`：重建批次落库后统一标 `source=migrated`。 |
| 设置页入口 | 「关于我」是设置页的独立 sheet（`BrowseRow` → `ProfileSettingsView`），不是塞进 Form 的一堆行。 |
| 长期目标的边界 | 评审后补：旅行/出差/看房这类**有具体起止时间的安排**明确判为事件，不进画像（`长期目标` 的 ExtractDesc 原来把"明年去意大利 7 天"当正面例子，等于教模型收行程，已改；抽取规则三处同步）。 |
| 时间回填 | `BackfillFactTimestamps` 在 `init_db.go` 与字段搬家一起跑（幂等，`WHERE observed_at IS NULL`）。 |
| 启动迁移的截断 | **不做**。评审指出改写内容会让旧文本不可恢复，且与 §6.1「只搬格子不清内容」自相矛盾。改为只改名 + 非白名单退场，配额收敛交给下一次 session_end（本条已同步 §6.1）。 |
| 注入 `updated_at` | 从 `created_at` 改为 `last_seen_at`（缺省退回 `observed_at` → `created_at`），让 LLM 看到"最近一次被证实"的时间。 |

~~P2（每周整合重写 + 目标状态机）未做~~：已于同日完成，见下节。

### 2026-11-20 · P2 实现记录（每周整合重写）

| 项 | 落地方式 |
|---|---|
| 与 `profile-rebuild` 的关系 | 核心逻辑搬进 `agent.ProfileConsolidator`（prompt/调用/diff/写入/备份全在一处），CLI 退化为薄壳调 `Rewrite(…, {DryRun, Force:true})`。**不再有两份 prompt**，避免改了抽取规则只改一处。 |
| 到期状态 | 新建 `profile_rewrite_states`（user_id 主键 + last_run_at / 上次事件数·会话数·事实数 / last_status / last_error）。不选"从 facts.created_at 推导"：一次重写若无任何字段变化就不产生新行，推导值会停在上次真有变化的时刻，退化成每天白跑。 |
| 触发 | 常量：距上次 ≥7 天，且近 7 天新增事件 ≥3 或新增会话 ≥5。不加配置项（先跑一段时间看真实分布再决定是否暴露）。**首次运行（无状态行）只记基线不重写**，避免上线/重启那一刻静默改写画像；手工 `--write` 会把 `last_run_at` 推到当下，之后满 7 天由 worker 接管。 |
| 会话计数 | 新增 `ConversationRepo.CountTouchedSince(userID, since)`（按用户 + 返回 error）。不复用 `CountActive`：它不分用户且吞掉 error。 |
| 证据输入 | 事件 = `EventRepo.ListUpdatedSince`（近窗、按 last_memory_at 排序）；记忆 = `UserMemoryRepo.ListUpdatedSince` 取窗口内最新 30 条。**近似**：设计写的是"高频记忆 top N"，库里没有可靠的高频口径，先按时间窗取。 |
| 写入 | 逐字段 `ReconcileFieldFacts`（`UserStated=true` → 直接 active，不受增量证据门槛）。`重要关系` 整字段跳过（模型输出丢弃）。`source=user` / `rejected` 行由对账保护，重写不越权。 |
| 失败处理 | 备份失败 → 本轮不落库（宁可少跑一次）；LLM/解析失败 → 记 `last_error`、**不推进** `last_run_at`，下次节拍自动重试；状态写失败只记日志。空输出（`{}` 或解析不出字段）按失败处理，不冒充"无事发生"。 |
| 备份 | 落库前写 `profile-backup-<user8>-<YYYYMMDD-HHMMSS.纳秒>.json`（0600，BJ 时间，含历史批次）。落点：SQLite 跟库同目录、MySQL 用 `data/`。dry-run 不备份（不写库）。 |
| 调度 | `CronRunner` 加 24h ticker + 启动补跑一次（ticker 首触在 +24h，重启频繁会永远等不到），running 标志防重入；`GetHookStatus` 增加 `profile_rewrite_remaining`。 |
| CLI | `make profile-rebuild` 行为不变（默认 dry-run）、`--write` 落库；去掉没人用过的 `--events/--memories` 两个旋钮。 |
| 前端 | 新增 `source=rewrite`，面板显示"每周整合"。 |
