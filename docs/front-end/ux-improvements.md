# 体验优化记录

> 日期: 2026-05-09

## 1 — 发送消息后 AI 回复时，思考过程应自动可见

- **状态**：待优化
- **影响范围**：`ChatView.vue` 滚动逻辑

### 问题

用户发送消息后，AI 开始流式回复。`scrollToBottom()` 将页面滚到底部，但思考过程（ThinkingBlock）位于 AI 消息气泡的上半部分，在滚动到底部后可能被推到视口上方，用户看不到思维链内容。用户需要手动向上滚动才能查看思考过程。

### 原因

`scrollToBottom()` 以 `messagesEndRef`（位于所有消息之后）为锚点滚动，导致视口对齐到页面最底部，而非 AI 消息气泡的顶部。

### 改善方向

方案一：在流式过程中，将滚动目标从 `messagesEndRef` 改为 AI streaming bubble 元素本身（通过 ref 定位）。使用 `scrollIntoView({ block: 'nearest' })` 确保思考块可见。

方案二：在 ThinkingBlock 组件上设置 `scroll-margin-top`，让浏览器自动为折叠内容预留空间。

## 2 — 刷新页面后工具调用卡片（"已调用 N 个工具"）消失

- **状态**：已修复（2026-05-09）
- **影响范围**：`MessageBubble.vue` / `ToolCallCard.vue` / `ChatView.vue`

### 问题

通过聊天触发工具调用后，页面刷新重新加载消息历史，AI 消息气泡中的"已调用 X 个工具"摘要消失，工具调用卡片不再展示。

### 原因

后端 API 返回值与前端 streaming 态字段名不一致：

| 含义 | 后端 API (JSON key) | 前端 streaming 态 (JS prop) | 格式 |
|------|---------------------|---------------------------|------|
| 工具调用数据 | `tool_calls_json` | `toolCalls` | API: JSON string, streaming: array |
| 思考过程 | `reasoning_content` | `reasoning` | API: string, streaming: string |

刷新页面后前端调 `messages-v2` 获取历史消息，`message.toolCalls` 为 `undefined` → `ToolCallCard` 的 `v-if="toolCalls.length > 0"` 不成立 → 不渲染。同理 `message.reasoning` 为 `undefined` → ThinkingBlock 不渲染。

### 修复

在 `ChatView.vue` 的 `loadHistory` 中增加 `normalizeMessage` 转换函数，将 API 字段名映射到前端内部字段名：

- `reasoning_content` → `reasoning`，同时设置 `reasoningDone: true`（历史消息的思考过程已完整）
- `tool_calls_json`（JSON string）→ `JSON.parse` → `toolCalls`（array），每个 tool call `status` 定为 `'done'`
