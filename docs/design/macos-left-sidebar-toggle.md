# 顶栏左侧栏切换按钮 + 收起时完全隐藏（macOS）

## 目标

在 macOS 客户端顶栏最左侧加一个按钮，控制左侧栏展开/收起；并让左栏「收起」时**完全不可见**（侧栏卡片、内边距、与中栏的缝隙、缝线全部消失，中间栏左移撑满）。

## 已确认的设计决策（勿改）

1. **按钮位置**：对齐中间栏左缘（展开/窄轨态）。隐藏态因与 macOS 红绿灯按钮冲突，改用**固定安全偏移 `topBarTrafficClearance = 56`**（按钮左缘落在 x≈72）。
2. **隐藏判定**：仅收起动作（`sidebarWidth == 38`）触发完全隐藏；手动拖拽到 38~120 仍显示窄轨。
3. **窗口最小宽计算保持不变**：`baseMinWidth` / `pipMinContentWidth` / `rightSidebarMaxAvailable` 均不动。
4. **无动画**、隐藏态**不持久化**（重启恢复展开）、窄窗下点展开无反馈（沿用现状）。

## 背景

- 左栏宽度 = `ContentView.sidebarWidth`（@State）。收起时被设为 `Layout.sidebarMinWidth = 38`；展开时恢复 `storedSidebarWidth`（记忆宽度）。
- Cmd+B 已通过 AppDelegate 的 NSEvent monitor → `AppShortcutBridge.shared.onToggleLeftSidebar` 闭包 → `ContentView.toggleLeftSidebar()` 工作。按钮直接复用该闭包，**toggle 逻辑零改动**。
- 顶栏 `TopBarView` 用 `TopBarRegistry` 左/中/右三槽位。左槽（`.left`）当前没有任何默认组，本次在其中注册一个按钮组。
- 顶栏最左侧 `Spacer().frame(width: navLeadingOffset)` 决定左槽按钮的 x 位置；`navLeadingOffset = sidebarWidth + Layout.navToolbarContentOffset(24)` 恰好对齐中间栏左缘。隐藏态中间栏左缘在 x=6，但按钮放那里会压在 macOS 红绿灯（x≈7-66）上，故隐藏态用固定偏移。
- 布局结构（ContentView macOS 分支，GeometryReader 内 HStack）：
  `HSpace(cardGap)` → `SidebarView`（+内边距+卡片背景） → `HSpace(seamHalfGap)` + `SeamMarker` + `HSpace(seamHalfGap)` → 中间栏。
  侧栏与缝线部分包进条件渲染，**第一行的 `HSpace(Layout.cardGap)`（窗口左侧 6px 边距）必须留在条件外**。

## 改动清单

| # | 文件 | 位置 | 改动 |
|---|------|------|------|
| 1 | `client/xHelper/xHelper/Shared/Views/LayoutConstants.swift` | `sidebarDefaultWidth` 之后 | 新增常量 `topBarTrafficClearance = 56` |
| 2 | `client/xHelper/xHelper/Shared/Views/TopBarView.swift` | `registerDefaultGroups()` 末尾 | 注册 `default-left` 左槽按钮组 |
| 3 | `client/xHelper/xHelper/Shared/Views/ContentView.swift` | 共 3 处 | 新增 `isSidebarHidden`；`navLeadingOffset` 按状态切换；侧栏块条件渲染；左拖拽手柄加隐藏条件 |

### 编辑 1：LayoutConstants.swift

锚点：
```swift
    static let sidebarDefaultWidth: CGFloat = 200

    static let toolbarHorizontalPadding: CGFloat = 16
```

在 `sidebarDefaultWidth` 行之后插入：
```swift
    /// 顶栏左槽按钮在侧栏隐藏态下的起始偏移：避开窗口左上角 macOS 红绿灯按钮
    /// （红绿灯约占 x 7~66，加上顶栏水平内边距 16，偏移 56 时按钮左缘在 x≈72）。
    static let topBarTrafficClearance: CGFloat = 56
```

### 编辑 2：TopBarView.swift

锚点（`registerDefaultGroups()` 结尾）：
```swift
                TopBarButton(id: "settings", icon: { "gear" },
                             enabled: { true },
                             action: { openSettings() },
                             help: { "设置" }),
            ]
        ))
        #endif
    }
```

