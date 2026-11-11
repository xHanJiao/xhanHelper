# 聊天 · 流式中排队发送

> 状态：已实现（待真机验收）
> 分支：`feat/chat-prompt-queue`
> 预览：`mockup.html`（本目录，单文件自包含，带 A/B/C 形态切换）

## 估算（定稿）

- 2 面（Vue 网页端）/ Swift 共享客户端（macOS + iOS 同一套 `Shared/`）
- 1 缝（网页与 Swift 的队列时序、停止/错误语义必须一致）
- 0 次真机往返（agent 不跑 `xcodebuild`，真机构建由用户在 Xcode 复核）
- 2 个待拍决定（失败暂停的恢复入口是否要更显眼；队列项是否支持点击回填编辑）
- 约 5–8 小时

## 背景

当前发送被前后端两道门卡住：

1. 网页端 `useChatSSE.js` 的 `sendMessage` 开头 `if (chat.isStreaming) return`，`ChatView` 的输入框在流式中 `:disabled`，发送键变成停止键；Swift 端 `InputBar` 同样禁用编辑器并在 Return 时抖动提示。
2. 后端同一会话只允许一个 ReAct 循环（`controller/chat_stop.go` 的 `Register` 会取消旧循环），这是**必须保留**的约束：两个循环交叉写库会产生 `assistant(tool_calls) → user → tool` 错位，整段会话作废。

opencode 等工具的做法不是并行跑多轮，而是**客户端排队**：流式中 Enter 只入队，当前轮结束再按序发。可选的中途注入（steer）不在本方案范围内。

## 目标

1. 流式中输入框保持可用，Enter / 发送键 = 入队；消息以虚线气泡出现在当前回复下方（不写进正式消息数组）。
2. 当前轮 `done` 后自动发队首，复用原有发送路径（本地气泡、附件、懒建会话、SSE 全部不变）。
3. 点「停止」= 停当前轮 + 清空队列。
4. 切会话 / 新建会话 = 先清队列再取消旧流，清空时提示「已清空 N 条排队消息」。
5. 发送失败 = 暂停队列，队列区显示「已暂停 · 继续」，点继续或下次手动发送时恢复。
6. 语音轮不进队列（保持现有「先停上一轮再发」语义）。

## 非目标

- 不做中途 steer / 把消息注入正在跑的 ReAct 循环。
- 不做队列持久化：刷新页面、退出 App 即丢（与 opencode TUI 口径一致）。
- 不做多会话并行（切走的会话后台继续跑是另一个维度，涉及全局单流状态重构）。
- 不做队列项点击回填编辑，只做删除。

## 数据与渲染的关键约束

**排队气泡不能写进 `chat.messages`。** `useChatSSE.js` 的 `done` 处理用 `findLast(m => m.role === 'user')` 回填真实 `user_message_id`；若排队消息也在数组里，id 会补到最后一条排队消息上。因此：

- 队列是独立数组（网页 `chat.pendingSends` / Swift `ChatViewModel.pendingSends`）。
- 队列渲染在消息列表的同一个滚动区里，紧跟流式回复气泡之后，视觉上是「对话流的一部分」。
- 出队时才把消息交给原发送路径，由原路径写入正式消息数组。

## 行为定义

| 时机 | 行为 |
|------|------|
| 流式中发送 | 入队；清空输入框与草稿图；滚到底部 |
| 当前轮 `done` 且无错误 | 自动出队首发送 |
| 当前轮 `done` 但带 error / SSE error | 队列暂停，保留尚未发送的排队项 |
| 点停止 | 清空队列 + 停当前轮 |
| 切会话 / 新建 | 清空队列（先清再 cancel），toast 提示 |
| 发送失败（含排队项图片全失败） | 暂停；失败的那条已作为普通消息留在对话里，其余排队项留在队列，可继续重试或删除 |
| 队列非空时手动发送 | 追加到队尾并恢复发送，保持先来先发 |
| 语音轮 | 不排队，沿用现有「停上一轮再发」；排队中的文本消息保留，等语音轮 `done` 后继续 |
| 页面刷新 / App 重启 | 队列丢失 |

## 网页端实现

