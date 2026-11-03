# 点提醒通知会跳进默认会话

## 问题表现

macOS 客户端点系统提醒通知 → 提醒被确认，同时中栏**跳到默认会话**（一个跟这条提醒毫无关系的长对话）。

同一个「默认会话」概念在别处也还在渗：Web 前端每次打开 `/chat`（没有会话 id）都会被重定向到默认会话，而不是空白新对话。

默认会话这个概念已经在淡出：macOS 启动改为开空白新对话（`ContentView.swift` 的 `.task { chatTabManager.newChat() }`），会话列表里也早已不再把它当特殊项提升。剩下的这几处是遗忘的尾巴。

## 根因

不是客户端单点问题，是**服务端 ack 的落地策略**就写死在默认会话上：

1. `POST /api/reminders/:id/ack` 里 `FindDefaultByUser` → 没有就现建一个 `IsDefault: true` 的会话 → 把「触发消息」插进去（`controller/reminders.go`）。
2. 客户端 ack 成功后按同一逻辑跳 `deviceManager.defaultConversation`（`ContentView.swift` 的 `handleReminderTap`）。
3. Web 前端 `ChatView.vue` 的「无会话 id」分支先 `GET /api/conversations/default` 再 `router.replace`。

`Reminder` 当时没有记录来源会话，服务端除了默认会话没有别的地方可写，所以整套都围着它转。

## 最终方案

**提醒记住自己是在哪段对话里创建的，点通知就回到那段对话。**

- `model.Reminder` 新增 `ConversationID`（创建时取 `tool.ExecuteContext.ConvID`；秘书 / 反思自动生成的提醒没有会话，为空）。
- ack：
  - 来源会话仍存在、属于本人、未删 → 触发消息写进那段对话，响应带 `conversation_id` + `conversation_title`；
  - 来源为空 / 会话已删 → **只置已确认，不写消息、不再现建默认会话**，响应两个字段为空串；
  - 幂等分支（已 ack 过）同样带上会话字段，重复点通知照样能跳。
- 客户端：`conversation_id` 非空就打开它；为空就不跳，`ToastCenter` 弹触发文案（`Models/Reminder.swift` 的 `AckReminderResponse` 加两个字段）。
- Web：删掉默认会话跳转，直接 `chat.reset()` 显示空白欢迎页；首条消息由 SSE 回传的 `conversation_id` 懒创建（`useChatSSE.js` 已支持）。
  - **必须发空串** `conversation_id: ''`，不能是 `undefined`：`JSON.stringify` 会丢掉 `undefined` 的键，服务端 `chat.go` 判 `nil` 就落默认会话、判 `""` 才建新会话。这一条不做等于没修。
- iOS：当前会话被删的兜底从「打开默认会话」改成 `resetForNewChat()`（并在 reset 里把 `mode` 复位成 work，否则删掉 plan 会话后会停在只读）。

**不选**：继续用默认会话当收件箱（概念已在淡出）；为没有来源的提醒新建一个会话（点一次通知就多一个会话，列表会脏）。

## 核心改动

| 文件 | 改动 |
|------|------|
| `server/internal/model/reminder.go` | 新增 `ConversationID` |
| `server/internal/tool/reminder.go` | 创建时写入 `ec.ConvID` |
| `server/internal/controller/reminders.go` | ack 改写来源会话 + 响应加会话字段 + `ackPayload` / `ackTargetConversation` |
| `server/internal/controller/reminders_ack_test.go` | 新增：① 写回来源会话 ② 无来源不建会话不写消息 |
| `client/.../Models/Reminder.swift` | `AckReminderResponse` 加 `conversationId` / `conversationTitle` |
| `client/.../Views/ContentView.swift` | `handleReminderTap` 改跳来源会话；iOS `onConversationGone` 改回空白新对话 |
| `front-end/src/views/ChatView.vue` | 无会话 id 分支不再跳默认会话 |

## 验证

- `go build ./... && go test ./internal/controller/ -run TestAckReminder`：两条新用例绿。
- `npx vite build` 通过。
- `swiftc -typecheck`（macOS 26.3 / iOS 26.0 各一遍）0 error。
- 真机：Xcode 构建后点一次真实提醒通知 → 应跳到设这条提醒的那段对话；秘书自动生成的提醒 → 只弹文案、不跳。

## 已知边界

- 老提醒（本改动之前创建）`conversation_id` 为空，点通知只 ack + 弹文案，跳不过去。
- 来源对话若已删，同样只 ack + 弹文案。
- `reminders` 表多一列靠 AutoMigrate；云端库要等下次部署启动才生效。
- iOS 启动仍会打开默认会话（`ContentView` 的 `.task(id: defaultConversation?.id)`），本次没动。

## 后续变更

- **2026-11-03**：Web 前端 `/chat` 恢复「启动定位」，但**不再走默认会话**——冷启动首次进来取会话列表第一条（置顶优先，其次 `updated_at` 倒序），`router.replace` 到那条并加载最近消息；顶栏「新对话」仍是空白新对话（模块级 `coldStartPending` 只消费一次）。同时底栏「对话」tab 与列表页返回按钮改为回上次看的会话（`chat.conversationId`），不再清空。影响文件：`front-end/src/views/ChatView.vue`、`front-end/src/components/BottomTabs.vue`、`front-end/src/components/ConversationSwitcher.vue`、`front-end/src/composables/useGoBack.js`。
