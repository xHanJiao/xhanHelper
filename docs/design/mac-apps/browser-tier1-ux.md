# 浏览器 Tier 1 UX（快捷键 + 节能）

> 日期：2026-08-03
> 分支：browser-tier1-ux
> 范围：macOS 客户端浏览器（右栏）。键盘快捷键（不做 Cmd+L）+ 非激活 tab 节能。

## 一、背景与目标

浏览器已具备基础能力（多 tab、地址栏、后退/前进、刷新），但：

1. **节能差**：`RightSidebarView.contentArea` 把所有 tab 的 `BrowserTabView`（内含 WKWebView）全量放在 `ZStack`，非激活 tab 只是 `opacity(0)` + `allowsHitTesting(false)`，**WKWebView 仍在合成渲染**。多开 tab 时 Mac Air 明显发烫。
2. **快捷键缺失**：没有浏览器专属快捷键，且全局监听（Cmd+`=`/`-` 字号、Cmd+Q 长按）与 SwiftUI `keyboardShortcut`（Cmd+[ ] 聊天历史、Cmd+R 刷新）混用，浏览器态下行为不符合直觉。

本期目标（Tier 1）：
- 节能：非激活 tab 从视图层级摘除，不再合成渲染，页面状态保留。
- 快捷键：Cmd+T 新标签 / Cmd+W 关闭当前 tab / Cmd+Shift+T 恢复关闭的 tab / Cmd+[ ] 后退前进 / Cmd+R 刷新 / Cmd+`=` `-` 页面缩放 / Cmd+F 页内查找（+ Cmd+G 下一个、Cmd+Shift+G 上一个、Esc 关闭）。
- 焦点路由：浏览器聚焦时拦截以上快捷键；聊天/其他区聚焦时保持原行为（聊天历史、全局刷新、字号）。
  （**2026-11-18 起 Cmd+W 不再走焦点路由**：右栏有可见的非首页 tab 就关它，见 §3.2 注。）
- **明确不做**：Cmd+L 聚焦地址栏（用户决定跳过）；地址栏增强（HTTPS 锁/进度/搜索兜底）留待后续。

## 二、用户决策记录

| 决策点 | 结论 |
|--------|------|
| Cmd+L | 不做（本期跳过） |
| 快捷键生效条件 | 浏览器"聚焦"时（见焦点路由） |
| 非浏览器聚焦 | 保持原行为（Cmd+[ ] 聊天历史、Cmd+R 全局刷新、Cmd+± 字号）；Cmd+W 例外，见下一行 |
| Cmd+W 语义 | 右栏打开且当前 tab 不是首页 → 关它（**不看焦点**，网页/文档/书/列表/工具/灵阅同一入口 `provider.closeTab`）；否则有网页焦点 → 关当前网页 tab；都没有 → 吞掉（**不关窗**） |
| 节能手段 | 非激活 tab 摘除视图层级（WKWebView 实例保留，页面状态不丢） |
| 页内查找 | WKWebView 原生 `find(_:configuration:)`（无需自写高亮 JS）。WKFindResult 仅暴露 matchFound（无计数），查找栏只显示命中与否（✓/✗） |
| 实现位置 | AppDelegate 现有 keyDown monitor 扩展 + RightSidebarView contentArea + BrowserFindCoordinator |

## 三、焦点路由（核心）

### 3.1 判定

浏览器聚焦 = 满足全部：

1. `BrowserToolProvider.rightSidebar.activeTabId != nil`（浏览器态，非浏览面板态）；
2. 激活 tab 的 `page.hostingView.window` 是 keyWindow（B2 起从 `webView` 改成内核协议，CEF tab 才判得出来）；
3. firstResponder 判定：
   - firstResponder **在 `page.hostingView` 子树里** → 浏览器聚焦（CEF 的 firstResponder 是 Chromium 自己的子视图，这条最直接）；
   - **正在编辑文本框**（field editor / NSTextView）→ 浏览器聚焦 = 该框是否为浏览器字段（地址栏/查找栏）；
   - **其他**（页面内 WKWebView 及其子视图、窗口、按钮、无焦点）→ 视为浏览器聚焦（对齐 Chrome：有激活 tab 且未在别处输入时，Cmd+W 关 tab）。

