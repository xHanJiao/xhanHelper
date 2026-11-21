# 073 · 网页输入框打拼音时丢光标、未上屏的拼音消失

- 日期：2026-11-21
- 平台：iOS PWA（主屏独立窗口，原生拼音输入法），现象与具体系统版本无关
- 相关：`docs/design/voice-ptt-composer/`（输入框形态）、`docs/bugs/069`（同族的「丢光标」）、`docs/bugs/074`（同一输入框的另一个坑：粘贴后输入法降级，与本条无因果关系）

## 问题表现

手机上在聊天输入框里打中文，**文字快满一行时再打拼音**，偶发：

- 输入框里的拼音（组合输入中、带下划线那段）直接消失，没上屏；
- 光标也不见了；
- 键盘没有收起（说明焦点还在 textarea 上），输入法自己的候选条还挂着 —— IME 侧和 DOM 侧状态分叉。

不是必现。

## 根因

原实现用的是 naive-ui `NInput` 的 `autosize`。它不是把 `height` 写在 textarea 上，而是：

- 真 textarea 被设成 `position: absolute; height: 100%`；
- 高度由旁边一个隐藏的 mirror `div` 的内容撑出来，mirror 内容是 `value + "\r\n"`（永远多一个换行）；
- `syncMirror()` 在「组合输入中直接 return」**之前**执行（`naive-ui/es/input/src/Input.mjs:421-436`），
  所以**拼音还没上屏时，输入框就已经跟着变高**。

于是「文字接近一行 + 打拼音」这个组合，必然在组合输入进行中把输入框顶高一整行，
连带触发 `ResizeObserver(mobileInputRef)` → `onLayoutResize()` → `pinToBottom()`（改消息列表 `scrollTop`）。
**组合输入期间改布局/滚动是 WebKit 掉 marked text 的经典条件**：composition 被就地作废，
未上屏的拼音随之丢弃，DOM 与 IME 状态分叉，就是你看到的样子。

未 100% 证实的一环：究竟是「输入框自身变高」还是「消息列表被改 scrollTop」触发的。
真机日志要抓 Safari 网页检查器（`compositionupdate` 之间是否夹着高度变化 / 滚动），当时没抓到，
所以方案直接把两条都掐掉（组合期间不量高、不滚动），不去区分。

## 方案

`front-end/src/views/ChatView.vue`：输入框从 `NInput` 换回原生 `<textarea>`。

- **高度直接写在 textarea 上**（`autoGrow()`：`height='auto'` → `min(scrollHeight, 行高 × maxRows)`），
  没有 mirror、没有绝对定位、没有 `$forceUpdate`；1 行起，手机 4 行 / 桌面 6 行封顶；
- **组合输入期间不量高**（`inputComposing` 标记）：盒子先不长高，超出部分靠 textarea 内部滚动，
  上屏后（`compositionend`）补一次高度。Vue 原生的 `v-model` 在组合中本来就不写 DOM 值、不回调，
  两边的处理是对齐的；
- **组合输入期间不动消息列表滚动**：`pinToBottom()` 直接 return，`compositionend` 后补一次贴底；
- 原来 5 处 `document.querySelector('.chat-input textarea')` 统一收成 `composerRef`。

没有用 `field-sizing: content`（Safari 26.2+ 才有）—— 一条 JS 路径对所有系统/浏览器都成立，
不值得为省 10 行代码做版本分叉。

## 验证（真机）

1. 文字打到快满一行，连续打拼音直到自动换行：拼音和光标不丢。
2. 输入框随内容从 1 行长到 4 行，到第 4 行后内部滚动，不再长高。
3. 删字/清空后高度回落。
4. Enter 发送、拼音组合中按 Enter 只上屏不发送。
5. 贴多行文本进来，高度一次量对。
6. 手机上发完消息输入卡收起，点「发消息」展开时高度仍对（草稿好几行也不塌）。
7. 语音条开 → 关，textarea 重新挂载后高度对。

真机验证：2026-11-21 · iOS PWA（原生拼音）· 1~7 全部通过，跨行打拼音不再丢光标/拼音。
