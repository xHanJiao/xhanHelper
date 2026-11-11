# 事件注入重写：全量列表 + 命中附记忆（2026-11-09）

> 取代 `design.md` §6.3–§6.5 的「四形态 + 状态机 + 配额降级」。拼装、数据模型、工具、只读 API 均不变。

## 结论

资源区不再靠向量检索**决定哪些事件出现**，而是**全量列出所有未删除事件**；检索只决定**给哪些事件附记忆**。

| 情况 | 形态 | 附带记忆 |
|---|---|---|
| 最近 14 天有记忆活动（`last_memory_at`） | `summary` | 最近 3 条（`updated_at DESC`） |
| 超过 14 天 | `index` | 无（只有 id / 标题 / 日期 / 记忆数） |
| 本轮命中记忆（相似度过阈值） | `hit` | 该事件**命中记忆** top 10（按分数降序），覆盖年龄规则 |
| `event.enable` 钉住 | `full` | 最近 20 条（`updated_at DESC`），覆盖年龄规则 |

参数（用户拍板）：

| 参数 | 值 |
|---|---|
| 摘要窗口 X | **14 天** |
| recent 附带记忆 | **3 条** |
| 命中事件附带记忆 | **10 条** |
| 命中事件数上限 | **3 个**（按事件分数降序） |
| pinned 附带记忆 | **20 条**（现有 `event_memories`） |
| 命中事件数上限 / pinned 上限 | 3 / 3（现有 `quota_event_pinned`） |

## 为什么改

1. **检索选事件的召回不稳。** 现在只有命中记忆的事件才会出现，没命中就只剩「上一轮携带」；用户聊到一件事、但向量没命中时，事件整块消失。
2. **形态状态机难解释。** full/compact/summary/index 由「命中 + miss 计数 + 配额」三者共同决定，同一句话在两轮里可能拿到完全不同的注入，行为不可预测。
3. **命中记忆没有用上。** 现在命中事件带的是「最近 20 条记忆」，不是命中的那几条；命中的是旧记忆时，注入里可能一条都不含它。
4. 全量列表让「有哪些事件」变成稳定事实，模型不用猜；记忆按需附上，成本可控。

## 新模型细节

### 排序

`pinned → hit → recent(summary) → index`，组内按 `last_memory_at DESC`、同值按 `id`（与列表接口一致，保证字节稳定）。

### 记忆取法

- **hit**：`EventSource` 现在就有记忆级分数（`searchMemoryScores` 返回 `memoryID → score`）。按事件分组后，取该事件命中记忆按分数降序前 10，**不是**按 `updated_at`。
- **recent**：没有命中信息，取最近更新的 3 条。
- **pinned**：最近更新的 20 条（维持现状）。
- 三种都只带 `memory_value`，不带 `facts`；`facts` 只在 `event.get` / `event.enable` 的工具结果里出现。

### 命中事件跨过 14 天

命中事件即使是 30 天前的，本轮也给 `summary + 命中记忆`；下一轮没有新命中就回到 `index`。不引入持久状态。

### 命中事件数上限

按事件分数降序取前 3 个；其余命中事件按普通规则处理（recent → summary + 3 条，老 → index）。避免一次聊到多个话题时记忆爆炸。

### 事件引用 / 携带

事件每轮都从 DB 全量重建，**不再从上一轮 XML 携带**。`CarryEventPackets`、`EventRefsFromPackets`、`miss` 计数、跨窗口 carried 全部删除。

短输入（`SkipRetrieval`）或检索失败时也要出全量列表：`EventSource.ListForInjection`（不跑向量检索，字面命中仍生效），orchestrator 通过 `EventLister` 接口调用它。**不能**沿用「携带上一轮」——那会让列表冻结在旧状态。

`unified_search` 的语义检索走 `SearchOptions.EventsAll=false`，只返回命中事件（不返回 pinned / recent / index），保持检索工具原有语义；主对话走 `EventsAll=true`。

## 成本（真实数据：25 个活跃事件，其中 12 个在 14 天内）

按真实 XML 编码实测（ASCII 0.3 token/字符、中文 0.6）：

