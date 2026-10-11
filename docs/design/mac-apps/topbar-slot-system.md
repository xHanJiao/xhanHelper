# 通用顶栏（槽位 + 优先级）设计

> 日期：2026-08-04
> 分支：browser-tier1-ux
> 状态：设计定稿，待施工
> 目的：把分散在各处的顶栏按钮（聊天历史、刷新、设置、侧栏开关、浏览器 tab 栏 7 按钮）统一成"槽位 + 优先级"的通用注册系统，解耦功能模块与顶栏布局，为将来 PDF 阅读器等新功能区留扩展位。
>
> ⚠️ **现状更新（2026-09）**：下方为最初设计稿，有两处已变，阅读时以本节为准——
> 1. **左槽已删除**：`TopBarSlot` 只剩 `.center` / `.right`，`TopBarView` 只渲染中/右两槽。顶栏左区改为**不走槽位系统的 leading View**（现为会话下拉），见 `../mac-two-column-layout/design.md`。
> 2. **浏览器组注册在「中槽」**（`priority: 10`，7 按钮、无 PiP），不再是左槽；文档组（中槽 `priority: 8`）在浏览器组隐藏时自动让位。默认组右槽 `priority: 5`（macOS 另有 `default-utility` 同为右槽 5，同优先共存）。
>
> 因此下文所有"左槽"字样均不再适用；槽位表内已就地标注。

## 一、架构

```
TopBarRegistry（@MainActor 单例，ObservableObject）
  ├─ register(Group) / unregister(id: / prefix:)
  ├─ visibleGroups(for: Slot) → 取最高优先级可见组（同优先多组共存）
  └─ refresh() → 触发重算

TopBarView（替换 NavigationToolbar）
  ├─ 观察 registry / rightSidebar / chatTabManager 等
  └─ 渲染 中/右 两槽位（每槽一个 CapsuleGroup）；左区为 leading View（不走槽位）
```

## 二、模型

```swift
enum Slot { case center, right }   // 原为 left/center/right，左槽已删除

struct Group {
    let id: String        // unregister 用（同 id 覆盖）
    let slot: Slot
    let priority: Int     // 越高越优先
    let buttons: [ButtonDef]
}

struct ButtonDef {
    let id: String
    let icon: () -> String   // 每次渲染动态取（如 ⭐ 实心/空心）
    let enabled: () -> Bool  // 动态（如聊天历史可否后退）
    let action: () -> Void
    let help: () -> String
    let isRefresh: Bool      // true → CapsuleRefreshButton（旋转动画）
}
```

**取显规则**：每槽位取 `maxPriority` 的所有组；**同最高优先级多组共存**（按注册顺序拼接，组间分隔）。低优先级组被隐藏（不是删除）。

**推演（用户确认）**：浏览器组 + PDF 阅读器组同时注册 priority 10 的同槽 [<][>]（切 tab / 翻页）→ 同槽共存、都展示。

### 组定义（现状）

### 默认组（`TopBarView.registerDefaultGroups()`，启动注册一次，永不注销）

| 槽位 | 按钮 | priority |
|------|------|----------|
| 中 | 空 | — |
| 右 | `default-right`：文档/记忆等（`⌘1/2/3/5`）；`default-utility`（仅 macOS）：`[⤢最大化][侧栏][🌓][⚙]` | 5（两组同优先共存） |

⚠️ 右槽**不含刷新**（用户确认默认 `< >` 刷新无意义，去掉）。

### 浏览器组（priority 10，`BrowserToolProvider.registerBrowserGroups()`）

| 槽位 | 按钮 |
|------|------|
| 中 | `[清数据][ + ][下载][配置][收藏夹栏][书签][🕐]`（7 按钮；PiP 已由「最大化侧栏」按钮覆盖；无 `< >`，切 tab 用 `⌘⇧[/]`） |
| 右 | 空（沿用默认组右槽） |

- **visible 闭包**：右栏正显示浏览器 tab（`activeTabId != nil && !tabs.isEmpty && isOpen`）时整组显示，返回浏览面板即隐藏。
- **文档组**（`priority: 8`，`DocumentSelectionCoordinator`）：同为中槽，低于浏览器组；浏览器组隐藏时自动让位显示。
- **让位规则**：中槽取**最高优先级的可见组**（`TopBarRegistry.visibleGroups`），浏览器组（10）不可见 → 落到文档组（8）。

## 四、生命周期

| 事件 | 动作 |
|------|------|
| ContentView 启动 | 注册默认组（`default-right` 右槽 5；macOS 另有 `default-utility` 右槽 5） |
| 第一个 tab 打开 | `BrowserToolProvider.registerBrowserGroups()` → 注册浏览器组（中槽，priority 10） |
| 最后一个 tab 关闭 | 浏览器组 `visible` 闭包自动隐藏（注册一次，不注销） |
| 浏览器组可见 | 同上（`activeTabId` 变化 → `TopBarRegistry.refresh()`） |

