# 056 macOS 语音分句被提交进「上一个会话」

## 问题表现

macOS 上同时开着两个会话 tab：A「UIUX系统学习路径」、B「语音对话声纹方案」。停在 B 按麦克风连续对话，
说的话没有出现在 B 里（B 只多出后面几轮），而是全落进了 **A**；朗读正常，所以用户先听到回答、
回头却在自己看的会话里找不到文字，像是「话丢了」。

线上实测（2026-11-10 早）：

- 本机客户端日志：`02:16:04 [voice] config ready`（最后一次 `onAppear`，当时在 A）
  → `02:26:29` 打开 B（**没有任何 `onAppear` 日志**）
  → `02:30:22 [voice] start requested`
  → `02:30:26 [voice] submit sentence: chars=7 ... cid=68dbe246-…`（A 的 id）。
- 线上库：5 条 user 消息全部 `conversation_id = 68dbe246-…`（A），时间 02:20–02:31 UTC；
  用户在看的是 `9ff9a7bc-…`（B，02:26:29 创建）。

## 根因

macOS 只渲染 `chatTabManager.currentTab` 的那**一个** `ChatView`
（`ContentView.macOSContentArea`），换 tab 只是换 `ChatPage(chatViewModel:)` 的入参，
**结构位置不变、也没有 `.id()`** → SwiftUI 复用同一个 `ChatView`，`onAppear` 不会重跑。于是：

- `@StateObject private var voice = VoiceChatService()` 跨 tab 存活；
- `onAppear` 里装的 `voice.onSentence / voice.onBargeIn / voice.isChatBusy` 以及
  `viewModel.onAssistantReplyDelta` 等回调**捕获的是当时的那个 `viewModel` 结构值**
  （`ChatViewModel` 是 class，结构值拷贝后仍指向同一实例），一直指向旧会话。

结果：麦克风在 B 上按，分句 `sendMessage` 却走 A 的 VM；`onAssistantReplyDelta` 也挂在 A 上，
B 的回复气泡不跟随朗读进度。iOS 不受影响（全程只有一个 `ChatViewModel`）。

## 最终方案

把语音接线抽成 `ChatView.wireVoice()`，在 `viewModel` 换人时重接（`client/xHelper/xHelper/Shared/Views/ChatView.swift`）：

```swift
.onChange(of: ObjectIdentifier(viewModel)) { _, _ in
    voice.stopSpeaking()
    voice.cancel()      // 离开的会话不该继续占麦克风、继续念
    wireVoice()         // 先摘旧 VM 的朗读回调，再装到新 VM
}
```

`wireVoice()` 里先调 `unwireVoice()`（把 `wiredViewModel` 的 `onAssistantReplyDelta/Done/Aborted` 置 nil 再清空），
避免切走后旧会话的流式尾巴继续被念出来；`onDisappear` 同样调 `unwireVoice()` —— 视图消失后
`voice` 已 `cancel`，但回调还挂在 VM 上会重新开一轮朗读。

线程安全的一条：进出 PiP 时中栏与 `FloatingChatPanel` 里的两个 `ChatView` 会共用同一个 `ChatViewModel`
（`ContentView` 用 `if !rightSidebar.isPipChat { macOSContentArea }` 替换视图，稳态只有一处，替换瞬间两处共存），
而 `onAssistantReply*` 在 VM 上是单槽。`onAppear` / `onDisappear` 先后不定，盲摘会把另一处刚接上的朗读摘没。
因此 `ChatViewModel` 增加 `replyOwner`（`ObjectIdentifier(voice)`）：`wireVoice()` 写入，`unwireVoice()`
只在自己仍是占用者时才置 nil。

不做 `.id()` 重造视图：那会重置滚动位、输入框草稿等 `@State`，代价大于收益。

## 验证

1. macOS 开 A 会话 → 切到 B（新 tab，B 有自己的 `ChatViewModel`）→ 按麦克风说一句：
   - 气泡出现在 **B**；
   - 日志 `[voice] submit sentence: ... cid=` 是 **B 的 id**。
2. 语音对话进行中切 tab：麦克风释放、朗读停止（`voice.cancel()` / `stopSpeaking()`）。
   这是**明确取舍**：换 tab 视为结束本轮语音 —— 连续档的会话被拆，录音发送档里正在录的那段直接丢弃、
   语音条关闭（回来要重新点麦克风）。
3. iOS 不受影响（`ObjectIdentifier` 不变，`onChange` 不触发）。
