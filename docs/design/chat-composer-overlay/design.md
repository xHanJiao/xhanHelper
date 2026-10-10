# 聊天输入栏改悬浮浮层（内容铺满中栏、从输入栏下方穿过）

> 状态：**已实现**
> 范围：macOS/iOS 客户端共享的聊天视图（`ChatView.swift`）+ 消息列表（`MessageListView.swift`）。服务端 **零改动**。
> 前序：初版方案是「底部渐隐」单独解决硬切观感，落地前用户改口径为「输入框应悬浮、其下也要能显示内容」，故整体改为浮层方案（渐隐保留为收边）。

---

## 一、背景与问题

聊天中栏（`ChatView`）原是一个 `VStack`：消息列表在上、输入栏在下一行。后果：

1. **输入栏那一行是死区**：消息区在输入栏上缘就被裁掉，输入胶囊**下方**及两侧的空档只显示底图，
   对话内容不会出现（用户诉求：「输入框下面也可以展示对话内容，现在不是啊」）。
2. 消息视口下边界几乎与输入胶囊上缘重合，滚动时内容在这条线上**硬裁**，像「输入框上缘切断显示区」。

---

## 二、决策记录

| 编号 | 决策 | 结论 |
|---|---|---|
| CO-1 | 输入栏与内容的关系 | ✅ 输入栏改**悬浮浮层**：消息列表铺满中栏整高，内容从浮层下方穿过、从胶囊两侧透出 |
| CO-2 | 滚到底时的停靠 | ✅ 最后一条**完整露在输入栏上方**（`safeAreaInset` 留出与底部栈等高的安全区），不是被胶囊压住 |
| CO-3 | 实现手段 | ✅ `.safeAreaInset(edge: .bottom)`：浮层画在内容之上，同时原生给滚动内容留安全区；不手写高度测量做 inset |
| CO-4 | 浮层总高（含 AskUserBar/子任务条/L3 确认条） | ✅ 全部并入同一 inset 栈，作为整体上推 |
| CO-5 | 「回到最新」按钮 / 子任务浮层 | ✅ 用实测 `composerHeight` 上推，不沉到窗口底被输入栏盖住 |
| CO-6 | 底部渐隐 | ✅ 保留，但**下沉进 `MessageListView` 只包滚动正文**，避免连内部 overlay 一起遮（见 §3.2） |

---

## 三、实现

### 3.1 结构

```
ChatView.content
└─ ZStack(alignment: .bottom)                 ← 铺满整高
   ├─ MessageListView(...)                    ← 全高；底部渐隐在它内部（见下）
   ├─ if showScrollToBottom { 回到最新按钮 }   ← .padding(.bottom, composerHeight + 12)
   └─ subagentOverlay                          ← .padding(.bottom, composerHeight)
   .safeAreaInset(edge: .bottom, spacing: 0) {
       bottomComposer                          ← 原「底部行」，整体搬进来
   }
```

`bottomComposer` 依次放：`AskUserBar` → `SubagentStatusBar` → L3 确认条 → `inputBar`，
末尾 `.onGeometryChange(for: CGFloat.self) { $0.size.height } action: { composerHeight = $0 }`
把栈高回传给基座里的按钮/浮层。

### 3.2 关键点

- **`.safeAreaInset` 而非手拼 padding**：浮层画在内容之上（内容从浮层下方穿过、从胶囊两侧透出），
  同时对 ScrollView 施加等高的安全区，滚到底时最后一条自动停在浮层上方；高度随编辑器长高 /
  展开「更多操作」/ AskUserBar 出现自动跟随。
- **渐隐 mask 下沉、且只包滚动正文**：`MessageListView.body` 里把 mask 挂在 `messageScrollView`
  上、位于各 `.overlay` **之前**。这样只渐隐消息正文，**不会**连带遮住 `menuOverlay` /
  `shareBottomBar` / 分享结果弹窗的 `ignoresSafeArea` 遮罩（这些是 body 上的 overlay）。
  高度用 `MessageListView` 内的 `bottomFadeHeight` 常量（当前 24）。
- **`composerHeight` 只服务基座里两个元素**：基座 ZStack 是整高，`showScrollToBottom` 按钮与
  子任务浮层必须按栈高上推，否则停在窗口底被输入栏盖住。用 `onGeometryChange` 实测栈高。
- **iOS**：`safeAreaInset` 与键盘安全区自动叠加，输入栏照常被键盘顶起；iOS 分支结构不变。

---

## 四、实现面

| 文件 | 改动 |
|---|---|
| `Shared/Views/ChatView.swift` | `content` 去掉原外层 `VStack`，`ZStack` 直接挂 `.safeAreaInset`；原底部行抽成 `bottomComposer` 属性；新增 `@State composerHeight`；按钮/子任务浮层按 `composerHeight` 上推 |
| `Shared/Views/MessageListView.swift` | 新增 `bottomFadeHeight` 常量；底部渐隐 mask 挂在 `messageScrollView`（overlay 之前） |
| `docs/design/chat-composer-overlay/design.md` | 本文件 |

---

## 五、边界与已知上限

