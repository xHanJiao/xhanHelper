# 无痕会话（Ephemeral / 阅后即焚）

> 2026-09-14 设计。目标：用户显式创建一种"阅后即焚"的会话，窗口结束或主动删除后，销毁会话及其全部会话域痕迹（消息、向量、摘要、计划、分享），不产出记忆/画像，未来不可恢复。

## 1. 语义

| 维度 | 定义 |
|------|------|
| 创建 | `POST /api/conversations` 传 `ephemeral: true`；默认会话不可无痕；属性**可切换**（见下） |
| 切换 | `PATCH /api/conversations/:id/ephemeral`（body `{"ephemeral":bool}`）；默认会话置 true 返回 `400`；置 true 时补挂 `session_end` 销毁任务 |
| 入口 | 输入栏 `theatermasks` pill（默认收起、右键可常驻；空白会话只改本地意图，首条消息落库时生效）。**仅 macOS**（iOS 无此入口） |
| 存储 | `Conversation.Extra` JSON 的 `ConversationExtra.Ephemeral`（无 migration） |
| 下发 | `GET /conversations/:id/messages-v2` 响应含 `ephemeral` 字段，客户端各加载路径统一据此 seed 状态 |
| 后处理 | 窗口结束（静默 `SessionGapMinutes`，默认 30 分钟）时**跳过全部 worker**（summary/memory/state/profile），改为销毁；**inline `record_memory` 也不写**（请求体隐藏该工具 + 执行层 `EphemeralChecker` 拒写） |
| 手动删除 | `DELETE /api/conversations/:id` 对无痕会话走销毁；普通会话仍软删 |
| 分享 | 无痕会话禁止创建消息分享（`403`） |
| 销毁范围 | 会话域数据：自身 + `parent_id` 子会话的 messages、消息向量、summaries、plans、shares、会话行；**该会话写下的 memories**（按 `source_conv_id`，含 embeddings/chunks） |
| 保留 | documents（用户产物）、notes/bills（工具副作用，无 conv 关联）、llm_cost、llm_call_io；**memories 不产生**（2026-11-18 起，见文末「后续变更」） |
| 恢复 | 无 |

## 2. 两段式销毁

### Stage A — 即时隐藏（同步，毫秒级）
`EphemeralDestroyer.BeginDestroy` → `ConversationRepo.BeginPurge`（单事务）：
1. `ListScopeIDs`（自身 + 子会话，不过滤 is_deleted）
2. `UPDATE conversations SET is_deleted=true`
3. `UPDATE messages SET status=2`
4. `DELETE conversation_summaries / conversation_plans / message_shares`

随后清除内存向量 `VectorStore.DeleteByMetadata("conv_id", id)`；再按 `source_conv_id IN scope`
清掉这些会话写下的记忆（`ListIDsBySourceConvs` → `EntityPurger.PurgeMany` 清派生 → `DeleteIDs` 删行，
先清派生再删行，失败可重跑）；最后入队 `ephemeral_purge` 延迟任务。

触发点（两者代码一致，均只做 Stage A）：
- 后处理：`PostChatWorkerGroup.Process` 检测到无痕会话 → `BeginDestroy`
- 在线：`DELETE /api/conversations/:id` → `BeginDestroy`

### Stage B — 物理清除（后台，分批 + 节流）
`CronRunner.processPurgeTasks` 轮询 `ephemeral_purge` 任务 → `EphemeralDestroyer.RunPurge`：
```
loop:
  ids = SELECT id FROM messages WHERE conversation_id IN scope LIMIT 300
  DELETE embeddings WHERE entity_type='message' AND entity_id IN ids
  DELETE messages WHERE id IN ids
  sleep 150ms   // 避免大字段删除打满磁盘 IO
purge memories by source_conv_id   // Stage A 失败过就在这里重试（删会话行前最后一次机会）
DELETE conversations WHERE id IN scope   // 最后一步
```

**可恢复性**：会话行最后删。中途失败时行仍在（`is_deleted=true` + `Ephemeral=true`），
CronRunner `ScheduleRetry`（≤3 次，2 分钟退避）→ 重入 `RunPurge`，谓词删除幂等续跑。

## 3. 关键文件

