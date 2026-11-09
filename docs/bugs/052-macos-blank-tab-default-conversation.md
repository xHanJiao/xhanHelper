# 052 macOS 新对话的第一条消息落进「默认对话」，旧会话只显示本轮几条消息

## 问题表现

macOS 上点「新对话」后直接说话/打字，消息没有进入一个新建会话，而是写进了
侧栏那条「默认对话」。再点开「默认对话」时，列表里只有这次刚说的两三条消息
（有的甚至只剩一轮），而同一会话在 PWA 里能看到完整历史。

## 根因

两件事叠在一起：

1. **空白 tab 把 `conversation_id` 发成了 nil。** 后端 `/api/chat` 对
   `conversation_id == nil` 的语义是「老客户端，落默认会话」；只有
   `conversation_id == ""`（空串）才表示「新建一个非默认会话」
   （`server/internal/controller/chat.go` 的 `req.ConversationID == nil` 分支）。
   `e2338d7e`（第三轮复审，「插话落回同一会话」）把发送路径从 `conversationId`
   换成了续传用的 `effectiveConversationID`，而后者对空白 tab 返回 **nil**：

   ```swift
   private var effectiveConversationID: String? {
       if let cid = conversationId, !cid.isEmpty { return cid }
       return pendingConversationID.isEmpty ? nil : pendingConversationID
   }
   ```

   于是 `SSEClient` 的 `if let cid = conversationId` 不再写入 `conversation_id`，
   后端按默认会话处理。它同时破坏了 `init_mode`：`targetConversationID == nil`
   才带，空串被当成「已有会话」。

2. **旧 tab 被当成「已加载」直接复用。** 空白 tab 收到 meta 帧后
   `conversationId` 被改成默认会话 id，但 `messages` 里只有本轮刚追加的几条。
   之后从侧栏点「默认对话」时，`ChatTabManager.openConversation` 命中
   `tabs.first(where: { $0.conversationId == id })`，只 `activate` 不重拉历史，
   所以只看到本轮那几条。PWA 每次都是重新拉 `messages-v2`，看起来是「PWA 有、macOS 没有」。

## 方案

- 新增 `sendConversationID`：空白 tab 返回空串（`""`），有 pending id 时返回 pending，
  其余返回真实 id；发送路径（`sendMessage` / `setupSSE`）改用它。
  续传仍用 `effectiveConversationID`（必须有真实 id）。
- `initMode` 的判定从 `targetConversationID == nil` 改成 `(targetConversationID ?? "").isEmpty`。
- 复用旧 tab 不重拉历史的行为**不做改动**：本条 bug 修好后，空白 tab 不会再认领
  一个已存在的会话，冲突场景自然消失；跨设备同步导致的 stale tab 属另一件事。

影响文件：`client/xHelper/xHelper/Shared/ViewModels/ChatViewModel.swift`。

## 验证

macOS `swiftc -typecheck` 通过。真机待验：

- 点「新对话」→ 说一句话 → 侧栏出现新的「新对话」条目，默认对话不长新消息；
- 从侧栏打开「默认对话」→ 能看到完整历史（不止本轮几条）；
- 空白 tab 第一轮流式未结束时接着说第二句 → 两句落在同一个新会话（插话落回不回归）；
- 计划模式：新对话首条消息仍按当前模式建会话（`init_mode` 带上）。
