# 附表 A · 事件字典

上游：[活动埋点系统 · 实施技术方案](./活动埋点系统 · 实施技术方案.md) ｜ [附表 B 表结构与索引](./附表 B · 表结构与索引.md)

**怎么用**：新增事件前先在这里查有没有能复用的；新增后必须登记一行，否则半年后没人知道系统里有哪些事件。命名格式 `domain.object.action`，全小写点分隔，**名字里不带主体**（谁做的靠 `actor` 区分），**失败不新增 type**（靠 `outcome`）。

## 公共字段（每条事件都有，由 emit 入口补）

| 字段 | 说明 |
|---|---|
| `event_id` | 客户端或服务端生成的全局唯一 id，幂等去重的依据 |
| `type` | 本表第一列 |
| `occurred_at` | 事情发生的时间（ISO 8601 带时区、**精度毫秒**），由产生方给 |
| `received_at` | 落库时间，离线补投时与上一列会差很远，排序兜底用它 |
| `actor` | `user` / `assistant` / `system` |
| `trigger` | `manual` / `batch` / `schedule` |
| `schema_version` | 表级版本号 |
| `biz_key` | 业务键，由 emit 入口按下表规则填；**没有业务键的事件留 NULL**（原 `ingest_source` 列已取消：2026-09-26 决定不做回填，不需要区分实时与补录） |
| `client_instance_id`、`client_build`、`os_version` | 仅客户端事件 |
| `outcome`、`error_type`、`error_msg` | 区间事件写在结束那条；**点事件也可以带**，默认 `completed`，失败了才写 `failed` |
| `pair_id` | 区间事件的开始与结束互指（**不用** `parent_event_id`） |
| `parent_event_id` | 因果父；根事件必须为空，不许"该填漏填" |

**三类形态**：点事件（一次动作）；区间事件（有起止、两条配对）；**状态类＝边界点事件 + 派生**（2026-09-26 修订，不再用心跳——理由见设计篇 3.2：心跳一天要 960 条，而它想防的悬挂区间已被闭合规则兜住）。判据仍是可靠性，不是量级。

**幂等的两道防线，谁负责什么**：第一道是 `event_id` 主键，**对所有事件生效**；第二道是业务键 `biz_key`，**只给服务端可能重放的事件用**（消息、turn、工具与 LLM 调用、子任务、系统变更）。客户端事件一律只靠 `event_id`——它们本地生成、不走重放路径；而"同一 URL 反复打开""同一秒连点两次确认"本身就是两次真实发生，硬造业务键只会把真事件吞掉。下表"幂等业务键"列写"—"的，就是只靠 `event_id` 去重。

**硬规则：`biz_key` 只能是"一个全局唯一 id"**——message_id、turn_id、call_id、task_id、对象 id，或服务端新生成的 uuid。**拼接键、序号、时间戳一律不许**：后台路径没有 turn，若拼出个 `bgflash#1`，第二天的同类调用就会撞上唯一索引，把一条真事件静默吃掉。凑不出唯一 id 就留 NULL——留 NULL 只是少一道防线，编一个假键那是丢数据。

## 对话与 LLM（服务端）

| type | 形态 | 必备字段 | 幂等业务键 | 父 | 说明 |
|---|---|---|---|---|---|
| `chat.message.sent` | 点 | conversation_id, message_id, content_len | message_id | 根（必须空） | 用户发言才算；助手与系统自动产生的不计 |
| `chat.turn.started` | 区间起 | conversation_id, turn_id | turn_id | 那条消息；助手主动搭话则根 | turn 是"一次助手回复周期"，不是一条用户消息 |
| `chat.turn.ended` | 区间终 | turn_id, outcome | turn_id | 同 | outcome 区分打断 / 网络断 / 正常完成 |
| `llm.call.started` | 区间起 | llm_call_id, turn_id（后台路径为空）, model, call_kind | llm_call_id（服务端为每次 LLM 调用生成的 id，可直接复用审计表主键） | chat.turn.started；后台任务算根 | call_kind 对应主 / 强 / 后台四档 |
| `llm.call.ended` | 区间终 | turn_id, prompt_tokens, completion_tokens, outcome | 同 | 同 | 不落逐 token，只留计数与速率 |
| `llm.first_token` | 点 | turn_id, ttft_ms | turn_id | llm.call.started | 首字延迟 |

## 工具与执行

