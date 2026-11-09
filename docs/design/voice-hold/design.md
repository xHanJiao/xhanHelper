# 语音对话 · 按住说（hold 暂存）

> 状态：已实现（macOS + PWA），待真机验收
> 分支：`feat/voice-chat`
> 关联：`docs/design/voice-chat/design.md`（C1 连续对话、C2b 句级朗读）
> 预览：`mockup.html`（本目录，单文件自包含，带控制条）

## 估算（定稿）

- 4 面（macOS 服务层 / macOS 输入栏+快捷键 / PWA composable / PWA 输入栏+快捷键）
- 4 缝（空格与输入焦点冲突、多 tab 归属、松手等尾巴的窗口取值、hold 与空闲计时器）
- 2–3 次真机往返（macOS 按住/松开/多 tab；PWA 桌面键盘 + 手机点按）
- 0 个待拍决定（UI 与文案已逐项拍板）
- 约 6–9 小时

## 背景

连续对话的「说完」完全由豆包 ASR 判定：`server/conf/config.yaml` 的 `asr.end_window_size: 800`
（`server/internal/voice/asr.go` 下发 `end_window_size`），**静音超过 800ms 就判停**，服务端
`SentenceCommitter` 去重后下发 `{"type":"sentence"}`，客户端收到就提交一轮
（PWA `ChatView.vue::submitVoiceSentence`、macOS `ChatView.swift` 的 `voice.onSentence`）。

于是出现两个真实问题（2026-11-09 真机截图）：

1. 「嗯，说起来」这种起头话，停 0.8 秒就被当成完整一轮提交，助手只能回「想起来什么了？接着说。」。
2. 一段连续的话被切成多轮，每轮还会打断上一轮的生成。

**为什么不能用文本特征补救**（文档核对结论）：

- `enable_punc` 默认 true，我们在 `asr.go` 写死 `EnablePunc: true`——**标点是 ASR 补的**，
  每个 `definite` 分句末尾都会被补上句号，所以「以句末标点结尾」这条判据恒真、没有信息量。
- `vad_segment_duration`（语义分句，默认 3000ms）文档明确「不决定判停，不会修改 definite 出现的位置」，
  配了 `end_window_size` 后该参数直接失效。去掉 `end_window_size` 只能靠真机试，且可能把判停拖到 3 秒。
- 客户端用麦克风电平猜「还在说」在 `docs/bugs/051` 已经证明不可靠。

结论：**判停这件事没法猜，只能让用户显式声明「我还没说完」**——即按住说。

## 目标

1. 按住期间，停顿多久都不提交；松手后整段作为**一轮**提交。
2. 两种触发方式：鼠标按住/松开，或键盘空格按住/松开；按钮点击也可切换（按下开始、再按结束）。
3. 连续对话开启时，输入栏只保留「按住说 / 退出语音对话」（+ 生成中的停止键），其余按钮隐藏。
4. macOS 与 PWA 行为逐项一致。

## 非目标

- 不做「非 hold 状态下的自动合并窗口」：普通说一句仍然一判停就提交，不额外加延迟。
- 不改 `end_window_size`，不做语义分句实验。
- 不改后端与 WS 协议：暂存完全在客户端，事件仍是 `sentence` / `voice`。
- 不做词级时间戳（朗读侧已在 C2b 说明）。

## 方案总览

客户端在 `sentence` 到达时先看 hold 状态：

```
空闲 ──按住/点击──▶ 暂存（holding）──松开──▶ 等尾巴 ──静默 1.5s──▶ 合并提交一轮
                       │                        │
                       │                        └─ 最长 3s 强制提交
                       └─ 期间 sentence 只追加，不提交、不打断
```

- **暂存期间**收到的每条 `sentence` 追加进已暂存串，不提交，也不打断正在生成的回复。
- **松手**时最后一句可能还在 ASR 的 800ms 判停 + 二遍识别里，所以要等：
  收到新的 `sentence` 或 `voice`（partial 有变化）就重置计时；**连续 1.5 秒无动静**则合并提交；
  **3 秒**硬上限防卡死。
