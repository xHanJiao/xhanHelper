# 045 — Web 端计划模式/无痕开关显示会自己关掉

- **状态**：已解决
- **发现日期**：2026-11-03
- **影响范围**：`front-end/src/views/ChatView.vue`、`front-end/src/stores/conversations.js`、`front-end/src/composables/useChatSSE.js`

## 表现

在 Web 端打开计划模式后：

- 发一条（或几条）消息，输入框的计划模式 pill 变回关闭态；
- 切到别的会话再切回来，pill 也变回关闭态。

服务端其实已经落库为 `plan`（刷新页面或重新拉列表又显示正确），所以是纯前端显示状态被覆盖。

## 根因

`ChatView.vue` 里有一个 watcher，把 `convs.list` 中当前会话条目的 `mode` / `ephemeral` 同步到 `chat.mode` / `chat.ephemeral`。它监听的是「列表里找到的那个对象」，对象身份一变就重新覆盖。两处隐患叠加：

1. `convs.prepend()` 用 `{ id, title, ... }` 这样的残缺对象替换了列表条目（`handleSend` 顶置当前会话、SSE `done` 给新会话入列），新对象没有 `mode`/`ephemeral`，watcher 读到 `undefined` → 回落成 `work`。
2. `toggleMode` / `toggleEphemeral` 只 PATCH 服务端并改 `chat.mode` / `chat.ephemeral`，没有同步 `convs.list` 里的条目。切走再切回时 watcher 读到旧条目（还是 `work`）→ 覆盖显示。

## 修复

- `stores/conversations.js`：`prepend()` 改为与已有条目合并（`{ ...prev, ...conv }`），不再冲掉 `mode`/`ephemeral`/`agent_*`。
- `stores/conversations.js`：新增 `updateMode(id, mode)` / `updateEphemeral(id, ephemeral)`，`ChatView` 在 PATCH 成功后调用，保持列表与「服务端事实」一致。
- `composables/useChatSSE.js`：新会话 `done` 入列时带上发送那一刻的 `initMode` / `ephemeral` 快照（流式中途再点开关不该改写已落库的值）。

## 已知残留（本次未修）

打开一个不在会话列表第一页、且没点过「加载更多」的会话（深链进入），列表里没有该条目，watcher 拿不到 `mode`，pill 仍可能显示错误。这不是本次覆盖问题，需要后端补会话详情接口（或让列表/消息接口带回 `mode`）才能根治。
