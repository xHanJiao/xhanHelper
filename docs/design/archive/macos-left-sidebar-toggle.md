# 左侧栏完全隐藏 + 左缘 hover 热区展开（macOS）

> ⚠️ **已废弃（2026-09 归档）**：左栏已被整体删除，本文所述交互（左缘热区、`⌘B`、`isSidebarHidden`、`LeftEdgeExpandZone`）均不再存在。
> 现行方案见 `docs/design/mac-two-column-layout/design.md`（删左栏 + 会话切换移至顶栏下拉）。本文仅作历史记录。

## 目标

左栏「收起」时**完全不可见**（侧栏卡片、内边距、与中栏的缝隙、缝线全部消失，中间栏左移撑满），收起状态下**顶栏无按钮**；在窗口左缘提供 hover 热区，悬停时出现圆形 `>` 按钮点击展开侧栏。

## 已确认的设计决策（勿改）

1. **收起交互**：拖缝隙/⌘B 把 `sidebarWidth` 收至最小宽 38 → 完全隐藏（无任何残留宽度）。窄轨（38~120）仅在手动拖拽到该区间时显示。
2. **隐藏态无顶栏按钮**：顶栏左槽不注册任何按钮。
3. **展开入口**：隐藏时窗口左缘 28px 透明热区，悬停显示圆形 `>` 按钮（内容区垂直居中），点击恢复记忆宽度；Cmd+B 也可展开。移出热区按钮消失。
4. **窗口最小宽计算保持不变**：`baseMinWidth` / `pipMinContentWidth` / `rightSidebarMaxAvailable` 均不动。
5. **无动画**（热区按钮的淡入缩放过渡除外）、隐藏态**不持久化**（重启恢复展开）、窄窗下展开无反馈（沿用现状）。

## 背景

- 左栏宽度 = `ContentView.sidebarWidth`（@State）。收起时被设为 `Layout.sidebarMinWidth = 38`；展开时 `toggleLeftSidebar()` 恢复 `storedSidebarWidth`（记忆宽度）。
- Cmd+B 由 AppDelegate NSEvent monitor → `AppShortcutBridge.shared.onToggleLeftSidebar` → `ContentView.toggleLeftSidebar()`（ContentView.swift:302），toggle 逻辑零改动。
- 布局结构（ContentView macOS 分支，GeometryReader 内 HStack）：
  `HSpace(cardGap)` → `SidebarView`（+内边距+卡片背景） → `HSpace(seamHalfGap)` + `SeamMarker` + `HSpace(seamHalfGap)` → 中间栏。
  侧栏与缝线部分包进条件渲染，**第一行的 `HSpace(Layout.cardGap)`（窗口左侧 6px 边距）必须留在条件外**。
- 热区挂在 HStack 的 `.overlay(alignment: .leading)` 上，仅在 `isSidebarHidden` 时渲染。

## 改动清单

| # | 文件 | 位置 | 改动 |
|---|------|------|------|
| 1 | `client/xHelper/xHelper/Shared/Views/ContentView.swift` | 共 4 处 | 新增 `isSidebarHidden`；侧栏块条件渲染；左拖拽手柄加隐藏条件；新增左缘热区 overlay + `LeftEdgeExpandZone` |
| 2 | `docs/design/macos-left-sidebar-toggle.md` | 本文件 | 交互方案迭代（顶栏按钮 → 左缘热区） |

> 迭代说明：初版为「顶栏左侧切换按钮」（`Layout.topBarTrafficClearance` + `default-left` 槽位），随后用户反馈按钮孤悬突兀，改为「隐藏时无按钮 + 左缘 hover 热区」。相关常量/注册已回退删除。

### ContentView.swift

