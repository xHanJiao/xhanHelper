# 会话接力：整理为文档并新开会话续接

> 目标：用户在会话 A 点一下按钮，Agent 把 A 的讨论整理成一篇结构化「接力文档」，客户端捕获文档后自动新建会话 B，并把文档标题/id 作为首条消息发给 B，由 B 接着做。整理过程本身（那轮 user 提示词 + LLM 回复）不应进入对话记录，也不应带给后续对话。

## 0. 背景与动机

早期方案（已废弃，见文末「未采用」）是「压缩上下文窗口」：引入 `SessionStartMsgID` + 压缩记录表，让同一会话丢弃旧消息。实现面 ~14 个文件、要改检索链路、且丢消息不可逆。

实际诉求是**基于结论继续讨论**，而不是「删掉旧消息」。于是改为更轻的形态：把结论落成一篇文档（现成的 `document` 工具 + 向量检索能力），再开一个新会话从文档出发。旧会话完整保留，不删任何消息。

## 1. 用户流程

```
会话 A ──点「接力」──▶ A 里 LLM 调 document.create_document 生成带「接力」tag 的文档
                                   │
                                   ▼（tool_result 回传 data.id / data.title）
                       客户端捕获 → 流结束(.done) → ChatTabManager.startHandoffConversation
                                   │
                                   ▼
                       新建会话 B，首条消息 = 【接力会话】…《{title}》(id={id})… → B 调 get_document 读取后继续
```

> ⚠️ **2026-10-16**：输入栏的「接力」按钮已下架，下面这条流程暂时没有触发入口（链路代码一行未删），见文末「后续变更」。

## 2. 关键设计

### 2.1 文档产出：复用 document 工具，不改工具协议

- `create_document` 已支持 `title/content/tags`。接力文档的正文结构由客户端注入的提示词规定（见 2.2），**不改工具 schema**。
- 统一 tag：`接力`。写进提示词强制（`tags` 必须包含「接力」），用于 macOS 文档页的标签词云（`DocumentListView` / `ListTagCounts`，`minTagCount` 从 1 起，单篇即会显示）。
- `document.go` 的 `createDocument` 增加来源会话可追溯：`if ec.ConvID != "" { doc.ConversationID = &convID }`。
- 客户端**不校验** tag 是否写入（纯靠提示词；无文档 tag 修改接口，不做额外兜底）。

### 2.2 提示词：借 opencode compaction 的 anchored summary 模板

客户端常量 `ChatViewModel.handoffPrompt`，把 opencode 的 `SUMMARY_TEMPLATE` 映射到本项目：

| opencode | 接力文档 |
|---|---|
| Objective | 目标 |
| Important Details | 重要细节 |
| Work State{Completed/Active/Blocked} | 工作状态{已完成/进行中/阻塞} |
| Next Move | 下一步 |
| Relevant Files | 相关文档 / 笔记 |

规则同样照搬：每节都保留（空填「(无)」）、短横线要点不写散文、保留精确路径/符号/命令/错误串/URL/ID、不提「整理/摘要」这个动作本身。另加：只做整理不要继续讨论，创建完只回复文档标题。

新会话首条消息由 `ChatViewModel.handoffTrigger(title:id:)` 生成，要求 B 先 `document.get_document` 读取再继续。

### 2.3 客户端捕获：扩展解析器 + FIFO 配对

- `tool_call` SSE 载荷含 `tool`/`function`/`args`/`display`（`orchestrator.go` 发事件，`tool.Registry.FillDisplay` 填 `display`）；`tool_result` 载荷是 `tool.Result` 的 JSON（`{success,data,error}`）。
- 原 `parseToolCall`/`parseToolResult` 只取 `display`/`success`/`error`，**丢掉了 `data`**。扩展为：
  - `ParsedToolCall` 增加 `tool`/`function`；`pendingToolDisplays: [String]` 改为 `pendingToolCalls: [PendingToolCall]`。
  - `ParsedToolResult` 增加 `data: [String: Any]?`。
- `tool_result` 到达时按 FIFO 配对（结果按调用顺序发出，可靠）：若 `handoffActive && tool=="document" && function=="create_document"` 且成功，记录 `capturedHandoffDoc = (title,id)`。
- `.done` 末尾：`handoffActive` 时若捕获到文档 → 回调 `onHandoffReady(title,id,baseURL,token)`；否则 Toast「未检测到新文档，已取消接力」。
- `stopStreaming()` / `resetForNewChat()` 复位 `handoffActive` / `capturedHandoffDoc`。
- `ChatTabManager.startHandoffConversation` 调 `newChat()` 后取 `currentTab.viewModel.sendMessage(trigger)`，UI 自动切到 B。新会话不继承 Agent（`ChatViewModel` 不持有 agentId），走默认 work 模式。

