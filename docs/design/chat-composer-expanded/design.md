# 聊天输入栏全屏输入（macOS）

> 状态：**已实现**
> 范围：macOS 客户端（`ChatView.swift` / `InputBar.swift` / 新增 `ExpandedComposer.swift`）。iOS 与服务端 **零改动**。

估算（定稿）：2 面 / 1 缝 / 1 次真机往返 / 0 个待拍决定，约 1.5–2.5 小时。

---

## 一、背景

写手账时常有很长的草稿，普通输入胶囊最多显示 4 行就内部滚动，看不到全局、也不好回头改。
需要一个「全屏输入」模式：把输入区临时放大到聊天中栏整高，专心写长文。

---

## 二、决策记录

| 编号 | 决策 | 结论 |
|---|---|---|
| EC-1 | 覆盖范围 | ✅ **聊天中栏整高**（左右栏、顶栏保留）。不做整窗覆盖 |
| EC-2 | 键位 | ✅ **与普通态完全一致**：`Enter` 发送、`⇧Enter` 换行、`Esc` 收起（用户明确「不要改按键」） |
| EC-3 | 顶部 | ✅ 左上「全屏输入」+ 右上收起箭头（图中做法 1） |
| EC-4 | 发送后 | ✅ **自动收起**回对话，立刻能看到回答 |
| EC-5 | 入口 | ✅ 普通输入栏状态行、紧挨「更多操作」chevron 左边一个展开键（`rectangle.expand.vertical`），hover 提示「全屏输入 · Enter 发送 / ⇧Enter 换行」 |
| EC-6 | 草稿 | ✅ 与普通输入栏共用 `viewModel.draftText`，发完由 `sendDraft()` 统一清空 |
| EC-7 | 收起后焦点 | ✅ 焦点还给普通输入框（`InputBar.focusToken`），不用再点一下 |

---

## 三、实现

```
ChatView.content
└─ .overlay { if composerExpanded { ExpandedComposer(...) } }   ← 盖住中栏整高
   ├─ 顶部行：「全屏输入」+ 收起箭头（hover：收起（Esc））
   ├─ TextEditor（字号跟随 FontSizeManager，撑满）
   └─ 底部细线 + 右下发送/停止（复用 SendStopButton）
```

- **新文件** `Shared/Views/ExpandedComposer.swift`（整体 `#if os(macOS)`）：只负责面板外观、键盘与焦点；
  草稿用 `@Binding` 注入，发送/停止/收起用闭包回调。独立成文件是因为 `ChatView` 里
  InputBar 的调用表达式已经顶到类型检查预算上限（见该文件既有注释）。
- **按键**：`Enter` 拦截为发送（与普通输入栏同一套 IME `hasMarkedText` 判断），
  `⇧Enter` / `⌥Enter` 放行给 TextEditor 换行；`Esc` 收起（不清草稿）；
  `Tab` 循环计划模式、`⌃C` 清空草稿 —— 与普通输入栏一致，两处共用 ChatView 里的
  `cycleMode()` / `clearDraft()`。
- **与 App 级 Esc 的关系**：有 L3 工具确认待处理时，App 级 NSEvent monitor 会把 Esc 当「拒绝」吞掉。
  展开期间 `ChatInputFocus.expandedComposerActive` 为真，monitor 跳过这条分支，Esc 才归面板
  （否则面板收不起来，还会在看不到确认条时静默拒绝工具）。
- **焦点**：面板出现后自动聚焦；焦点变化经 `onFocusChange` 上报。普通胶囊与面板各存一份焦点态、
  再取「或」得出 `inputFocused`，`ChatInputFocus.shared.isFocused` 只在 ChatView 一处写，
  避免两个 `@FocusState` 交替上报时后到的 false 冲掉共享标记（⌘V 贴图靠它定位收件视图）。
- **收起**：`composerExpanded = false` + 200ms 后给 `InputBar` 发一个新的 `focusToken`
  让普通输入框回焦。
- **切换会话**：`onChange(of: ObjectIdentifier(viewModel))` 里强制收起。

---

## 四、边界与已知上限

- 只盖中栏，不含顶栏与左右栏（拍板如此）；中栏通常已是最宽的一块。
- **附件**：草稿已选图片不重复渲染托盘（展开态看不到缩略图），但发送时照旧一并带上。
- **语音会话**：语音条激活时输入栏尾部动作整组为空（既有逻辑），因此没有展开入口；
  展开态本身也没有语音按钮，发完即收起。
- **PiP**：PiP 面板里同样能展开，只是盖住那块小面板；行为与主视图一致。
- **切会话**：只收起面板、不自动回焦新会话的输入框（切 tab 时抢键盘更打扰）。
- **Esc 与 L3 确认**：展开期间 Tab/Esc 不再作为「允许/拒绝」——Esc 收起面板，Tab 照旧只循环模式；
  要处理待确认工具得先收起面板（或直接点确认条）。
- 离屏 `ImageRenderer` 预览的材质不可信，最终观感以 Xcode 真机为准。

---

## 五、自查与验证

- `swiftc -typecheck`（macOS 26.3 + iOS 26.0，带 `-default-isolation MainActor`）0 error。
- 真机（用户在 Xcode 确认）：
  - 普通态发送键左边出现展开键，hover 有键位提示。
  - 点开后中栏被面板盖住、编辑器自动聚焦，草稿与刚才写的一致。
  - `Enter` 发送并自动收起、能看到 AI 回复；`⇧Enter` 换行；`Esc` 收起且草稿保留。
  - 收起后光标回到普通输入框，可直接继续打字。
  - 切换会话 tab 时展开态自动收起。

---

## 六、后续变更

- （暂无）