**A. 新增 `isSidebarHidden`**（锚点 `private var allowExpand: Bool { windowWidth >= 600 }` 之后）：
```swift
    /// 侧栏完全隐藏判定：仅收起动作（sidebarWidth 落到最小宽 38）触发；
    /// 手动拖拽到 38~120 之间仍显示窄轨，不隐藏。
    private var isSidebarHidden: Bool {
        sidebarWidth <= Layout.sidebarMinWidth
    }
```

**B. 侧栏块条件渲染**（`HSpace(Layout.cardGap)` 留在 if 外，其余包进 `if !isSidebarHidden` 并整体右移 4 空格）：
```swift
                            HSpace(Layout.cardGap)

                            if !isSidebarHidden {
                                SidebarView(
                                    selectedTab: sidebarTabBinding,
                                    sidebarWidth: $sidebarWidth,
                                    chatTabManager: chatTabManager,
                                    conversationListVM: conversationListVM,
                                    defaultConversationID: deviceManager.defaultConversation?.id
                                )
                                .frame(minWidth: Layout.sidebarMinWidth)
                                .padding(.horizontal, Layout.sidebarHPadding)
                                .modifier(CardBackgroundModifier(isLifted: sidebarHovered))
                                .layoutPriority(3)
                                .onHover { sidebarHovered = $0 }

                                HSpace(Layout.seamHalfGap)
                                SeamMarker(x: $leftSeamX)
                                HSpace(Layout.seamHalfGap)
                            }
```
紧随其后的 `if !rightSidebar.isPipChat {`（中间栏）不动。

**C. 左拖拽手柄**（`if allowExpand {` → `if allowExpand && !isSidebarHidden {`）：
```swift
                                if allowExpand && !isSidebarHidden {
                                    ResizableDivider(
                                        id: "left",
```

**D. `navLeadingOffset` 置 0**（无左槽按钮后偏移无意义）：
```swift
                    TopBarView(
                        rightSidebar: rightSidebar,
                        navLeadingOffset: 0
                    )
```

**E. 左缘热区 overlay**（HStack 闭合 `}` 之后、`.coordinateSpace(name:)` 之后）：
```swift
                        .overlay(alignment: .leading) {
                            // 左栏完全隐藏时，左缘 hover 热区：悬停显示圆形展开按钮
                            if isSidebarHidden {
                                HoverRevealButton(systemName: "chevron.right",
                                                  tooltip: "展开左侧栏 (⌘B)",
                                                  action: { toggleLeftSidebar() })
                            }
                        }
```

**F/G. 公共组件**：新增 `Shared/Views/HoverRevealButton.swift`（分离命中层与视觉层，思路沿用早期左侧栏收起手柄 82b9d30；替代最初的内联 `LeftEdgeExpandZone`）：
```swift
import SwiftUI

/// 边缘悬停热区 + 浮现按钮（分离命中层与视觉层）。
///
/// - 命中层：底部透明热区条（沿 `axis` 方向铺开，宽/高 `zoneWidth`，整条可命中），负责 hover 检测与点击。
/// - 视觉层：圆形按钮 `.allowsHitTesting(false)`，只镜像 hover 状态，不参与命中。
///   因此悬停热区 → 按钮浮现；指针移向按钮不会让按钮消失（按钮不拦截命中）。
///
/// `axis` 决定热区朝向：`.horizontal` 为竖直条（贴左/右边缘），`.vertical` 为水平条（贴上/下边缘）；
/// 图标方向由 `systemName` 决定（如 chevron.left/right/up/down），与热区朝向相互独立。
struct HoverRevealButton: View {
    let systemName: String
    let tooltip: String
    let action: () -> Void
    var zoneWidth: CGFloat = 48
    var extent: CGFloat = 120
    var buttonSize: CGFloat = 32
    var axis: Axis = .horizontal

    @State private var isHovered = false

    var body: some View {
        ZStack(alignment: .center) {
            Color.clear
                .frame(width: axis == .horizontal ? zoneWidth : nil)
                .frame(height: axis == .vertical ? zoneWidth : nil)
                .frame(maxWidth: axis == .vertical ? extent : nil,
                       maxHeight: axis == .horizontal ? extent : nil)
                .contentShape(Rectangle())
                .onHover { isHovered = $0 }
                .onTapGesture { action() }
                .help(tooltip)

            Image(systemName: systemName)
                .font(.system(size: 13, weight: .semibold))
                .foregroundStyle(.primary)
                .frame(width: buttonSize, height: buttonSize)
                .background(.regularMaterial, in: Circle())
                .overlay(Circle().strokeBorder(.quaternary, lineWidth: 0.5))
                .shadow(color: .black.opacity(0.12), radius: 4, y: 1)
                .opacity(isHovered ? 1 : 0)
                .scaleEffect(isHovered ? 1 : 0.8)
                .allowsHitTesting(false)
        }
        .animation(.easeOut(duration: 0.15), value: isHovered)
    }
}
```