| 文件 | 内容 |
|------|------|
| `model/conversation.go` | `ConversationExtra.Ephemeral` |
| `service/conversation_repo.go` | `IsEphemeral` / `ListScopeIDs` / `BeginPurge` / `PurgeMessageBatch` / `FinishPurge` |
| `agent/ephemeral_destroyer.go` | `BeginDestroy`（Stage A + 入队）/ `RunPurge`（Stage B） |
| `agent/post_chat_group.go` | `EphemeralPurger` 接口 + 无痕分支 |
| `agent/cron_runner.go` | `processPurgeTasks` + `HookStatus.TaskQueues` |
| `agent/vector_store.go` | `DeleteByMetadata` |
| `controller/conversations.go` | create 参数/响应、list 响应、delete 销毁 |
| `controller/chat.go` | 指定会话不存在 → 404（不静默新建） |
| `controller/message_share.go` | 无痕会话禁止分享 |
| `service/delayed_task_repo.go` | `StatsByType`（按类型聚合 pending） |
| `client/.../SSEClient.swift` | HTTP 状态码检查（非 2xx 上报） |
| `client/.../ChatViewModel.swift` | `onConversationGone` + 404 探测 |
| `client/.../ChatTabManager.swift` | 关闭 tab + `.conversationRemoved` / `.conversationsGone` 通知 |
| `client/.../ConversationListViewModel.swift` | `removeLocal`（保留分页）+ `startAutoRefresh`（前台 60s） |
| `client/.../ContentView.swift` | 激活时刷新列表 + 回调接线 |

## 4. 客户端同步（macOS / iOS）

无推送通道，客户端靠四条路径感知服务端销毁：

1. **HTTP 404 探测**：`SSEClient` 检查 `HTTPURLResponse.statusCode`，非 2xx 经
   `onError(NSError(domain:"XHANHTTP", code:))` 上报；`ChatViewModel` 对 404 调
   `onConversationGone(cid)`。`loadConversation`（messages-v2 404）与 `sendMessage`（chat 404）都覆盖。
2. **关闭与清理**：`ChatTabManager.handleConversationGone` 关闭 tab（复用自动切换）、
   发 `.conversationRemoved` 通知 → `ConversationListViewModel.removeLocal`（保留分页）。**静默**，不弹提示（D10）。
3. **激活刷新**：`ContentView` 回到前台（scenePhase / didBecomeActive）时
   `conversationListVM.reload()`，清除会话下拉中已销毁的幽灵条目（当前 tab 由 `syncActiveChat` 经路径 1/2 自愈）。
4. **前台周期刷新**：应用保持激活时 `ConversationListViewModel.startAutoRefresh()`
   每 60s `reload()` 一次（`stopAutoRefresh` 在离开激活态时停），覆盖"一直不切 app"的场景。
   每次 `load()` 会用「旧列表中的无痕会话 − 新首页」算出已销毁的 id，发 `.conversationsGone`；
   `ChatTabManager` 订阅后关闭仍开着的对应 tab（消息列表随之联动），已被 404 路径关掉的静默跳过。

修复的既有 bug：`SSEClient` 原先完全不看 HTTP 状态码，非 2xx（含 404）被静默吞掉，
导致 `isStreaming` 永久卡住。

## 5. 监控耦合

- `/api/admin/hooks` 的 `HookStatus.TaskQueues` 按 `task_type` 聚合 pending 任务，
  `ephemeral_purge` 自动出现，未来新任务类型无需改代码/加字段。
- 成本看板、I/O 审计不变（记录保留）。

## 6. 边界

- 空无痕会话（建了没发消息）：无 `session_end` 任务，不会自动销毁，只能手动删。
- `DisableBackgroundTasks=true`：自动销毁不触发（`OnChatEnd` 直接 return），仅手动删。
- Stage A→B 窗口内进程重启：warmup 可能从 DB 重载已 `status=2` 消息的 embedding（秒级窗口，低概率）。
- `MarkFailed` 后：会话已隐藏但未物理删除，会打 error 日志。

## 7. 测试

