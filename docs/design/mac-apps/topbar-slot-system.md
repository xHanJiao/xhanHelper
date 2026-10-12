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
HStack(spacing: Layout.topBarGroupSpacing) {
    Spacer().frame(width: Layout.topBarTrafficClearance)   // 红绿灯让位（顶栏铺到标题栏）
    conversationSwitcher(interactive:labelCap:)            // 当前会话下拉 + ＋（常驻，不走槽位系统）
    if flexible { Spacer(minLength: 0) } else { Spacer().frame(width: 0) }   // 弹性撑满 / 探针 0 宽
    if plan.showsDevChip { devChip }                       // 仅 macOS dev 构建
    if plan.showsCenter { renderCenterSlot(plan:interactive:) }
    if plan.showsCenter && 中/右槽都有可见组 { UnresizableDivider() }
    renderRightSlot(plan:interactive:)
}
.padding(.horizontal, Layout.toolbarHorizontalPadding)
```

`plan` = `TopBarLayout.plan(available: availableWidth, trailingLimit: trailingLimit, model: widthModel)`（`TopBarView` 里的计算属性）。
真实栏（`flexible: true, interactive: true`）与隐藏探针（`false, false`）**共用这一套骨架** → 几何必然同档同宽；
探针只是把弹性 `Spacer` 换成 0 宽、把 `Button` 换成静态图标。
（`Layout.topBarTrafficClearance` 让出红绿灯；实际常量以 `LayoutConstants.swift` 为准。）

`renderCenterSlot`（中心槽；右槽走 `renderRightSlot`）→ `registry.visibleGroups(for:)` → 每个 Group 一个 `CapsuleGroup` / `OverflowCapsuleGroup`（复用胶囊样式），内部 `ForEach(buttons)` 渲染 `CapsuleButton` / `CapsuleRefreshButton`，按钮间用 `Divider` 分隔。

**按钮 hover 圆底**（2026-10-12）：直径 `Layout.topBarHoverCircleSize` = **22**，由 `CapsuleButtonBackground` 给 `Circle()` 自己定尺寸。
原按图标盒整格（26）画 → 胶囊内高也是 26，上下只剩 1pt、看着贴到胶囊边。缩圈**不改命中区**（仍 26pt 图标盒）、
**不动胶囊高度**（仍 28）、不进宽度账（背景不占位）；`CapsuleButton` / `CapsuleRefreshButton` / 两处 `⋯` 全走这条路，一处生效。

### 五之二、降级档 + `⋯` 溢出（2026-10-11）

顶栏**不再决定窗口最小宽**，改为按可用宽（`TopBarView.availableWidth` = 窗口内容宽）自适应降级：
档位 = DEV 徽标 → 中心槽组 → 下拉标签收窄，被收的按钮进右槽最左的 `⋯`（原生 `Menu`，贴在 ⌘1–5 导航组左侧）。
判定是**纯函数**（`Shared/Views/TopBarLayout.swift`，按钮统一 26pt icon 故可算不必量），
真实栏与隐藏探针必然同档；探针（`TopBarMinWidthKey`）降级为安全网。
完整宽度账、档位阈值、验收项见 `../mac-topbar-degrade/design.md`。

**`⋯` 落位（2026-10-12 起）**：中心组**留了一部分**时它长在那一组的**末尾**（同一颗胶囊、组内分隔线），
只有**一个都没留下**时才退回右槽最左那颗独立胶囊。
它是**可选行为**：组自己声明 `TopBarGroup.autoOverflow`（默认关），今天只有两个中心组开着；
右槽导航 / 工具组不开 → 永不变形（`⌘1–5` 与设置始终在栏上）。
细节：`../mac-topbar-degrade/design.md` §3.3 要点 5、`../overflow-collapse/design.md`（人话版）。

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

### 2026-10-11 · 主窗口红绿灯右下微移（纵向值 2026-10-12 更新为 19）

- **决定**：主窗口三个窗口按钮整体右移 6 / **下移到距窗顶 19**（`AppDelegate.trafficLightNudge`；y 由 `Layout.trafficLightTopFromWindow` 派生 = `9 − 19` = **−10**，改顶栏上边距/胶囊高会自动跟随）。系统没有公开的偏移 API，只能改按钮 frame；AppKit 每次重排标题栏（缩放、改标题、进出全屏）都会复位，故在 `windowDidResize` / `windowDidExitFullScreen` 之后按记下的系统基准重写（当前值 ≠ 上次写入值 → 当作新基准）。
  - ⚠️ **纵向值调整过两次**：先是"下移 4"（距窗顶 13），后为「与顶栏内容**同一行居中**」改成距窗顶 **23**（`y = −14`）；**2026-10-12** 顶栏胶囊 36 → 28，派生值随之变 **19**（`y = −10`，探出标题栏容器的量由 5pt 降到 1pt）。推导与依赖写在 `App.swift` 注释里，几何账见 `../mac-card-top-wrap/design.md` §二。
- **影响**：`Layout.topBarTrafficClearance` 改为由 `Layout.trafficLightNudgeX` 派生（56 → 62），顶栏内在宽随之 +6 —— 顶栏是绑定约束时窗口最小宽也 +6（属宽度账的正常传导）。纵向偏移不影响让位常量。
- **已知陷阱**：改窗口标题同样会复位、且**不触发** `didResize`。主窗口标题首启后不再设置，故当前无碍；若日后出现弹回，按 `client/xHelper/xHelperMac/App.swift` 里的 `shortcut:` 注释加 `NSWindow.didUpdateNotification` 兜底。

### 2026-10-12 · 顶栏胶囊常驻阴影统一

- **问题**：顶栏三种胶囊里只有 `CapsuleGroup`（导航组 / 工具组 / `⋯`）有常驻阴影，左侧「会话下拉」「＋」没有 → 同一个顶栏里"有影/无影"并存。
- **决定**：**给左侧两处补上同参常驻阴影**（`黑 12% / radius 4 / y 2`，与 `CapsuleGroup` 逐字一致），DEV 徽标不加（它是 tinted 底徽标、不是按钮）。
  - **不要**反向"都去掉阴影"：试过（`/tmp/pill_shadow.png` 第 2/3 格）—— 材质（`.ultraThinMaterial` + 0.5pt 白描边）在卡片底 / 照片底上对比不足，**去掉投影后胶囊看不出是按钮**。层级上是「卡片抬起来（`CardElevationShadow`）、按钮靠自身投影立住」。
  - 自然后果：投影挂在半透明材质上，胶囊内缘会带一点暗（右侧本来就这样），三处因此观感一致。
- **影响文件**：`Shared/Views/TopBarView.swift`（`conversationSwitcher` 的两处 `.shadow`）、
  `xHelperMac/TwoColumnLayoutPreview.swift`（预览那三个胶囊也补上同款，画布才跟生产一致）。
- **未做（记一笔）**：左侧「下拉」「＋」至今 **hover 无任何反馈**（右侧组内按钮有图标圆底 + 整组抬起）；要不要补留给后续决定。
