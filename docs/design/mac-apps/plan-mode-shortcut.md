# 计划模式 Tab 快捷键（macOS）

> 日期：2026-09-15
> 范围：macOS 客户端。为已存在的会话级计划模式（`Conversation.Mode = "plan"`）补一个 opencode 风格的 Tab 一键切换。

## 背景

计划模式当前只能通过输入框内的 `ModePill` 胶囊点击切换（`ChatView.swift:156` → `ChatViewModel.toggleMode()` `ChatViewModel.swift:310`）。`ChatViewModel.swift:128` 注释早已声称「Tab 快捷键/胶囊条」，但 Tab 从未接线。

## 决策：Tab 仅在聊天输入框聚焦时生效

全局 Tab 劫持不可行，会撞：L3 工具确认（Tab=允许，`App.swift:40`）、地址栏自动补全（Tab=采纳，`BrowserTabView.swift:197`）、浏览器网页焦点导航、系统焦点键循环。

限定在聊天 `TextEditor` 聚焦时，则以下危险场景自动规避：

- PiP 收起态（悬浮球，无 InputBar）→ 不触发
- 浏览器/地址栏聚焦 → 不触发
- 右栏非浏览器面板、左栏、模态 → 不触发
- L3 确认优先级不受影响：AppDelegate 的 NSEvent 局部监听器先于视图分发消费 Tab

## 改动

1. `InputBar.swift`：新增 `onToggleMode: () -> Void`（两个 init 都补，便利 init 默认 `{}`）；在 `editor` 的 TextEditor 上紧邻 `.onKeyPress(.return)` 加 `.onKeyPress(.tab, phases: .down) { onToggleMode(); return .handled }`。
2. `ChatView.swift`：传入 `onToggleMode: { Task { await viewModel.toggleMode() } }`。
3. `ChatViewModel.toggleMode()`：补 Toast 反馈（`已进入计划模式（只读讨论）` / `已退出计划模式`），与 `toggleTrustedMode()` 一致。
4. `ShortcutGuideView.swift`：聊天输入段补 `Tab → 切换计划 / 聊天模式`。

## 已知边界

- 流式中 TextEditor 被 `.disabled(isStreaming)`，Tab 不触发（胶囊按钮仍可点）。可接受。
- **不确定点**：SwiftUI 焦点系统可能先吞掉 Tab，需真机验证 `.onKeyPress(.tab)` 是否触发。
- **兜底**（仅在上面失效时启用）：`InputBar` 的 `@FocusState` 变化写入 `AppShortcutBridge.isChatInputFocused` 标记，AppDelegate 监听器仅当标记为真时消费 Tab。

## 验收

- 真机：输入框聚焦按 Tab，模式在聊天/计划间切换，胶囊与输入框光晕同步变化，Toast 提示。
- 真机确认：PiP 收起态、浏览器/地址栏聚焦时按 Tab 无副作用。
- 客户端编译/运行由用户在 Xcode 完成（约定）。

## 后续变更

- 2026-10-14：计划模式撤掉严格档、恢复二态（关/计划），Tab 循环序变为 关↔计划；本文其余内容不变。详见 `docs/design/plan-mode-two-state/design.md`。