- **提交**走与普通句完全相同的路径：先 `stopSpeaking()`，若上一轮还在生成先断流，再发一轮对话。

## 交互规格

### 状态机

| 状态 | 触发 | 行为 |
|---|---|---|
| 空闲 | — | 按钮空心 |
| 暂存中 | 按住空格 / 按下按钮 | 按钮实心 accent + 外圈 |
| 等尾巴 | 松开 | 按钮回到空心；1.5s 静默或 3s 上限后提交 |
| 生成中 | 上一轮在流式 | 用静态停止键替换退出键（仍是两个按钮，按住说保留） |

### 边界

- 松开时暂存为空 → 什么都不做（不提交空消息）。
- 断线 / 出错 → 清空暂存并提示，不补发。
- 暂存期间**不触发 30 秒空闲收口**（否则按住想事情会被收会话）：
  macOS 在 `armIdleTimer` 的重排条件里加 `|| isHolding`，PWA 在 `startIdleWatch`
  的「仍在进行」判断里加 `holding.value`。会话硬上限（`session_seconds`，按音频时长计费）不变。
- 暂存期间点「退出语音对话」→ 先 flush 暂存，再结束会话（不丢话）。
- 暂存中若 AI 还在朗读 → 松手提交时停朗读（与普通句一致）。
- 暂存期间不改变已有轮次：已经开始流式的那一轮继续跑，松手提交时才按 barge-in 规则停掉它。

### 时序（一次典型使用）

```
用户按住空格 ──▶ 说「我想一下，那个退租的事……还有科三」
                    ↑ ASR 在「想一下，」后判停      ↑ 在「……还有」后判停
              两句 sentence 都进暂存
用户松开 ──▶ 等 1.5s（等最后半句 definite）──▶ 一次性提交 21 字，作为一轮
```

## UI 规格

详见 `mockup.html`（三态 × 深浅色 × hover 均可切换）。

连续对话开启时，按钮行**整组水平居中**，始终只有两个按钮：

```
             [ 🖐 ]   [ ◎ ]      ← 非生成中
             [ 🖐 ]   [ ■ ]      ← 生成中
```

| 元素 | 规格 |
|---|---|
| 按住说 | `hand.tap` 图标，21pt，34pt 圆（与输入栏其他图标按钮一致）；常态 accent 色图标、无底色；按住时实心 accent + 白图标 + 外圈脉冲 |
| 连续对话 | 红色 `waveform.circle`（无状态点）；hover 时淡红底 + 气泡「退出语音对话」 |
| 停止 | 仅在 `isStreaming` 时出现，替换退出键（仍是两个按钮），静态（**去掉呼吸环**），点它 = 停朗读 + 停生成 |
| 隐藏 | 计划模式、信任、无痕、附件、更多 `^`、发送 `↑`、左下角上下文圆环 |
| hover 文案 | 按住说：`按住期间停顿不触发提交，松开后整段提交`；按住时：`松开后整段提交` |

- macOS 用系统符号 `Image(systemName: "hand.tap")`（`walkie.talkie` 不存在，`radio` 是收音机，不采用）；
  PWA 手绘等价 SVG（手指 + 按压弧），接受 ~90% 相似度。
- 颜色只用现有 token（`appAccent` / `danger` / `assistantText` 的透明度），不新增色相。

## 实现设计

### macOS

