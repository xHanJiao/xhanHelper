# 069 · 手机 PWA 流式中发送后输入框丢光标

- 日期：2026-11-18
- 平台：iOS PWA（Safari / 主屏独立窗口）
- 相关：`docs/design/chat-prompt-queue/`（流式中排队发送）

## 问题表现

LLM 正在流式回复时，在输入框里打字后点右侧「↑」把消息加入队列，输入框立刻失去焦点：
光标消失、iOS 键盘收起，得重新点一次输入框；键盘收/起时 `.app-shell` 高度在
`100dvh - var(--keyboard-inset)` 与 `100dvh` 之间跳、底部 Tab 挂载/卸载，输入卡跟着上下移动，
手快时还会点空，表现为「有时候甚至输入不进去」。

## 根因

1. `NButton` 的 mousedown 处理会主动抢焦点（`naive-ui/es/button/src/Button.mjs`）：
   `nativeFocusBehavior` 默认 `!isSafari`，Safari 下为 `false`，于是每次按下都
   `e.preventDefault()` 之后 `selfEl.focus({ preventScroll: true })`，
   焦点从 textarea 挪到 `<button>` 上 → iOS 收键盘。
2. iOS 自身「点到编辑区外就收键盘」的行为，靠 `preventDefault()` 挡；
   但 naive 在 preventDefault 之后又自己聚焦，等于把挡掉的效果还回去。
3. 空闲直发时同样会掉焦点，只是那时不需要接着打字没人注意；
   排队功能（`ceaeef1c`）把「流式中发送」变成常态操作后才暴露成 bug。

## 方案

`front-end/src/views/ChatView.vue`：

- 两枚发送键（↑）加 `:focusable="false"`：naive 只 `preventDefault()`、不再 `focus()` 到自己身上，
  焦点交给我们的收起逻辑统一处理（否则先被按钮抢焦点、再 blur，iOS 键盘会闪一下）。
  代价是发送键 `tabindex=-1`，Tab 聚焦不到它（Enter 即可发送，可接受）。
  「■ 停止」**不加**：它需要保留 `tabindex`（停生成没有键盘替代路径），点它把焦点拿走无所谓。
- `handleSend()` 里（`collectOutgoing()` 通过之后）手机端发完就收：`inputVisible = false` + textarea blur，
  键盘和输入卡一起收掉，整屏还给对话流；用户点右下角「发消息」再展开。
  流式中入队同样适用（排队消息已在对话流里，看得见）。
  「手机端」沿用 `useMobile()` 的 `isMobile`（**宽度 < 1100**）：窄桌面窗口也一样收起 —— 它与输入卡的
  手机布局、⌄ 收起按钮是同一个断点，不额外分档。
- 收起后 ■ 随输入卡一起消失，所以在右下角浮标**旁边补一枚 ■**（同一行，不展开输入卡），
  流式中收起态也能直接停生成。
- 输入卡里的「■ 停止」不收起输入卡（省一次「发消息」），但它仍会把焦点拿走：iOS 上键盘会收，
  想接着改一句得再点一下 textarea。

## 验证（真机）

1. 流式中打字 → 点 ↑ → 输入卡收起、键盘收起，排队气泡可见。
2. 收起态（流式中）点浮标旁的 ■ → 停止生成，输入卡仍收起。
3. 点「发消息」→ 展开输入卡并弹键盘，能接着打。
4. 输入卡展开时点 ■ → 停止；输入卡保持展开（键盘会收，接着改一句需再点一下 textarea）。
5. 空闲直发 → 同样收起，不会残留在展开态。
6. 桌面点 ↑ 后 textarea 仍聚焦（改前焦点会跳到按钮上），输入卡不收。
7. 切会话 / 语音条开着时不受影响（没有 textarea 可 blur，收起也不参与）。
8. 草稿不合格（图还在上传 / 有图失败）→ 提示后不收起，键盘不被收走。
