# 046 — PWA 会话列表不刷新：新建的会话看不到

- **状态**：已解决
- **发现日期**：2026-11-06
- **影响范围**：`front-end/src/components/PageTopBar.vue`、`front-end/src/components/ConversationSwitcher.vue`、`front-end/src/stores/conversations.js`、`front-end/src/composables/useChatSSE.js`、`front-end/src/utils/sse.js`、`front-end/src/views/AgentView.vue`

## 表现

移动端（iPhone 主屏 PWA，不杀进程）里：

1. 顶栏 `＋` 新建对话 → 说了几句话 → 打开会话下拉，**没有这条**。
2. 在别的客户端（mac）当天新建的会话，PWA 里也**一直不出现**；杀掉 PWA 重开才出现。

列表停在某一天（截图里最新一条是前一天 11/05），不是数据丢了——服务端和 mac 端都正常。

## 根因

三个互相独立的缺陷叠在一起：

1. **列表只在内存里增量维护，从不以服务端为准刷新。** `ConversationSwitcher.refresh()` 原来只在 `convs.list` 为空时 `fetchList()`；PWA 进程被 iOS 挂起而不销毁，可以活好几天，于是列表就是冷启动那天的快照。其它端新建的会话永远不会出现。
2. **新会话的 id 只从 `done` 事件来。** 顶栏 `＋` 原来只 `router.push('/chat')`，会话由首条消息懒创建，`conversationId` 要等 SSE `done` 才拿到。但流式中切底部 Tab / 锁屏 / 按「停止」都会 `cancel()` 断流（服务端 `runCtx` 独立于连接，照样落库），`done` 永远不到 → 前端不知道这条会话存在：列表没有、URL 停在 `/chat`、再发一句还会**再新建一个会话**。
3. **没有空闲兜底。** 移动端 PWA 被挂起后可能留下半开连接（`reader.read()` 既不返回也不报错），`chat.isStreaming` 会永远为真，回前台既不同步历史也发不出消息。

## 修复

- `stores/conversations.js` 新增 `create(agentId)`：`POST /api/conversations` → 立即 `prepend`（带 `mode: 'work'` / `ephemeral`，标题空时补「新对话」）。新建逻辑收口到这里，顶栏＋/下拉「新对话」/智能体「开始对话」共用。
- `PageTopBar.vue` 的 `＋` 改成先建会话、再 `push('/chat/:id')`，id 在发消息前就确定；失败不跳转（避免「看着像新建了、其实没有」）。三个入口都不再先 `chat.reset()`：路由分支 A 会先 `cancel()` 旧流再 `setConversation`，顺序反了会让在途的 `done` 把 `conversationId` 写回旧会话。
- `ConversationSwitcher.refresh()` 改为每次打开面板都 `fetchList()`（仅 `convs.loading` 时跳过）：其它端新建、断流漏掉的条目都在开列表时补回来。
- 分页游标从 `list.length` 换成 store 里的 `loadedCount`（服务端已加载条数）：本地 `prepend` 的条目（新建会话、补回当前会话）不再让「加载更多」跳条；追加时按 id 去重。重拉第一页后若当前会话不在结果里（深链、翻过页），补回列表顶部，胶囊标题不会退回「对话」。
- `utils/sse.js` 新增 `createIdleWatchdog(ms, onIdle)`，`useChatSSE.js` 用 120s 空闲看门狗包住**整条请求链路**（`api.stream` 之前就 arm，收到响应前也计时）：超时先把 UI 收尾（`failStream` + `isStreaming=false`）再 `abort`，不指望 `AbortController` 一定能叫醒半死的 `read()`；已 abort 过（用户停止）则只复位状态、不报假超时。`cancel()` 现在也直接复位 `isStreaming`。服务端没有心跳帧，阈值不能再压。
- 顺带修好一个静默失败：空白新会话里选智能体原来 `handleSelectAgent` 直接 `return`（前端等于没反应）；现在没有会话 id 时先记在本地，首条消息随 `agent_id` 落库。有了 eager 创建后，正常路径走 PATCH。

## 验证

- `cd front-end && npm test`（新增看门狗两条用例）、`npx vite build` 通过。
- 真机（PWA，不杀进程）：
  1. mac 上新建会话并说话 → 手机开下拉 → 出现；
  2. 手机顶栏＋新建并发消息 → 下拉里有这条（标题已生成）；
  3. 流式中切到「灵阅」再回「对话」→ 会话仍在，历史能拉回，不再另建一条。

## 已知残留

- 点了＋但一句话没发就离开，会留下一条空「新对话」（原来的懒创建就是为了避免这个）。接受这个代价，换取「id 永不丢」；累计多了需要手工归档——后端目前没有空会话清理任务，后续可考虑按 `updated_at` 清掉 N 天无消息的非默认会话。
- 重开面板会重拉第一页：翻过的页收回（「加载更多」还在，游标用 `loadedCount` 不会跳条），当前会话会补回列表顶部。
- 服务端没有 SSE 心跳帧：120s 阈值是按「多久没收到任何字节」判死，`verify` / 子任务里单次 `ChatSync` 期间的静默可能逼近这个量级（误判的代价是看到「连接超时」+ 丢实时增量，服务端照常落库，重新加载即恢复）。要更灵敏需服务端在静默时发 `: ping`（属于后端改动，单独排）。
