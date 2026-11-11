# 063 · 发完消息后正文不跟到底部，最新内容被输入卡切掉

**日期**：2026-11-11
**影响面**：网页前端（`front-end/src/views/ChatView.vue`）。Swift 客户端是另一套滚动实现（反转列表），不受影响。
**现象**：发出消息（或回复正在流式输出）时，最新内容停在输入卡上方看不见，要手动往下拉一下。
触发时右下角会挂着 `↓` 按钮，但用户第一反应是"消息被输入框挡住了"。

## 复现（真实应用 + 真实 SSE）

测试账号 `scroll-test`（dev 库 `xhan_helper_test`）+ headless Chrome，1280×860：

| 时刻 | 距底 dist | scrollTop |
|---|---|---|
| 发消息前（已在底部） | 0 | 1058 |
| **发出一条 10 行消息后 0.7s** | **269** | 1058（不动） |
| 流式 1.5s | 399 | 1058 |
| 排队消息出队后 | 83 | 1778（非流式分支无条件贴底了一次）|
| 本轮结束 | 0 | 1940 |

同一逻辑在窄屏（390px）对照测试里更明显：流式中入队 → 末尾被切 **871px**；流结束后图片加载完成 → 被切 **326px**。

## 根因

`ChatView.scrollToBottom()` 在流式中是**内容已经长高之后**才量距离，把"我们自己把内容撑高的那一帧"误判成"用户上滑了"：

```js
if (chat.isStreaming && streamingBubbleRef.value) {
  const distFromBottom = container.scrollHeight - container.scrollTop - container.clientHeight
  if (distFromBottom > 80) return          // ← 判定成用户在读历史，本轮再也不跟随
  container.scrollTop = container.scrollHeight
}
```

一旦 `return` 就不会再恢复：后续每个 delta 的 dist 只会更大。只有下一轮出队走**非流式分支**（那条是无条件贴底）时才会"自己好了"——这就是"有时候"的来源。

触发路径不止一条，共同点是**单帧内容增量 > 80px**：

1. **自己发出的消息本身 > 80px**（长文本、多图）。`dispatchOutgoing` 先 `addMessage()` 再 `scrollToBottom()`，
   而 `scrollToBottom` 是 `nextTick + rAF` 异步执行；`sendMessage()` 里 `chat.isStreaming = true` 是**同步**置上的
   （`useChatSSE.js:168`），rAF 跑到时已经在流式分支 → 量到的就是自己气泡的高度。**必现**，且整轮不跟随。
2. **流式中按 Enter 入队**：`pendingSends` 多出一块排队气泡（虚线块），同样是单帧 > 80px。
3. **流结束后图片 / markdown 异步长高**：没有任何 watcher 会再触发，`ResizeObserver` 也没挂在消息内容区
   （目前只挂了输入卡，`ChatView.vue:313`）。

## 踩坑：不能靠"滚动事件里量距离"来记跟随意图

第一版方案想改成 sticky 标记，但仍用 `dist > 80` 在 `scroll` 回调里更新标记 —— **同样失效**（窄屏对照里仍被切 871px）。
因为 `scroll` 事件是异步派发的：等到回调执行，内容早就又长高了，`dist` 把"内容变高"和"用户上移"混在一起。

**判定必须只看 `scrollTop` 有没有变小**（用户把视图往上挪），内容长高不改 `scrollTop`。

## 修法（本分支实施）

只改 `front-end/src/views/ChatView.vue`：

1. `let stickToBottom = true` / `let lastScrollTop = 0`。
2. `handleScroll` 里先认「已在底部」（距底 ≤80 → `true`），再认「用户把视图往上挪了」（`scrollTop` 比基线小 0.5px 以上 → `false`）。
   顺序不能反：**键盘弹出 / 输入卡变高 / 缩窗口**会让视口变小、浏览器把 `scrollTop` 夹小，那不是用户上滑，
   反了就会在手机上整轮不跟随（又回到 063 的症状）。
3. `scrollToBottom()` 去掉事后量距离，改成 `if (!stickToBottom) return`。**所有调用点一视同仁**
   （流式中增量、流式结束、内容长高都尊重跟随意图）；只为那段判定存在的 `streamingBubbleRef` 一并删掉。
4. `dispatchOutgoing` / `enqueueOutgoing` / `handleScrollToBottomBtn` / 三个路由分支显式贴底
   （"我刚发了消息"永远该贴底）。`pinToBottom()` 顺带同步 `lastScrollTop` 与 `stickToBottom`，
   所有程序化滚动（同步会话、错误重试、上滑加载历史的位置补偿）都走它，基线不会漂。
5. `.chat-column` 挂 `ResizeObserver`：`stickToBottom` 时贴底；`onBeforeUnmount` 里 disconnect。
   补掉流结束后图片 / markdown 异步长高这一路（实测差 326px）。
   **`.chat-column` 必须 `align-self: flex-start`**：它是 `.chat-messages`（`display:flex`）的 flex item，
   默认 stretch 时盒子高度恒等于视口，内容长高根本不触发 RO（第一版就栽在这，看着挂了其实空转）。
6. 同步更新文件头 L26 / L55-57 两行生命周期注释。

保留的行为：流式中用户上滑读历史 → 不被拽回底部（**本轮结束时也不拽回**）；`↓` 按钮阈值（200px）与位置逻辑不动。
上滑不足 80px 仍按「在底部」处理（与改造前的 80px 判据、`↓` 的 200px 阈值一致），要脱离跟随得甩出 80px 以上。

## 验收

- 发一条 10 行长消息：发送后 0.7s 距底 = 0，最后一条内容完整可见
- 流式中按 Enter 入队：排队气泡出现后仍贴底，回复继续跟随
- 流式中手动上滑：不被拽回；**本轮结束也不拽回**；点 `↓` 后恢复跟随
- 流结束后图片 / 代码块加载完：仍贴底（RO 命中）
- 视口变矮（手机键盘弹出 / 挂图撑高输入卡 / 竖向拖窗口）：仍继续跟随，不被误判成上滑（`onLayoutResize` 里贴底时重 pin）
- 回归：切会话定位到底部、上滑加载历史的位置保持、输入卡收起/展开后 `↓` 位置正确
- `cd front-end && npx vite build`

## 修复后实测（同一套脚本复跑）

| 时刻 | 修复前 | 修复后 |
|---|---|---|
| 发出 10 行消息后 0.7s | 269px（被切） | **0px** |
| 流式 1.5s | 399px | **0px** |
| 流式中入队后 1.5s | 83~871px | **0px** |
| 流式中上滑 1.7s | — | 331px（未被拽回，`↓` 出现）|
| 上滑后本轮结束 | 拽回底部 | **1381px，仍停在用户看的位置** |
| 点 `↓` | — | 0px，`↓` 收起 |
| 视口 860→460（贴底态） | — | 0px，`↓` 不出现 |

测量时点落在两帧之间会看到几十 px 的瞬时值（如 51px），下一次 delta 就归零；这不是 063 的表现（那条是单调增大、永不归零）。

## 影响文件

- `front-end/src/views/ChatView.vue`（`scrollToBottom` / `handleScroll` / 发送路径 / `onMounted` + `onBeforeUnmount`）

## 附：验证脚本

复现用的 headless Chrome 脚本（CDP 直连、注入 token、真实发消息）留在
`/private/var/folders/ll/_20zt46d3c38h75j9t2xg1c00000gn/T/opencode/chat-scroll-repro3.mjs`，
修复后用同一脚本复跑，期望 `长消息出现后 0.7s` 那一行 `dist = 0`。
