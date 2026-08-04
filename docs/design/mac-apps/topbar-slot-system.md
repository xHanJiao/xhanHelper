# 通用顶栏（槽位 + 优先级）设计

> 日期：2026-08-04
> 分支：browser-tier1-ux
> 状态：设计定稿，待施工
> 目的：把分散在各处的顶栏按钮（聊天历史、刷新、设置、侧栏开关、浏览器 tab 栏 7 按钮）统一成"槽位 + 优先级"的通用注册系统，解耦功能模块与顶栏布局，为将来 PDF 阅读器等新功能区留扩展位。

## 一、架构

```
TopBarRegistry（@MainActor 单例，ObservableObject）
  ├─ register(Group) / unregister(id:)
  ├─ visibleGroups(for: Slot) → 取最高优先级组（同优先多组共存）
  └─ centerSlotHasGroup: Bool

TopBarView（替换 NavigationToolbar）
  ├─ 观察 registry / rightSidebar / chatTabManager / router
  └─ 渲染 左/中/右 三槽位（每槽一个 CapsuleGroup）

RightSidebarView
  └─ 观察 registry.centerSlotHasGroup → 条件隐藏浏览器 tab 栏按钮
```

## 二、模型

```swift
enum Slot { case left, center, right }

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

**推演（用户确认）**：浏览器组 + PDF 阅读器组同时注册 priority 10 的左槽 [<][>]（切 tab / 翻页）→ 同槽共存、都展示。

## 三、组定义

### 默认组（priority 0，ContentView.onAppear 注册一次，永不注销）

| 槽位 | 按钮 |
|------|------|
| 左 | `[<][>]` 聊天历史（仅 chat tab enabled，`chatTabManager` 驱动）+ `[刷新]`（`router.refresh()`，enabled=`router.canRefreshCurrentTab`，isRefresh） |
| 中 | 空 |
| 右 | `[🌐]`（enterBrowser+PiP）+ `[⚙]`（设置）+ `[侧栏]`（rightSidebar.toggle） |

⚠️ 右槽位**不含刷新**（刷新在左槽，去重）。

### 浏览器组（priority 10，BrowserToolProvider 管理生命周期）

| 槽位 | 按钮 |
|------|------|
| 左 | `[<][>]` tab 上一个/下一个（`rightSidebar.switchTab(delta:)`）+ `[刷新]`（`activeWebView?.reload()`） |
| 中 | `[PiP][清数据][ + ][下载][ ⭐ ][书签][ 🕐 ]`（从浏览器 tab 栏挪上来） |
| 右 | 空（沿用默认组右槽） |

**右槽位默认组优先级设 priority 5**（高于默认 0、低于浏览器 10）——右槽是重要功能（🌐/⚙/侧栏），防被误覆盖。

**< > 自动切换行为**：浏览器组存在（priority 10 > 0）→ 左槽取浏览器组 → `< >` 切 tab；无浏览器 → 退回默认组 → 聊天历史。与右侧栏/具体功能区解耦。

**仅浏览器时显示**：开 tab 注册、关完 tab 注销 → 自然满足"仅浏览器有 tab 时显示"。

## 四、生命周期

| 事件 | 动作 |
|------|------|
| ContentView.onAppear | 注册默认组（左/右，priority 0/5） |
| 第一个 tab 打开 | `BrowserToolProvider.syncBrowserGroup()` → 注册浏览器组（左/中，priority 10） |
| 最后一个 tab 关闭 | `syncBrowserGroup()` → 注销浏览器组 |
| 浏览器组注册 | RightSidebarView 观察到中槽有组 → 隐藏 tab 栏 7 按钮 |
| 浏览器组注销 | 无 tab → 浏览面板 → tab 栏不渲染，无影响 |

## 五、TopBarView 渲染

```
HStack(spacing: 8) {
    Spacer().frame(width: navLeadingOffset)   // 清红绿灯
    renderSlot(.left)
    renderSlot(.center)
    Spacer()
    renderSlot(.right)
}
.padding(.horizontal, Layout.toolbarHorizontalPadding)
.padding(.vertical, 6)
.ignoresSafeArea(edges: .top)   // 上推到红绿灯条带
```

`renderSlot` → `registry.visibleGroups(for:)` → 每个 Group 一个 `CapsuleGroup`（复用胶囊样式），内部 `ForEach(buttons)` 渲染 `CapsuleButton` / `CapsuleRefreshButton`，按钮间用 `Divider` 分隔。

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
4. 地址栏 `< >` 由用户确认可去掉（顶栏已有 `< >`）。
5. 清数据逻辑从 RightSidebarView 移入 `BrowserToolProvider.clearBrowsingData()`（static，present sheet）。
6. 实现顺序：TopBarRegistry → TopBarView → 默认组注册（验证渲染）→ 浏览器组注册（验证切换）→ 清理旧文件。