替换为：
```swift
                TopBarButton(id: "settings", icon: { "gear" },
                             enabled: { true },
                             action: { openSettings() },
                             help: { "设置" }),
            ]
        ))
        #endif

        #if os(macOS)
        TopBarRegistry.shared.register(TopBarGroup(
            id: "default-left",
            slot: .left,
            priority: 5,
            buttons: [
                TopBarButton(id: "toggle-left-sidebar",
                             icon: { "sidebar.leading" },
                             enabled: { true },
                             action: { AppShortcutBridge.shared.onToggleLeftSidebar?() },
                             help: { "切换左侧栏 (⌘B)" })
            ]
        ))
        #endif
    }
```

注意：
- 不给按钮加 `keyEquivalent`（Cmd+B 已被 NSEvent monitor 消费）。
- 不给按钮加 `visible:` 参数（默认恒显示）。
- 新块必须包在 `#if os(macOS)` 内。

### 编辑 3：ContentView.swift

**3a. 新增 `isSidebarHidden`**（锚点 `private var allowExpand: Bool { windowWidth >= 600 }` 之后）：
```swift
    /// 侧栏完全隐藏判定：仅收起动作（sidebarWidth 落到最小宽 38）触发；
    /// 手动拖拽到 38~120 之间仍显示窄轨，不隐藏。
    private var isSidebarHidden: Bool {
        sidebarWidth <= Layout.sidebarMinWidth
    }
```

**3b. `navLeadingOffset`**（锚点）：
```swift
                    TopBarView(
                        rightSidebar: rightSidebar,
                        navLeadingOffset: sidebarWidth + Layout.navToolbarContentOffset
                    )
```
替换为：
```swift
                    TopBarView(
                        rightSidebar: rightSidebar,
                        navLeadingOffset: (isSidebarHidden ? Layout.topBarTrafficClearance : sidebarWidth + Layout.navToolbarContentOffset)
                    )
```

**3c. 侧栏块条件渲染**（`HSpace(Layout.cardGap)` 留在 if 外，其余包进 `if !isSidebarHidden` 并整体右移 4 空格）：
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

**3d. 左拖拽手柄**（`if allowExpand {` → `if allowExpand && !isSidebarHidden {`）：
```swift
                                if allowExpand && !isSidebarHidden {
                                    ResizableDivider(
                                        id: "left",
```

## 禁止事项

1. 不改 `toggleLeftSidebar()`——收起设 38 已能触发隐藏。
2. 不改 `SidebarView.swift`——窄轨渲染逻辑保留，拖拽到 38~120 时仍要用。
3. 不改 `AppShortcutBridge.swift`、`App.swift`（AppDelegate）、`TopBarRegistry.swift`、`ResizableDivider.swift`。
4. 不动 `baseMinWidth` / `pipMinContentWidth` / `rightSidebarMaxAvailable`。
5. 不加动画 / `.transition` / `withAnimation`；不加 `keyEquivalent`。
6. 不动 `#if os(iOS)` 分支及 iOS 代码；新增代码必须在 `#if os(macOS)` 内或 macOS 分支内。
7. 不重排/格式化未涉及代码。
8. 不运行 `xcodebuild`/`swift build`（客户端由用户 Xcode 验证）。

## 已知接受的简化

- 窗口最小宽/右栏最大宽不感知隐藏（预留 38+24，略保守）。
- 隐藏/展开无动画。
- 隐藏态不持久化（重启恢复展开）。
- 窄窗下点展开无反馈（clamp 回 38，保持隐藏）。
- 浏览器/文档模式下，切换按钮旁可能出现一根孤立的 1px 细分隔线（仅外观）。

## 自检（只读）

```bash
grep -n "topBarTrafficClearance" Shared/Views/LayoutConstants.swift Shared/Views/ContentView.swift   # 1 + 1
grep -n "default-left" Shared/Views/TopBarView.swift                                                   # 1
grep -c "isSidebarHidden" Shared/Views/ContentView.swift                                               # 4
git diff --stat                                                                                        # 仅 3 文件
```

## 人工验收清单

1. 展开态：顶栏左侧出现 `sidebar.leading` 按钮，位于中间栏左缘；hover 提示「切换左侧栏 (⌘B)」。
2. 点按钮 → 侧栏卡片、内边距、缝线、缝隙全部消失，中间栏左移撑满；按钮左移到红绿灯右侧（x≈72）。
3. 再点 → 侧栏恢复记忆宽度，按钮回到中间栏左缘。
4. Cmd+B 行为与按钮一致。
5. 窗口缩窄自动收起 → 完全隐藏；拉宽 → 自动恢复。
6. 拖拽左分隔线到 38~120 → 窄轨显示不隐藏；拖到 38 → 隐藏，手柄消失，仅按钮/Cmd+B 可恢复。
7. 右侧栏打开、PiP 模式下收起/展开正常；双击顶栏空白缩放窗口正常。
8. 浏览器/文档模式下顶栏无布局错乱。