### 2.4 隐藏轮：整轮消息不进对话记录

整理这一轮（A 里的 user 提示词 + LLM 回复 + 工具结果 + 校验消息）不应污染 A 的对话，也不应带给后续对话。做法是给 `messages` 加一个**状态值**：

```go
// model/message.go
MsgStatusNormal  = 1 // 正常
MsgStatusDeleted = 2 // 已删除
MsgStatusHidden  = 3 // 隐藏：接力等控制轮
```

**为什么复用 `Status` 而不是新增 bool 字段**：项目里**所有**消息读取路径都带 `status = MsgStatusNormal`（历史列表、游标分页、轮次组装、上下文窗口、检索、搜索、分享、后处理）。置 `3` 即「一处标记、处处隔离」，零查询改动、零迁移；新增字段则要在 ~25 个查询点逐个加过滤，漏一个就泄漏。已确认全库没有对 messages 的 `status != 2` / `status IN (...)` 查询，加 `3` 不误伤。

**为什么不用 `is_internal`**：它只在零散位置过滤（`ListByConversation` 并不过滤它），user-role 的 internal 消息会照常出现在历史里，不具备「整条不可见」语义。

链路：

- `/api/chat` 请求体新增 `hidden_round bool`（`controller/chat.go`）。
- `RunRequest.HiddenRound` → `mkUserMsg` / `saveAssistantMsgWithFlags` / `saveToolMsg` / `saveVerifyFeedback` / `collectToolResults*` 落库时统一 `Status = msgStatusFor(hidden)`。
- 隐藏轮额外跳过：用户消息向量（`saveUserMsgEmbedding`，避免提示词污染历史检索）、`onChatEnd`（后处理无内容可做）、`mergeTitle`（避免用接力提示词生成会话标题）。
- `MessageRepo.existsBefore/existsAfter` 补 `status` 过滤：它们是游标 `hasMore` 的边界判断，是唯一不带 status 的读取点。

**自动隔离清单**（无需改动的路径，均为 `status=1` 过滤）：

| 路径 | 承载方法 |
|---|---|
| 上下文窗口 / 历史重放 | `ListRecentRounds`、`loadUserMessagesBefore`、`computeWindowSize` |
| 会话历史浏览 / 分页 | `ListByConversation*`、`ListByConversationCursor*` |
| 检索 / 搜索 | `GetByID(Cols)`、`GetByIDs`、`buildMessageFilterQuery`、`history_source` |
| 后处理 | `post_chat_group.Process`（锚点 `GetByID` 直接查不到 → 空转）、`ListUserMessagesBetween`、`ListActiveSince`、`IsActiveSince`、用户统计 |
| 分享 / 反思 | `message_share_repo`、`reflection.go` |

## 3. 已知边界

**整理的是「当前窗口」，不是整段会话。** 接力轮走普通 `/api/chat`，历史量由 `controller/chat.go` 的 `computeWindowSize` 决定：

- 距上一条 user 消息 < `session_gap_minutes`（生产 15 / dev 1 / test 3）：加载最近 `window` 轮（逐轮 +1 扩张，封顶 `max_window_rounds`，生产配 800）→ 约等于整个 session 窗口。
- 间隔 ≥ 阈值（`IsNewSession`）：`SkipHistory=true` 不重放历史，仅把最近 `recent_user_msgs`（3）轮注入资源区。
- 窗口之外依赖 system prompt 稳定区的画像 + 最多 `summary_attach_count`（1）条、`summary_attach_max_days`（3）天内的摘要。

即：连续使用、会话不长时基本等于全量；**停顿超过阈值再点接力，文档可能只覆盖最近 3 轮**。当前**有意不处理**（方案见 5.2）。

其它边界：

- 隐藏轮的存在不影响下一轮窗口：`computeWindowSize` 读的是上一条**可见** user 消息。
- 会话删除级联仍会清理隐藏消息（`conversation_repo` 按 `conversation_id` 批量置删除，不按 status 过滤）。
- 调试页 / `message-traces` 也看不到隐藏轮（同 `status` 过滤）。
- 客户端在流式期间照常展示这一轮，直到下次重载会话时自然消失（服务端已过滤）。