| 文件 | 改动 |
|---|---|
| `Shared/Services/VoiceChatService.swift` | 新增 `isHolding` / `heldSentences` / `hasHeld` / `startHold()` / `endHold()` / `flushHeld()`；`sentence` 分支在 holding / 等尾巴时只追加；`voice` 分支重置尾巴计时；`armIdleTimer` 的重排条件加 `isHolding` |
| `Shared/Views/ChatView.swift` | `inputBarStatusRow` 在 `voice.continuousActive` 时始终两个按钮：非生成 = hold + 退出，生成中 = hold + 停止；`inputBarTrailingActions` 返回空；`inputBarBottomLeading` 返回空；注册空格监听；新增 hold 按钮视图 |
| `Shared/Views/ChatView.swift` | 新增 `isCurrentTab: Bool` 入参（由 `ChatTabManager.currentTabID` 传入），避免多 tab 同时响应空格 |
| `Shared/Views/InputBarControls.swift` | `IconButton` 增加可选参数 `hoverBackground`（连续对话键 hover 时淡红底；其余按钮保持现在只缩放） |
| `Shared/Views/InputBarButtons.swift` | 新增 `HoldToTalkButton`（`hand.tap`，34pt 圆，按住实心 + 脉冲） |

`VoiceChatService` 新增的内部状态：

```swift
private(set) var isHolding = false
private var heldSentences: [String] = []
private var heldAwaitingTail = false   // 已松手、等最后半句
private var heldTailTask: Task<Void, Never>?  // 1.5s 静默
private var heldCapTask: Task<Void, Never>?   // 松手后 3s 硬上限
```

- `startHold()`：`isHolding = true`、清空暂存、暂停空闲收口；若正在朗读则停朗读。
- `endHold()`：`isHolding = false`，起 1.5s 的 flush 定时器；收到 `sentence`/`voice` 时重置，
  `Date() - heldStartedAt > 3s` 立即 flush。
- `flushHeld()`：拼接、清空、`onSentence(joined)`（上层仍做 `stopSpeaking` + 断流 + 发消息）。

空格监听：`NSEvent.addLocalMonitorForEvents(matching: [.keyDown, .keyUp])`，`keyCode == 49`；
满足 `isCurrentTab && voice.continuousActive && 第一响应者不是文本输入 && !event.isARepeat` 才接管，
接管理便 `return nil` 吞掉事件；`NSApplication.didResignActiveNotification` 时强制 `endHold()`
兜底丢失的 keyUp。

### PWA

| 文件 | 改动 |
|---|---|
| `src/composables/useVoiceChat.js` | 新增 `holding` / `heldSentences` / `hasHeld()` / `startHold()` / `endHold()`；`sentence` 分支在 holding / 等尾巴时只追加；`voice` 分支重置尾巴计时；flush 后走 `onSentence`；`startIdleWatch` 的「仍在进行」判断加 `holding` |
| `src/views/ChatView.vue` | 语音模式下隐藏 composer 左侧其余按钮与右侧发送，新增 hold 按钮（`hand.tap` SVG）；`window` 上注册 `keydown`/`keyup`（`code === 'Space'`、非 repeat、非输入元素、`preventDefault`）；`blur` 时收 hold |

- 按钮统一走「点击切换」（点一下开始、再点一下结束），空格走「按住/松开」；两者都落到同一对
  `startHold()` / `endHold()`，不各自实现一套。
- 移动端没有空格键，只能点按。

## 测试与验收

- 回归：不按 hold 时，普通一句仍然一判停就提交、不产生额外延迟（这条链路本次不改，只加分支）。
- `swiftc -typecheck`（macOS 26.3 / `-default-isolation MainActor`）0 error；`npm run build`、`npm test` 通过。
- PWA 用假 WS + 假 TTS 页面跑一遍：hold 说三句 → 松开 → **只提交 1 轮**、文本按顺序拼接、
  1.5s 内来了新句会重置、3s 上限能强制提交。
- 真机：
  - macOS 按住空格说两句带停顿的话，松手 1.5s 内出回复，且只有一条用户气泡；
  - 两个 tab 都开着连续对话时，只有当前 tab 响应空格；
  - 按住时切走窗口再回来，不会卡在暂存态；
  - PWA 桌面键盘、手机点按各一次；
  - 窄窗 320px 下两个按钮不换行、不溢出。