- `service/conversation_repo_test.go`：IsEphemeral / BeginPurge（含子会话、无关会话隔离）/ PurgeMessageBatch 分批 / FinishPurge
- `agent/ephemeral_destroyer_test.go`：BeginDestroy（入队、去重、清向量、幂等）/ 按 `source_conv_id` 删本会话记忆与派生（其他会话、无出处记忆不误删）/ RunPurge（分批、硬删、幂等）
- `agent/post_chat_group_test.go`：无痕会话跳过 workers 只调 BeginDestroy
- `tool/record_memory_test.go`：无痕拒写（runner 不被调用）/ 正常写入 / checker 报错 fail-closed / 空 convID 放行 / nil checker panic
- `agent/orchestrator_ephemeral_test.go`：无痕请求体不下发 `record_memory`、`expose_tools` 拒绝并把它移出 available，其他工具不受连坐
- `agent/cron_session_end_test.go`：processPurgeTasks 成功 MarkDone / 失败 ScheduleRetry

## 8. 后续变更

- **2026-11-18：堵住 inline 记忆泄漏。** 原文第 1 节把 memories 列进「保留（工具副作用，无 conv 关联）」，
  实现上也就真的没拦：`MemoryWorker`（post_chat）有无痕判断，但主循环的 `record_memory` 是聊天中
  实时调用的，没有任何无痕检查，`BeginPurge` 也不认识 `user_memories`（该表无 conv 关联）。
  线上证据：11-16 晚、11-17 下午两个无痕会话的消息已物理销毁，但同期 **9 条记忆留在库里**
  （含家庭隐私），且部分已挂进事件（会随事件注入后续对话）。
  修法（两道闸）：
  - 请求层：`RunRequest.Ephemeral` → 本轮隐藏 `record_memory`（请求体 `tools` 与
    `expose_tools` 的 `available` 都不再出现），模型不会白试。
  - 执行层：`RecordMemoryTool` 构造时必须注入 `EphemeralChecker`（`repos.Conversation.IsEphemeral`），
    无痕或**查询报错**时拒写（fail-closed）；`convID` 为空（replay/eval）或会话行不存在时放行
    （后者没有 chat 入口）。
  存量：9 条已按「落在 purge 窗口 + facts 在 messages 里无出处」逐条核对后删除（含 derivations）。
  已知残留：system prompt 的工具目录不过滤 `record_memory`（只影响一轮 token，不影响正确性）。

- **2026-11-18（同日第二刀）：补上「普通会话事后转无痕」的缺口。** 上一刀只挡住「无痕会话里
  **新建**记忆」，但用户可以先在普通会话聊出几条记忆、再把会话切成无痕：记忆在切换前就落库了，
  请求层隐藏与执行层拒写都管不到，销毁时也没人认得它是哪来的（`user_memories` 无 conv 关联）。
  修法（出处 + 按出处清理）：
  - `UserMemory.SourceConvID`（新列，AutoMigrate 自动加）由决策循环写入：`Decide` 带 convID，
    `newMemory` 给新建行记下会话 id；**`update` 合并到别人的行时不改出处**（那是原会话的记忆）。
  - `EphemeralDestroyer.BeginDestroy` 在拿到 scope（本会话 + `parent_id` 子会话）后按
    `source_conv_id IN scope` 清记忆：`ListIDsBySourceConvs` → `EntityPurger.PurgeMany`（清派生）→
    `DeleteIDs`（删行）。Stage A 失败时 Stage B `RunPurge` 会在 `FinishPurge` 删会话行**之前**重试
    （会话行一删，出处键就没了；cron 失败还会 `ScheduleRetry` 最多 3 次），全部失败才永久残留（有日志）。
    只在销毁时清，不在切换瞬间清：用户把开关点回来就还能反悔，记忆不丢。
  - 清理顺序是「先清派生、再删行」：反过来失败会留下再也定位不到的 chunk 原文与向量行。
  残留：① 存量行没有出处，只能 forward-only；② 这次对话若是「合并进已有记忆」（决策循环 action=update），
  那条行仍属原会话，销毁本会话不会删它（内容里可能含本会话的片段）；③ csv 导入与 replay/eval 不写出处；
  ④ 记忆已挂进事件时，事件的 `Summary` 仍会被注入（`retrieval/event_source.go` 无条件注入），
  本方案不重写事件摘要——销毁后这条通道仍可能带出隐私片段。