| type | 形态 | 必备字段 | 幂等业务键 | 父 | 采集点 |
|---|---|---|---|---|---|
| `tool.call.started` | 区间起 | tool_call_id, tool_name, args_digest, args_len | tool_call_id（服务端生成）；没有就 NULL | chat.turn.started；后台任务算根 | 服务端工具调度器（统一包装一层） |
| `tool.call.ended` | 区间终 | outcome, error_type, result_len | 同 | 同 | 同 |
| `tool.bridge.dispatched` | 点 | call_id, tool_name, conversation_id, turn_id | call_id | chat.turn.started | 服务端（桥接工具：派发视角，含排队与网络） |
| `tool.bridge.returned` | 点 | call_id, outcome | call_id | 同 | 服务端（返回视角） |
| `client.tool.exec.started` | 区间起 | call_id, tool_name, conversation_id, turn_id | call_id | 服务端那条 `tool.bridge.dispatched` | 客户端（执行视角，真实耗时） |
| `client.tool.exec.ended` | 区间终 | call_id, outcome, error_type | 同 | 同 | 客户端；**终局只认执行方** |
| `task.spawned` | 区间起 | task_id, description_len | task_id | tool.call.started | 服务端子任务 |
| `task.ended` | 区间终 | task_id, outcome | task_id | 同 | 同 |

桥接工具的两个视角都要留、不许混成一条；服务端耗时为派发往返，客户端耗时为真实执行。

## 浏览（客户端）

| type | 形态 | 必备字段 | 幂等业务键 | 父 | 说明 |
|---|---|---|---|---|---|
| `browser.nav.started` | 区间起 | url_raw, host, owner, tab_id | —（只靠 event_id） | 助手代开的挂 `tool.bridge.dispatched`；用户自己点的是根 | `owner` 区分 user / agent |
| `browser.nav.ended` | 区间终 | outcome, error_type | — | 同 | 失败（404、超时）走 outcome，不新增 type |
| `browser.page.dwell` | 点 | url, dwell_ms | — | 根 | **降为辅助值、不进统计**：停留时长的权威是 nav 配对区间（崩溃时它仍能由闭合规则算出）；同一 URL 反复打开是常态，故不设业务键。注意 SPA 有 readyState 轮询兜底路径 |
| `browser.bookmark.added` | 点 | url, title_len | — | 根 | 用户显式动作 |

统计口径：默认只算 `actor=user` 的停留；助手替你打开的页面当上下文。

## 用户显式动作（客户端 UI 层）

| type | 形态 | 必备字段 | 幂等业务键 | 父 |
|---|---|---|---|---|
| `user.action.confirmed` | 点 | target_kind, target_id | —（只靠 event_id） | 根 |
| `user.action.dismissed` | 点 | 同上 | — | 根 |
| `user.action.deleted` | 点 | 同上 | — | 根 |
| `user.action.saved` | 点 | 同上 | — | 根 |
| `user.action.retried` | 点 | 同上 | — | 根 |

连点两次确认是两次真实动作，都要记，所以这类不给业务键。

**这一组才是"功能使用"的统计口径**。下面的系统变更事件不是——写入类动作绝大多数由助手代劳或批处理产生。

## 系统变更（服务端仓储层统一包装）

| type | 形态 | 必备字段 | 父 | 说明 |
|---|---|---|---|---|
| `doc.created` / `doc.updated` / `doc.deleted` | 点 | doc_id, size_delta | 由工具触发则挂 tool.call，否则根 | actor 与 trigger 区分谁做的 |
| `memory.entity.changed` | 点 | memory_id, op, topic | 同 | 记忆是唯一可写实体；笔记已只读，不再有 note 事件 |
| `memory.topic.cleaned` | 点 | count | 根（trigger=batch） | 09-25 14:52 那批 8 个 topic 属于此类 |
| `bill.created` | 点 | bill_id, category, amount | 同 doc | |
| `reminder.created` / `reminder.acknowledged` | 点 | reminder_id | 同 doc | 确认提醒同时是 `user.action.confirmed`，两者不同层不是重复 |

**系统变更事件的业务键**：创建类用对象 id（一个对象只会被创建一次，可作 `biz_key`）；更新类不带业务键——同一对象会反复更新，重复上报只能靠 `event_id` 去重。

**不再产生的事件**：`note.updated`（笔记已停写，新记录走记忆）；`theme.updated`（本方案不把 theme 当锚，且它是否彻底存废待定，**暂不实现**，留一行以便将来开）。