## 4. 改动清单

**后端**

| 文件 | 改动 |
|---|---|
| `internal/model/message.go` | `MsgStatusHidden = 3` |
| `internal/controller/chat.go` | 请求体 `hidden_round`；`RunRequest.HiddenRound`；`mergeTitle` 加 `hidden` 参数并早退 |
| `internal/agent/orchestrator.go` | `RunRequest.HiddenRound`；`msgStatusFor`；`mkUserMsg`/`saveAssistantMsg*`/`saveToolMsg`/`saveVerifyFeedback`/`collectToolResults*` 落隐藏状态；隐藏轮跳过 embedding / onChatEnd |
| `internal/service/message_repo.go` | `existsBefore`/`existsAfter` 补 status 过滤 |
| `internal/tool/document.go` | `createDocument` 写 `ConversationID` |

**客户端（macOS）**

| 文件 | 改动 |
|---|---|
| `Shared/ViewModels/ChatViewModel.swift` | `beginHandoff` + `handoffPrompt`/`handoffTrigger` + `handoffActive`/`capturedHandoffDoc`/`onHandoffReady`；解析器扩展 + `tool_result` 捕获；`.done` 回调；`sendMessage`/`setupSSE` 透传 `hiddenRound` |
| `Shared/Views/InputBarButtons.swift` | `HandoffPill`（照 `ModelPill`） |
| `Shared/Views/ChatView.swift` | 胶囊组挂载按钮；`canHandoff = !isStreaming && 非空会话 && !isPlanMode` |
| `Shared/ViewModels/ChatTabManager.swift` | `startHandoffConversation` + `onHandoffReady` 接线 |
| `Shared/Services/SSEClient.swift` | `connect(hiddenRound:)` → body `hidden_round` |

## 5. 测试与验证

- `internal/agent/hidden_round_test.go`
  - 隐藏轮 user/assistant/tool 三类消息落库 `status=3`；`ListByConversation`/`ListRecentRounds` 不可见；`GetByID` 查不到。
  - `post_chat_group.Process` 对隐藏锚点空转，worker 不被调用。
- `internal/service/message_repo_test.go`：`existsBefore/After` 忽略隐藏消息。
- 验收：`go build ./...` + `go vet ./...` + `go test ./...` 全绿；客户端 `swiftc -parse`；真机由用户在 Xcode 验（CLAUDE.md 约定）。
- 手工路径：非空会话点接力 → 看到文档生成 → 自动切到新会话且首条为 trigger → 刷新会话 A 该轮消失 → 文档页词云出现「接力」并可筛出。

## 6. 未采用 / 后续

### 5.1 未采用：窗口压缩（原 `context-window-compaction.md`）

在会话内引入压缩记录 + `SessionStartMsgID` 下界，丢弃/摘要旧消息。实现面大（~14 文件）、要改检索排除逻辑、丢消息不可逆。接力文档方案以「落文档 + 开新会话」达成同一目的，且不删历史。该设计文档已删除。

### 5.2 后续可选：接力轮全量会话

若要消除「停顿后只剩最近 3 轮」的边界，可让接力轮走全量路径：

- 用现有 `hidden_round`（或新增 `full_history`）标志，controller 跳过 `computeWindowSize`；
- 批量取该会话**全部**轮次（新增一次性查询的 repo 方法，遵守「禁止循环内逐个查库」），`ReduceRecentRounds` 压成「user 原文 + 每轮最终 assistant 回复」的紧凑转录注入资源区；
- 加 `handoff_max_rounds`（建议 300）兜底，超出靠摘要。

当前未做，保持窗口语义。

## 后续变更

### 2026-10-16 · 接力入口下架（用户拍板）

- `HandoffPill` 的 `visibility` 改为 `.hidden`（`Shared/Views/InputBarButtons.swift`），输入栏不再有接力按钮。
- 本文描述的全链路（`beginHandoff` → 隐藏轮 → `onHandoffReady` → `ChatTabManager.startHandoffConversation`）**代码全部保留、零改动**，只是没有触发入口了；恢复只需把 `visibility` 改回 `.always`。
- 服务端零改动（`hidden_round` / `MsgStatusHidden` / 文档 tag「接力」都还在，词云筛选照样有效）。