被吞掉的 ⌘W 会打一行 `[Shortcut] ⌘W 未处理：…`（含 activePage / window / firstResponder / 两个字段标志），因为这条路径失败时从外部完全看不出原因（只有"按了没反应"）。

"浏览器字段"由 `BrowserTabView` 通过 `@FocusState urlFocused / findFocused` 的 onChange 上报到 `BrowserFocus.isUrlFieldFocused / isFindFieldFocused`（onAppear/onDisappear 重置，防旧 tab 残留）。

> **为何不用 superview 链判定"浏览器容器"**：SwiftUI 窗口整体由单个根 NSHostingView 承载，webView 与聊天输入框同为该宿主视图的子视图（VStack 等不是 NSView），无法靠祖先区分；故改为字段级上报标志。

### 3.2 路由表（AppDelegate keyDown local monitor，注册在字号 monitor 内）

| 按键 | keyCode | 浏览器聚焦 | 非浏览器聚焦 |
|------|---------|-----------|-------------|
| Cmd+T | 17 | openBlankTab() | 透传（无绑定） |
| Cmd+Shift+T | 17+shift | 恢复最近关闭的 tab | 透传 |
| Cmd+W | 13 | 关闭当前 tab | **不看焦点**：右栏打开且非首页 tab → 关它；否则有网页焦点 → 关网页 tab；否则吞掉（不关窗，见下方注） |
| Cmd+[ | 33 | webView.goBack() | 透传（聊天历史） |
| Cmd+] | 30 | webView.goForward() | 透传（聊天历史） |
| Cmd+Shift+[ / ] | 33/30+shift | 右栏上一个 / 下一个 tab（仅侧栏打开时；侧栏收起时**什么都不做**，不退化成后退/前进） | 同左 |
| Cmd+R | 15 | webView.reload() | 透传（全局刷新） |
| Cmd+`=` | 24 | pageZoom += 0.1 | FontSizeManager.increase() |
| Cmd+`-` | 27 | pageZoom -= 0.1 | FontSizeManager.decrease() |
| Cmd+F | 3 | 唤出查找栏 | 透传 |
| Cmd+G | 5 | 查找下一个（栏显示时） | 透传 |
| Cmd+Shift+G | 5+shift | 查找上一个 | 透传 |
| Esc | 53 | 关闭查找栏（栏显示时） | 透传 |

> **Cmd+W 不再看焦点**（2026-11-18，见 `docs/bugs/070`）：侧栏打开、非分屏、当前 tab 不是首页就关它，无论焦点在聊天框、网页还是别处 —— `activeTabId` 就是用户眼前那个 tab。收尾统一走 `BrowserToolProvider.closeTab(id)`（网页 teardown + 恢复栈 / 灵阅恢复栈 / 其余 `onClose`）。曾经的 `ChatInputFocus` 门控会因 SwiftUI `@FocusState` 不跟随 AppKit 焦点而永久为真，把非网页 tab 的关闭路径堵死。
>
> **灵阅（HTML 产物）tab**：它没有浏览器 session，`BrowserFocus` 判不出焦点，所以焦点门控的快捷键默认都不生效。灵阅靠上面这条 Cmd+W（非首页 tab 一律可关）与 **Cmd+Shift+T**（`ArtifactOpenCoordinator.open` 重开，`closedStack` 记 artifactID）覆盖。Cmd+T / Cmd+Shift+T 的触发条件同时放宽为「有 tab **或** 有可恢复项」，否则关掉最后一个 tab 后恢复键完全没反应。缩放 / 刷新 / 页内查找目前仍不作用于灵阅。

## 四、节能：非激活 tab 摘除

`RightSidebarView.contentArea`（浏览器态）由

```swift
ZStack { ForEach(manager.tabs) { tab in tab.content.opacity(...).allowsHitTesting(...) } }
```

改为：