| 文件 | 改动 |
|------|------|
| `front-end/src/stores/chat.js` | `pendingSends` / `queuePaused` + 入队、出队、删除、清空；`reset()` 清空 |
| `front-end/src/views/ChatView.vue` | `handleSend` 拆分「收集草稿 → 入队 / 直发」；队列渲染在 `chat.streamingMessage` 之后；`handleStop`、路由 watcher、卸载时清队列；输入框解禁；流式中同时显示停止与发送；队列区删除 / 继续 |
| `front-end/src/utils/promptQueue.js` | 纯函数 `shouldDrainQueue`（是否自动出队）供 watcher 与单测共用 |
| `front-end/src/composables/useChatSSE.js` | 不改；`if (chat.isStreaming) return` 保留为最后防线 |

发送顺序由 `drainQueue()` 保证：`while (!isStreaming && !paused && queue.length) { await dispatch(shift()) }`。`sendMessage` 会等待整轮结束，因此循环天然按轮推进。

## Swift 端实现

| 文件 | 改动 |
|------|------|
| `Shared/ViewModels/ChatViewModel.swift` | 新增 `PendingSend` 与 `pendingSends` / `pendingPaused`；把原 `ChatView.sendDraft` 的上传+发送逻辑下沉为 VM 方法；`.done` 出队、`.error` 暂停；`stopStreaming` / `reset` / `loadConversation` 清空 |
| `Shared/Views/MessageListView.swift` | 新增队列参数与回调；在倒序列表底部（源码序 `ForEach` 之前）渲染分隔线与虚线气泡，整组做 `scaleEffect(y: -1)` |
| `Shared/Views/ChatView.swift` | `sendDraft` 改为调用 VM；把队列与回调传给 `MessageListView` |
| `Shared/Views/InputBar.swift` | 去掉编辑器与 iOS 输入框的 `isStreaming` 禁用；Return 不再抖动拒绝，改为排队 |
| `Shared/Views/InputBarButtons.swift` | `SendStopButton` 流式中且可发送时同时给「停止 + 发送」 |

Swift 的队列出队挂在 `.done` 事件末尾（不是 `finishStreaming`，后者也被停止/错误复用），错误暂停挂在 `.error` 与 `done` 带 error 的分支。

## 错误与语音时序

网页端：
- 语音轮提交前先挂起自动出队（`suppressQueueDrain`），等语音轮 `done` 再恢复，避免 `stopLocalStream()` 把 `isStreaming` 置 false 时 watcher 抢发队列，导致语音消息被 `sendMessage` 的 `isStreaming` 闸门丢掉。

Swift 端：
- `.done` 出队；`.error`、`done` 带 error、`handleStreamEnded`（idle/reset/换轮）一律暂停。
- 语音 / 全局热键的「立刻接一轮」传 `clearPending: false`，保留排队；真实停止（按钮、关 tab、reset）才清空。
- 出队上传期间条目仍留在 `pendingSends`：用户删除后不再发送；上传全失败时放回队首并暂停。

## 边界

- 新会话首轮流式中排队：`done` 后 `conversationId` 已由 meta/done 写入，出队时天然落到同一会话。
- 挂起（切后台）期间 `isStreaming` 保持 true，不会误出队；回前台续传后再按正常 `done` 出队。
- 停止先清队列：避免 `isStreaming → false` 的 watcher 把队列发出去。
- 网页切会话时 `cancel()` 会把 `isStreaming` 置 false；必须在 `cancel()` 之前清队列，且 watcher 以「队列为空」为第一道闸。
- 排队项图片已上传，删除后由服务端 orphan TTL 清理；Swift 端排队项图片在出队时才上传，失败则回队首并暂停。

## 验证

网页端：

```bash
cd front-end
npm run test
npm run build
```

手动：连排 2 条 → 按序发出；停止 → 队列清空；切会话 → 提示且只剩目标会话；制造失败 → 队列暂停，点继续恢复；新会话首轮排队；带图排队；刷新后队列消失。

Swift 端：

```bash
cd client/xHelper/xHelper/Shared
xcrun swiftc -typecheck -sdk "$(xcrun --show-sdk-path --sdk macosx)" \
  -target arm64-apple-macos26.3 -default-isolation MainActor \
  -I <DerivedData>/Build/Products/Debug $(cat /tmp/shared.txt) ../../xHelperMac/*.swift
# iOS 同法，sdk 换 iphonesimulator
```

真机构建与交互验收由用户在 Xcode 完成。

## 后续变更

- 2026-11-11：方案定稿。队列形态选 B（消息列表虚线气泡）；停止清空；切会话清空并提示；范围含网页 + Swift。
- 2026-11-11：实现落地。补充语音轮时序（网页 `suppressQueueDrain` / Swift `clearPending:false`）、错误/断网暂停与恢复、iOS 下 `ChatView` 的 macOS-only 声明加 `#if`。
