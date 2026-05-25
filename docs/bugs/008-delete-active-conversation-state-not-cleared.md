# 008: 删除当前对话后聊天视图未重置

**日期:** 2026-05-25
**状态:** 已修复

## 现象

在某个对话中，从侧边栏删除该对话后，聊天区域仍然显示该对话的消息内容，没有切换到「新对话」空状态。

## 复现步骤

1. 点击「新对话」→ URL 变为 `/chat`
2. 发送一条消息 → 后端创建新对话，SSE `done` 事件设置 `chat.conversationId`，对话出现在侧边栏
3. URL 仍为 `/chat`（没有 `:id` 参数）
4. 从侧边栏删除这个对话
5. 对话从侧边栏消失，但聊天区域的旧消息仍在

## 根因

`ConversationList.vue` 的 `handleDelete` 只通过 `activeId.value === conv.id` 判断是否需要重置聊天视图。

`activeId` 来自 `route.params.id`，而 `chat.conversationId` 可以由 SSE `done` 事件独立设置（不更新 URL）。当 URL 中没有 `:id` 时，`activeId.value` 为 `undefined`，条件永远为 `false`，导致 `chat.reset()` 不会被调用。

**关键代码路径：**

- `ConversationList.vue:117` — 只检查了 `activeId.value === conv.id`
- `useChatSSE.js:186-188` — SSE `done` 事件设置 `chat.conversationId` 但不更新 URL
- `ChatView.vue:359-365` — 路由 watcher 的 `else if (!newId)` 分支负责调 `chat.reset()`，但 URL 未变化时不会触发

## 修复

在 `handleDelete` 中增加对 `chat.conversationId === conv.id` 的判断，直接调用 `chat.reset()` 清除状态。这样无论 URL 中是否有 `:id`，只要删除的是当前活跃对话，都能正确重置。

**修改文件：** `front-end/src/components/ConversationList.vue`

```diff
 async function handleDelete(conv) {
   const res = await convs.remove(conv.id)
   if (res.success) {
     message.success('删除成功')
+    if (chat.conversationId === conv.id) {
+      chat.reset()
+    }
     if (activeId.value === conv.id) {
       router.push('/chat')
     }
   } else {
     message.error(res.message)
   }
 }
```