```swift
if let active = manager.tabs.first(where: { $0.id == manager.activeTabId }) { active.content }
```

- WKWebView 实例存于 `BrowserToolProvider.tabs`（`TabSession.webView`），摘除只是脱离视图层级，**页面状态（URL/滚动/DOM）保留**；切回时同一 NSView 重新挂载。
- `BrowserTabView.onAppear` 会重新 KVO 绑定（重新观测 url/isLoading/canGoBack），无泄漏风险。
- 附带收益：活动条/确认条天然只显示在激活 tab。
- 可选增强：tab 切走时对旧 session 调 `pauseAllMediaPlayback()`，切回不再强制恢复。

## 五、快捷键实现（AppDelegate）

### 5.1 现有结构

`App.swift`（`xHelperMac`）`AppDelegate.applicationDidFinishLaunching` 已有三个 monitor：
1. keyDown：Cmd+Q（keyCode 12）→ 长按退出；
2. keyUp：Cmd+Q 松键取消；
3. keyDown：Cmd+24 / Cmd+27 → 全局字号。

扩展 **monitor 3**：先判浏览器聚焦 → 命中路由表则处理并 `return nil`（事件不再下发，SwiftUI `keyboardShortcut` 与默认菜单行为都不会触发）；否则回退到原字号逻辑。

### 5.2 新文件

`BrowserFocus.swift`：`browserIsFocused()` + 浏览器字段聚焦标志（BrowserTabView 上报）。

`BrowserFindCoordinator`（@MainActor ObservableObject，shared 单例）：
- `@Published isPresented / query / matchFound`；`weak var webView`。
- `requestShow(_ wv:)`（Cmd+F）、`close()`（Esc）、`next()/previous()`（Cmd+G / Shift+Cmd+G）、`queryChanged()`（输入变化）。
- `webView.find(query, configuration: WKFindConfiguration(backwards: !forward, wraps: true))` 回调刷新 matchFound。

### 5.3 BrowserToolProvider 新增

- `func closeActiveTab()`：复用 handleCloseTab 的收尾逻辑（cancelLoadWait → 侧栏 closeTab → 移除 → 关激活页自动切最近 tab）。
- `func restoreLastClosedTab()`：`closedStack`（url/title）弹出恢复。
- `private var closedStack: [(url: URL?, title: String)]`：`openTab/close` 时压栈，`closeAll` 清空。

### 5.4 BrowserTabView 新增

- `@ObservedObject findCoordinator = BrowserFindCoordinator.shared`；onAppear 时 `attach(webView)`。
- 查找栏：工具栏下方，含输入框（focus 唤起）、命中标识（✓/✗）、上一个/下一个/关闭按钮。
- 从 URL 字段下拉替换或独立一行（独立一行，避免干扰地址栏）。

## 六、恢复关闭的 tab（Cmd+Shift+T）

- `closeTab`（UI 按钮与工具 `close_tab`）压栈 `(url, title)`；`closeAll`（`agent_browser_close`）清空栈。
- 恢复：`BrowserToolProvider.openURL(url)` 复用现有 open 流程（新 tab、加载 URL、同步侧栏）。

## 七、验证

- 打开 ≥2 个 tab，切后台 tab：确认 WKWebView 不再渲染（Activity Monitor CPU/GPU 下降、Xcode 视图调试器确认脱层）。
- 切回 tab：URL/滚动位置/页面状态保留。
- 浏览器聚焦时各快捷键生效；聊天聚焦时 Cmd+[ ] 仍为聊天历史、Cmd+± 改字号。
- Cmd+W：聊天框里点一下 → 开文档 tab → 能关（`docs/bugs/070` 的回归）；侧栏收起时按 Cmd+W 不关任何 tab。
- 页内查找：Cmd+F 唤出、输入高亮计数、Cmd+G/Shift+Cmd+G 跳转、Esc 关闭。
- Cmd+Shift+T 恢复最近关闭 tab。
- 回归：agent 浏览器工具链（open/switch_tab/close_tab/snapshot/click）在节能改动后仍正常作用于激活 session。