## 在场与电源（客户端，基本为点事件，`client.input.*` 是区间配对）

| type | 必备字段 | 父 | 现状 |
|---|---|---|---|
| `client.app.launched` / `client.app.quit` | launch_id | 根 | 现有 `AppDelegate` 可挂钩 |
| `client.focus.gained` / `client.focus.lost` | focus_epoch_id | 根 | 现成（didBecomeActive / didResignActive） |
| `client.idle.started` / `client.idle.ended` | idle_seconds | 根 | 用 `CGEventSourceSecondsSinceLastEventType`，免权限 |
| `client.input.active` / `client.input.ended` | 区间配对；duration_ms, chars_bucket, had_delete | 根 | **要新写（2026-09-26 定要做）**：`NSEvent` 本地监听，不要任何系统权限；只统计本 app 窗口内的输入，只计数不记内容；**停 5 秒算一段结束**，下次输入开新段 |
| `system.sleep` / `system.wake` | wake_epoch_id | 根 | **要新写**（NSWorkspace 通知中心） |
| `system.screens.sleep` / `system.screens.wake` | 无 | 根 | **要新写**；锁屏的近似边界 |
| `system.power.off` | 无 | 根 | **要新写**；**不写 boundary**——悬挂的 wake epoch 靠下次进程启动的 boundary 兜底（关机路径本就可能赶不及落盘） |
| `system.boundary` | 来源（client / server） | 根 | 进程启动、**睡眠**、唤醒时写，用于截断悬挂区间 |

这一组全部只靠 `event_id` 去重（没有业务键）。

## 组件（客户端，L4 的原料）

| type | 形态 | 必备字段 | 父 |
|---|---|---|---|
| `component.opened` / `component.closed` | 点 | component_kind（browser / doc / chat）, component_instance_id | 根 |
| `component.focus.gained` / `component.focus.lost` | 点 | component_instance_id | 根 |

## 读取自记（服务端）

| type | 形态 | 必备字段 | 父 | 说明 |
|---|---|---|---|---|
| `read.query` | 点 | api_name, params_digest, result_count, level, caller | 根 | 记录方是 assistant；`caller` 用来区分是哪一路在取数（本机助手还是将来的其他 agent），A2A 之后要能回答"是谁在取、为哪件事取的"；来源单列一类，**默认不参与"你干了什么"的统计** |

## 资源抽取（服务端归一用，P12）

服务端在写入事件时从 `attrs` 抽资源、归一到规范 id 并落 `resources` / `resource_aliases` / `event_resources`（设计篇 6.1，归一在写入时做）。**只认下表这几格，其余键一律不进资源层**：

| attrs 键 | resource_kind | 归一规则 |
|---|---|---|
| `url_raw` / `url` | `url` | RFC 3986 语法级：小写 host、去默认端口、去 fragment、删跟踪参数（`utm_*` / `fbclid` / `gclid` / `xsec_token` …）与凭证参数、参数按名排序、去末尾斜杠；**仅 http/https** |
| `repo_path` | `repo` | 有 git remote → 归一 remote（ssh/https 同形、去 `.git`、host 与 path 小写）；无 remote → 绝对路径折叠家目录（`~/…`） |
| `file_path`（配 `repo_path`） | `file` | 仓库内 → 仓库锚 + 仓库内相对路径；仓库外 → 绝对路径折叠家目录 |
| `doc_id` | `doc` | 直接用系统内 id |
| `memory_id` | `memory` | 直接用系统内 id |
| `bill_id` | `bill` | 直接用系统内 id |
| `reminder_id` | `reminder` | 直接用系统内 id |

> 会话（conversation）不是资源锚：它本来就是事件的 `conversation_id` 列，不进 `resources`（设计篇 6.1 把它列在「直接用系统内 id」是别名式的说法，实际由列承载）。

## epoch 标记（采集时顺手生成，写进后续事件）

| 标记 | 何时生成 | 用于哪一层 |
|---|---|---|
| `launch_id` | 进程启动 | L1 归属，唯一不需要时间推断的一层 |
| `focus_epoch_id` | 前台激活 | L2 原始区间、L3 去抖的消歧依据 |
| `component_instance_id` | 组件打开 | L4 归属 |
| `wake_epoch_id` | 唤醒 | L0 |
