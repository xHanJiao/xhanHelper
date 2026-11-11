# 062 · 切换 PiP / 折叠成圆点会掐断正在朗读的语音

**日期**：2026-11-11
**影响面**：macOS 客户端（`client/xHelper`）。网页端与 iOS 无 PiP，不受影响。
**现象**：Agent 正在朗读回复时，点顶栏「最大化侧栏内容」进 PiP（或退出 PiP、或把 PiP 面板
折叠成圆点），朗读当场断掉；如果这一轮还在流式输出，还会**从第一句重新念一遍**。
连续对话（麦克风开着）时切 PiP 则连会话一起结束。

## 根因

PiP 不是「把中栏缩小」，而是**把中栏整棵视图拆掉、在右栏上另挂一个 ChatView**：

- `ContentView` 的布局是 `if !rightSidebar.isPipChat { macOSContentArea }`，进 PiP 时中栏消失
- `FloatingChatPanel` → `PipChatContent` → `ChatPage` → **新建一个 `ChatView`**
- 每个 `ChatView` 自带一份 `@StateObject voice = VoiceChatService()`，而旧视图的 `onDisappear`
  无条件执行 `voice.cancel()` + `voice.stopSpeaking()`

于是：TTS 队列在旧实例里，旧实例被销毁 → 音频与队列一起没了。新实例的 `speechConsumed = 0`，
若回复还在流式，`appendReplyDelta` 会把整段正文从头再切一遍句 → 从头重念。
折叠成圆点同理：`FloatingChatPanel.body` 里 `if pipCollapsed { 圆点 } else { expandedBody }`
把 `ChatView` 一起拆了。

`docs/design/mac-apps/chat-pip-mode.md` 早就记录过「进出 PiP 会拆掉并重建中栏 ChatView，
状态必须放 VM」（草稿图那次修复），但语音服务当时还留在视图里，没被这条纪律覆盖。

## 修法

1. **语音服务随 tab 存活，不随视图**：`ChatViewModel.voice`（与 `draftAttachments` 同层）。
   `ChatView` 改成计算属性 `voice { viewModel.voice }`；VM 把 `voice.objectWillChange`
   转发到自己的 `objectWillChange`，视图照常跟着「说 / 想 / 句级进度」刷新。
2. **在屏使用者计数**：`VoiceChatService.attachView() / detachView()`。中栏与 PiP 的 ChatView
   会先后接管同一个服务，最后一个走的人才收尾。
3. **延后 200ms 复查**：进出 PiP 时新旧视图的 `onAppear` / `onDisappear` 先后顺序 SwiftUI
   不保证，当场停就会掐断（新视图还没来得及 attach）。离开聊天页 / 关窗仍是 0.2s 内停麦。
4. **折叠不拆内容树**：`FloatingChatPanel` 折叠时不再切分支，`expandedBody` 常驻，
   只加 `opacity(0)` + `allowsHitTesting(false)`；折叠期间仍按**上次展开的尺寸**布局
   （缩到 45×45 会让消息列表 / 输入框重排、丢滚动位置）。
5. 换 tab 的路径（`onChange(of: ObjectIdentifier(viewModel))`）随之改为「停旧 tab 的服务 →
   登记新服务 → 重接」；`unwireVoice()` 的身份改从 `wiredViewModel` 取（换 tab 时
   `viewModel` 已经是新的了，原来会摘不掉旧回调）。

## 验收

- 朗读中进 PiP / 出 PiP：朗读不中断，音波条与面板状态仍为「说」，念完正常收尾
- 朗读中折叠成圆点 / 再展开：不中断；展开后消息列表还在原位置
- 连续对话开着时切 PiP / 折叠：会话、麦克风、判停提交都不中断
- 回归：拖拽 / 吸附 / 缩放 / 双击折叠 / ⌘⇧E 正常；草稿图、⌘V 粘贴、发送不受影响
- 回归：切 tab 仍结束旧 tab 的语音；关窗 / 退出不留麦克风

## 已知未修

- PiP 进 / 出仍会重建 `ChatView`（滚动位置会回到顶部）—— 既有行为，本次不动。

## 附：折叠时的焦点

折叠后内容树仍常驻（为了不结束语音会话），隐藏的输入框若还持焦点会继续吃键盘、独吞 ⌘V。
所以在 `FloatingChatPanel` 的 `.onChange(of: manager.pipCollapsed)` 里，折叠时若
`ChatInputFocus.shared.isFocused`（PiP 里只有这一个输入框）就 `makeFirstResponder(nil)`；
折叠期间还叠 `.disabled` + `allowsHitTesting(false)`，全键盘控制下也点不到看不见的控件。
放在 `onChange` 而不是 `toggleCollapse`：⌘⇧E 与进 PiP 自动折叠都直接写 `pipCollapsed`。

## 影响文件

- `client/xHelper/xHelper/Shared/ViewModels/ChatViewModel.swift`（`voice` + `objectWillChange` 转发）
- `client/xHelper/xHelper/Shared/Services/VoiceChatService.swift`（`attachView` / `detachView`）
- `client/xHelper/xHelper/Shared/Views/ChatView.swift`（观测绑定、`holdVoice` / `releaseVoiceIfLast`、换 tab 重绑）
- `client/xHelper/xHelper/Shared/Views/FloatingChatPanel.swift`（折叠不拆内容树）