| 配置 | 字符 | ≈ tokens/轮 |
|---|---|---|
| 摘要 + 索引（无命中） | 5,908 | 2,427 |
| recent 各 3 条 + 命中 1 个 × 10 | 13,554 | 5,771 |
| recent 各 3 条 + 命中 3 个 × 10 | 16,096 | 6,910 |
| ↑ + pinned 1 个 × 20 | 17,813 | 7,664 |
| 现状（向量选 + 携带，最近 80 轮实测） | p50 1,830 / p90 5,199 | p50 ~550 / p90 ~1,600 |

最坏约 7.7k tokens/轮（128k 窗口的 6%），按效果优先接受。成本不是瓶颈，**瓶颈是下面两个预算帽**。

## 预算与配置变更

| 配置 | 现值 | 新值 | 原因 |
|---|---|---|---|
| `retrieval.resource_zone.budget_event` | 12000 | **18000** | 最坏 17.8k，留 200 余量 |
| `retrieval.resource_zone.budget_chars` | 15000 | **24000** | event + memory 3000 + recent 1710 + notice |
| `retrieval.resource_zone.budget_memory` | 1000 | **3000** | 用户确认放大；`quota_memory: 6` 仍是条数上限（≈1.4k） |
| `retrieval.resource_zone.quota_event_hit` | — | **3** | 新增：命中事件数上限 |
| `retrieval.resource_zone.event_hit_memories` | — | **10** | 新增：命中事件附带记忆数 |
| `retrieval.resource_zone.event_recent_memories` | — | **3** | 新增：recent 事件附带记忆数 |
| `retrieval.resource_zone.event_recent_window_days` | — | **14** | 新增：摘要窗口 |
| `quota_event_full / compact / summary / index` | 4 / 6 / 12 / 25 | 删除 | 四形态状态机取消；`index` 不再有上限（预算兜底） |

不抬预算的后果：`BudgetResourceZone` 会从最低优先级事件开始降级/丢弃，静默把全量列表砍回部分列表——正是这次要消除的行为。

## 实现改动

| 文件 | 改动 |
|---|---|
| `server/internal/agent/retrieval/event_source.go` | 去掉 `eventState` / `degrade` / `fresh→full→compact` 状态机与 `PrevEventRefs`；改为「全量列表 + 命中分组 + 按形态取记忆」。新增 `ListForInjection()`；新增 `EventModeHit`。日志改为 `[event] inject: active=%d hit=%d (pinned/hit/summary/index=%d/%d/%d/%d)` |
| `server/internal/agent/retrieval/types.go` | `SearchOptions` 删除 `PrevEventRefs` / `IsNewSession`，新增 `EventsAll`；`DescEvents` 改写为全量列表口径 |
| `server/internal/agent/ctxflow/blocks.go` | 事件记忆渲染条件改为 `len(Memories)>0`；删除 `carried` / `miss` 属性；`eventModeRank` 增加 `hit` |
| `server/internal/agent/ctxflow/resource_zone.go` | 删除 `CarryEventPackets` / `EventRefsFromPackets`；`MergeResourceZone` 仍跳过 prev 事件；`BudgetResourceZone` 的事件降级顺序改为 **pinned/hit 不动 → summary 降 index → index 再丢** |
| `server/internal/agent/orchestrator.go` | 新增 `EventLister` 接口与 `eventZonePackets` 方法（检索缺失时回退 lister）；删除 `PrevEventRefs` 解析 |
| `server/internal/appinit/agents.go` | 接线新配置项；把 EventSource 以 `EventLister` 形式注入 orchestrator |
| `server/cmd/event-bootstrap/main.go` | `EventSourceConfig` 字段同步 |
| `server/internal/config/config.go` | 新增 4 个配置 + 默认值/校验；删除四形态配额字段 |
| `server/conf/config.yaml` | 按上表改值 |

`<events>` description 新文案（关键：说明这是全量列表、索引也能查）：

> 用户现实里持续在跟进的全部事件（未删除）。最近有活动的带当前状态 summary 和少量最近记忆；更早的只有标题，需要细节用 `event.get` 查。mode=hit 是本次对话命中的事件，附带命中的记忆；mode=full 是用户钉住的事件，附带最近 20 条记忆。