## 风险与已知取舍

- **松手后最多 1.5 秒才出回复**：这是等最后半句 definite 的代价（800ms 判停 + 二遍识别）。
  真机若觉得慢，只能把这 1.5s 调小，但会丢最后一句话。
- **纯鼠标用户发现不了空格**：文案里按定稿不再写「空格」，快捷键只出现在 `aria-label`。
  若真机发现率低，再补一条首次提示。
- **PWA 图标是手绘近似**：与 SF Symbol 有细微差异，21pt 下观感可能略粗。
- **多 tab 的空格归属**依赖 `isCurrentTab` 传参，切 tab 瞬间有极小窗口可能落到旧 tab，需真机确认。
- 暂存不设字数上限：单连接上限 600 秒（`session_seconds`），极端长按会被服务端 `limit` 收口，
  此时按断线处理（清空并提示）。

## 后续变更

- 2026-11-09（实现落地）：
  - PWA 的按住说图标直接复用 mockup 里 `hand.tap` 的位图蒙版（`mask: url(data:image/png;base64,...)`），
    不再手绘 SVG —— 保证与 mockup / macOS SF Symbol 同形。
  - 生成中（`isStreaming`）按住说按钮隐藏 —— **已被下方「真机反馈后的 UI 收敛」取代**（现为始终两个按钮）。
  - 「退出语音对话」先记录退出前是否在生成，再 `stopContinuous()`（内部先 flush 暂存），
    避免把刚 flush 出来的新一轮误停。
  - 居中靠 `InputBar` 已有的 `voiceStrip != nil` 信号：语音模式下按钮行用左右 Spacer 居中，
    并隐藏附件托盘 / 更多操作行 / 左下圆环；未新增 InputBar 公开参数。
  - 停止键直接复用 `IconButton(stop.fill)`（静态、无呼吸环），未改 `SendStopButton` 的 `pulsing` 参数。
  - 3s 硬上限从「松手」起算（不是从按下），长按超过 3s 不会被立刻提交。
  - 用户退出语音时 flush 出来的那一轮按普通消息提交（不挂 TTS 回调、不朗读）：先停旧轮再 flush，
    避免把刚提交的新轮误停；有暂存要 flush 时只断本地流、不发 `/api/chat/stop`（迟到的 stop 会误杀新轮）。空闲/会话上限收口走 `stopContinuous(flush: false)` / `stop({ flush: false })`，
    暂存按断线处理清空、不补发。
  - 按住说按钮 macOS 取 34pt，与相邻 IconButton 一致（不采用 mockup 的 36pt）。
  - 2026-11-09（真机反馈后的 UI 收敛）：
    - 删除「暂存 N 字」提示（窄窗会被按钮压住，信息价值低）；`heldCharCount` / `heldCount` 一并移除。
    - 语音模式始终两个按钮：非生成中 = 按住说 + 退出语音对话；生成中 = 按住说 + 停止。
      退出键与停止键不再同时出现；按住说在生成中同样可用。
    - PWA 修正 `.icon-pill` 覆盖：退出/停止/按住说现在真正是 36pt 且用各自颜色（此前被通用
      `color/background/width` 覆盖成灰色 32px）。
    - 椭圆外圈修复：`.hold-btn` 补 `position: relative`，按住态外圈只包住按住说按钮。
    - 空格监听增加 `event.window === 本视图窗口` 判定：PiP / 设置窗有焦点时不再抢空格。
  - 影响文件：`Shared/Services/VoiceChatService.swift`、`Shared/Views/ChatView.swift`、
    `Shared/Views/InputBar.swift`、`Shared/Views/InputBarButtons.swift`、`Shared/Views/InputBarControls.swift`、
    `Shared/Views/PageHolders.swift`、`Shared/Views/ContentView.swift`、`front-end/src/composables/useVoiceChat.js`、
    `front-end/src/views/ChatView.vue`。