- **`viewHeight` 口径**：「回到最新」按钮判定用的 `viewHeight` 取自 `MessageListView` 的
  `.background(GeometryReader)`。`safeAreaInset` **不改变**被修饰视图（ZStack）的帧，只缩安全区，
  故 `MessageListView` 仍铺满整高、`viewHeight` ≈ 中栏整高（含被浮层覆盖的部分）。因此
  `newY > viewHeight * 0.75` 的触发点比旧版（减掉输入栏高度）**略晚**，可接受、不改；
  **真机需确认按钮出现时机无明显滞后**。
- `subagentOverlay` 的 `geo.size.height * 0.78` 现在基于整高，展开面板比原来略高一点点；
  已用 `.padding(.bottom, composerHeight)` 推回输入栏上方，不与输入栏重叠。
- `composerHeight` 初值 0，首帧按钮/浮层会停在窗口底、下一帧才由 `onGeometryChange` 纠正。
  当前初始态 `showScrollToBottom` / `subagentPanelExpanded` 均为 false，**不可见**，风险低；
  若未来这两者初值为真，会出现一帧跳位。
- `composerHeight` 变化未包动画：编辑器长高 / 展开「更多操作」时按钮 padding 会瞬跳
  （`.animation(value: showScrollToBottom)` 不覆盖它）。已确认**不加动画**（更静）。
- 底部渐隐 `bottomFadeHeight = 24`：真机若嫌重/嫌轻，改 `MessageListView` 里该常量即可。

---

## 六、自查与验证

- `swiftc -typecheck`（macOS 26.3 + iOS 26.0，带 `-default-isolation MainActor`）0 error。
- 真机（用户在 Xcode 确认）：
  - 滚动消息时，内容从输入胶囊**下方穿过**、胶囊两侧透出（不再是一行死区）。
  - 滚到底时，最后一条**完整**停在输入栏上方。
  - 「回到最新」按钮、子任务浮层仍在输入栏**上方**，不被盖住；按钮出现时机无明显滞后。
  - 分享选择态（`shareBottomBar`）与分享结果弹窗的整屏压暗**不被渐隐波及**。
  - 编辑器长高 / 展开「更多操作」/ 出现 AskUserBar 时，安全区自动跟随。

---

## 七、后续变更

- **2026-10-10 · 计划便签改由 bottomComposer 独立成行 + 子任务展开面板改内联**
  （用户拍板：计划在上、子任务在下；展开面板内联进底部栈，底部贴输入栏、顶部贴计划便签下缘）
  - 计划便签从 `InputBar` 的 `topTrailingNote` 槽移出，改在 `bottomComposer` 里作为 `planNoteRow`
    独立一行渲染、插在子任务槽之前 → 顺序变为 计划 → 子任务 → L3 → 胶囊（原顺序
    「子任务条 → 计划便签 → 胶囊」反转）。
    - `InputBar.swift`：删除 `TopNote` 泛型参数与 `topTrailingNote`（属性 / 两 init 入参 / 两 body 槽位）。
    - `ChatView.swift`：`planNoteRow`（右对齐、宽度封顶 360）；`bottomComposer` 引用之。
  - 展开态从"覆盖式浮层"改"内联"：删掉 `subagentOverlay` 与 `content` 里
    `.padding(.bottom, composerHeight)`；`bottomComposer` 子任务槽二选一——展开渲染
    `SubagentDetailPanel`（内联），否则 `SubagentStatusBar`。面板天然夹在计划便签与输入栏之间。
    内联在包内容的 `VStack` 里，`ScrollView` ideal 阶段返回内容高 → 自动贴内容；
    外部 `.frame(maxHeight: contentHeight * 0.6)` 封顶，超出内部滚动。
    - `ChatView.swift`（新增 `@State contentHeight` + `subagentPanelMaxHeight`）、
      `SubagentPanel.swift`（`SubagentDetailPanel` 去掉 `maxListHeight`/`PreferenceKey` 实测）。
  - 副作用：内联后无遮罩层，只能靠面板头部「收起」收回（不再支持点空白收起）。
  - 详见 `docs/bugs/032-macos-plan-subagent-overlay-layout.md`。

- **2026-10-10 · L3 待确认条与计划便签合并为「子任务条上方一行」**
  （用户拍板：待确认条放中间、计划便签靠右，同一行；窄窗竖排回退）
  - 新增可复用 `ComposerTopRow<Leading, Trailing>`（`ChatView.swift`）：`ViewThatFits(in: .horizontal)`
    ——宽则 `HStack { Spacer; leading; Spacer(min:12); trailing }`（leading 居中偏左、trailing 靠右），
    窄则 `VStack { trailing(右); leading }`（计划在上、待确认在下）。整体 `maxWidth 720` + 居中 + 底部 6。
  - `bottomComposer` 顺序变为 `AskUserBar → [待确认 + 计划 同行] → 子任务 → 输入栏`：原独立的
    `planNoteRow` 与 L3 待确认块合并进 `composerTopRow`（`planNote` 本体宽度 360 → 300 让位）。
  - diff 预览型待确认条（大卡片）天然走 `ViewThatFits` 竖排回退，不做特殊分支。
  - 顺带去掉 `toolConfirmationBar` 简单分支的横向 padding（改由 `ComposerTopRow` 统一提供，避免双内边距）。
  - 预览：`ChatView.swift` 的 `#Preview("子任务条上方一行 · 宽/窄")`。