## 边界

- **事件软删 / 合并**：全量列表只查 `is_deleted = false`；已删事件不再出现，记忆已退回普通池，不受影响。
- **命中的 `event_id` 指向已删事件**：跳过并记日志（沿用现有防御）。
- **pinned 列表里有失效 ID**：跳过，不占名额；下次 `event.enable` 写入时顺手剔除（沿用现状）。
- **预算兜底**：`pinned` / `hit` 不降不丢；recent 先降 index，index 再丢。全是 pinned 时接受超预算（沿用现状）。
- **confirm_count**：事件块里的记忆不计入 `recordInjectedMemories`（只统计 `TypeMemory`）。这是现状，本次不改；若要「命中即确认」，需要单独拍板。

## 不做

- **稳定上下文拆分**（把全量列表放 system prompt 吃前缀缓存）：效果优先暂不需要，等真觉得每轮 7k tokens 贵了再做。改动点已明确：`buildStableContextXML` 多接一个 event 块、`MergeResourceZone` 不再带事件。
- **facts 进注入**：仍只在工具结果里给。
- **`event.get` 截断**：主对话实时工具结果不截断，保持现状。

## 验收

1. `go build ./... && go test ./...`；补单测：
   - 25 个事件（12 recent / 13 老）全量编码，断言 index 条数、recent 记忆条数、hit 覆盖年龄；
   - 命中 4 个事件时只 3 个带 10 条记忆；
   - 短输入路径仍出全量列表；
   - 预算兜底先降 recent 再丢 index，pinned/hit 不动。
2. 真机：聊「回西安」→ 事件块含该事件 summary + 命中记忆；聊无关话题 → 列表仍在、无 hit；新会话 → 列表仍全量（不再 carried）；`event.enable` → 下一轮 full 20 条。
3. 日志：`[event] inject` 与 `[resource_zone] budget trim` 无异常降级。

## 后续变更

- 2026-11-09 实施：按本方案落地（14 天 / recent 3 条 / 命中 10 条最多 3 个事件 / pinned 20 条）。预算与配置按上表生效；事件预算裁剪改为「summary 先降 index、index 再二分丢弃」，pinned/hit 不降不丢；短输入走 `EventLister.ListForInjection`；`unified_search` 用 `EventsAll=false` 保持只返回命中事件。影响文件：`server/internal/agent/retrieval/{event_source,types,context_sources}.go`、`server/internal/agent/ctxflow/{blocks,resource_zone}.go`、`server/internal/agent/orchestrator.go`、`server/internal/appinit/agents.go`、`server/internal/config/config.go`、`server/conf/config.yaml`、`server/cmd/event-bootstrap/main.go`。
- 已知未做（评审 P3）：`trimEventGroup` 二分「部分保留」分支缺测试；总预算路径仍是逐条降级/丢弃（规模假设：per-type 已封顶，超限很小）；summary 逐条降级时整组重编码（假设 14 天内活跃事件数不大）。
- 2026-11-09 行为准则：新增第 9 条「事件全貌」——用户在谈论某事件而注入形态不是 full（index/summary/hit）时，必须先 `event.enable` 钉住再回答；已 full 不重复钉，钉满 3 个先 disable 无关的。`DescEvents` 同步补「正在深入谈这件事就 event.enable 钉住」。影响文件：`server/internal/agent/promptbuilder/{static,builder_test}.go`、`server/internal/agent/retrieval/types.go`。
- 2026-11-09 术语：用户可见的「钉住」统一改为「关注」（工具标签 `关注事件` / `取消关注`、上限报错、准则 9、`DescEvents`、调试页徽标）。代码标识符与 XML 协议（`mode="full"`、`pinned="1"`）不变。
- 2026-11-11 位置调整：「事件全貌」规则内容不变，从行为准则第 9 条移到工具规则区（渲染在工具目录之后）；行为准则收窄为 1–7 的纯人格准则。影响文件：`server/internal/agent/promptbuilder/{static.go,builder.go,builder_test.go}`。