## 五、TopBarView 渲染

```
HStack(spacing: Layout.toolbarCapsuleGap) {
    conversationSwitcher()                    // 左区 leading View（不走槽位系统）
    Spacer(minLength: 0)
    renderSlot(.center)
    Spacer(minLength: 0)
    renderSlot(.right)
}
.padding(.horizontal, Layout.toolbarHorizontalPadding)
```
（`Layout.topBarTrafficClearance` 让出红绿灯；实际常量以 `LayoutConstants.swift` 为准。）

`renderSlot` → `registry.visibleGroups(for:)` → 每个 Group 一个 `CapsuleGroup`（复用胶囊样式），内部 `ForEach(buttons)` 渲染 `CapsuleButton` / `CapsuleRefreshButton`，按钮间用 `Divider` 分隔。

### 五之二、降级档 + `⋯` 溢出（2026-10-11）

顶栏**不再决定窗口最小宽**，改为按可用宽（`TopBarView.availableWidth` = 窗口内容宽）自适应降级：
档位 = DEV 徽标 → 中心槽组 → 下拉标签收窄，被收的按钮进右槽最左的 `⋯`（原生 `Menu`，贴在 ⌘1–5 导航组左侧）。
判定是**纯函数**（`Shared/Views/TopBarLayout.swift`，按钮统一 26pt icon 故可算不必量），
真实栏与隐藏探针必然同档；探针（`TopBarMinWidthKey`）降级为安全网。
完整宽度账、档位阈值、验收项见 `../mac-topbar-degrade/design.md`。

**按钮 enabled/icon/help 实时更新**：TopBarView `@ObservedObject registry, rightSidebar, chatTabManager, router`，任一 publish → re-render → `ButtonDef` 的 closure 重新求值。

## 六、改动文件

| 文件 | 改动 |
|------|------|
| 新增 `Shared/Services/TopBarRegistry.swift` | 注册中心 + Slot/Group/ButtonDef |
| 新增 `Shared/Views/TopBarView.swift` | 顶栏渲染（替代 NavigationToolbar） |
| 删除 `Shared/Views/NavigationToolbar.swift` | 被 TopBarView 取代 |
| `Shared/Views/ContentView.swift` | NavigationToolbar → TopBarView；.onAppear 注册默认组 |
| `Shared/Tools/BrowserToolProvider.swift` | `syncBrowserGroup()`、`clearBrowsingData()`；tab 生命周期调用 sync |
| `Shared/Views/NavigationRouter.swift` | 删除 ToolbarAction/toolbarActions/currentToolbarActions/setToolbarActions |
| `Shared/Views/RightSidebarView.swift` | 删除 tab 栏 7 按钮 + confirmClearBrowsingData（移入 Provider） |

## 七、边界 & 注意事项

1. `register` 同 id + slot 覆盖更新（幂等）。
2. 同槽同优先多组共存（Divider 分隔）。
3. 默认组右槽 priority 5（重要功能保护）。
4. 浏览器组「注册一次 + `visible` 闭包判状态」，不做 register/unregister 切换显隐。
5. 清数据逻辑从 RightSidebarView 移入 `BrowserToolProvider.clearBrowsingData()`（static，present sheet）。
6. 实现顺序：TopBarRegistry → TopBarView → 默认组注册（验证渲染）→ 浏览器组注册（验证切换）→ 清理旧文件。

## 八、后续变更

### 2026-10-11 · 主窗口红绿灯右下微移

- **决定**：主窗口三个窗口按钮整体右移 6 / 下移 4（`AppDelegate.trafficLightNudge`）。系统没有公开的偏移 API，只能改按钮 frame；AppKit 每次重排标题栏（缩放、改标题、进出全屏）都会复位，故在 `windowDidResize` / `windowDidExitFullScreen` 之后按记下的系统基准重写（当前值 ≠ 上次写入值 → 当作新基准）。
- **影响**：`Layout.topBarTrafficClearance` 改为由 `Layout.trafficLightNudgeX` 派生（56 → 62），顶栏内在宽随之 +6 —— 顶栏是绑定约束时窗口最小宽也 +6（属宽度账的正常传导）。
- **已知陷阱**：改窗口标题同样会复位、且**不触发** `didResize`。主窗口标题首启后不再设置，故当前无碍；若日后出现弹回，按 `client/xHelper/xHelperMac/App.swift` 里的 `shortcut:` 注释加 `NSWindow.didUpdateNotification` 兜底。