> `extent` 限制热区长轴长度（默认 120），热区只覆盖边缘中间一段并垂直居中，避免整条左缘被透明命中条劫持（如顶部收藏夹的点击）。

## 禁止事项

1. 不改 `toggleLeftSidebar()`（ContentView.swift:302）——收起设 38 已能触发隐藏。
2. 不改 `SidebarView.swift`——窄轨渲染逻辑保留，拖拽到 38~120 时仍要用。
3. 不改 `AppShortcutBridge.swift`、`App.swift`（AppDelegate）、`TopBarRegistry.swift`、`ResizableDivider.swift`、`TopBarView.swift`。
4. 不动 `baseMinWidth` / `pipMinContentWidth` / `rightSidebarMaxAvailable`。
5. 不加 `withAnimation`/系统动画（热区按钮自带的 `.transition` 除外）；不加 `keyEquivalent`。
6. 不动 `#if os(iOS)` 分支及 iOS 代码；新增代码必须在 `#if os(macOS)` 内或 macOS 分支内。
7. 不重排/格式化未涉及代码。
8. 不运行 `xcodebuild`/`swift build`（客户端由用户 Xcode 验证）。

## 已知接受的简化

- 窗口最小宽/右栏最大宽不感知隐藏（预留 38+24，略保守）。
- 隐藏/展开无动画；热区按钮仅 hover 出现（无常驻边缘指示）。
- 隐藏态不持久化（重启恢复展开）。
- 窄窗下点展开无反馈（clamp 回 38，保持隐藏）。
- 热区 48px × 120px（垂直居中）会拦截其覆盖区域（中栏卡片左侧中间段）的点击，仅隐藏态存在，可接受；顶部收藏夹区域不再被劫持。

## 自检（只读）

```bash
grep -n "isSidebarHidden" Shared/Views/ContentView.swift          # 4（定义+布局+手柄+热区）
grep -rn "LeftEdgeExpandZone" Shared/Views/                       # 0（已移除，改用公共组件）
grep -n "HoverRevealButton" Shared/Views/HoverRevealButton.swift  # 定义
git diff --stat                                                   # ContentView.swift + HoverRevealButton.swift + 本 doc
```

## 人工验收清单

1. 拖缝隙到最窄 / ⌘B → 侧栏卡片、内边距、缝线、缝隙全部消失，中间栏左移撑满；顶栏无任何按钮。
2. 隐藏后悬停窗口左缘（内容区任意高度）→ 垂直居中位置出现圆形 `>` 按钮，有 tooltip「展开左侧栏 (⌘B)」；点击 → 侧栏恢复记忆宽度。
3. 移出左缘 → 按钮淡出消失。
4. Cmd+B 隐藏/展开行为一致。
5. 窗口缩窄自动收起 → 完全隐藏；拉宽 → 自动恢复。
6. 手动拖拽左分隔线到 38~120 → 窄轨显示，不隐藏、不出热区；拖到 38 → 隐藏，热区出现。
7. 右侧栏打开、PiP 模式下收起/展开正常。
