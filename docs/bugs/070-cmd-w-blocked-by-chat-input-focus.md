# 070 · ⌘W 关不掉右栏的非网页 tab（被聊天输入框焦点闸死）

- 日期：2026-11-18
- 平台：macOS 客户端
- 相关：`docs/design/right-sidebar-unified-tabs/design.md`（右栏统一 tab）

## 问题表现

右栏停在文档 / 书架 / 列表 / 工具页这类**非网页 tab** 时按 ⌘W 完全没反应；
换成网页 tab 却能关。先在中栏聊天输入框里点一下（光标出现）再去开那个文档 tab，
⌘W 必定失效；此时**再点一下右侧栏**（焦点落进 WKWebView）后，⌘W 又恢复正常。
关不掉时事件仍被吞（不会退给系统「关闭窗口」）。

## 根因

1. `89b4386d`（评审 P1/P2 修复）在 `xHelperMac/App.swift` 的 ⌘W 分支上加了
   `!ChatInputFocus.shared.isFocused`。
2. `ChatInputFocus.shared.isFocused` 只由 `InputBar` 里 `TextEditor` 的 `@FocusState`
   写入（`InputBar.swift:256`）。焦点移到**非 SwiftUI 的 AppKit 视图**（右栏 WKWebView /
   CEF 页面视图）或点 tab 条按钮（macOS 按钮默认不抢 first responder）时，
   `@FocusState` 不保证回 `false` —— 标记粘住，之后 ⌘W 一直被拦。
3. 为什么只有网页 tab 还能关：网页 tab 有第二条路（`else if BrowserFocus.isFocused()`），
   非网页 tab 只存在于被拦住的第一入口。
4. 同处第二个静默失效：网页分支里又套了 `if BrowserFocus.isFocused()`，
   「网页 tab 激活但页面没聚焦」时事件被吞、什么都不关。

## 方案

`xHelperMac/App.swift`：⌘W 的侧栏分支**不看键盘焦点** —— `activeTabId` 就是用户眼前那个 tab，
焦点停在哪不改变这份意图（Chrome 语义：⌘W 永远关当前标签页）：

- 删 `!ChatInputFocus.shared.isFocused`，删网页分支里的 `BrowserFocus.isFocused()`。
- 三条收尾合并成 `BrowserToolProvider.shared.closeTab(id)`：它内部已覆盖
  网页 session teardown + 恢复栈、灵阅恢复栈、其余 `manager.closeTab`（含 `onClose`）。
- 保留：侧栏收起不关、分屏下退回浏览器语义、`BrowserFocus` 只在「侧栏没 tab」的兜底里用。
- 配套：`BrowserFocus.diagnosis()` 加 `chatInput=` 字段（这块以前是盲的，日志看不出被焦点挡了）。

`ChatInputFocus` 本身不动 —— ⌘V 的图片拦截仍然需要它（那条路径只在真正聚焦时才有意义）。
