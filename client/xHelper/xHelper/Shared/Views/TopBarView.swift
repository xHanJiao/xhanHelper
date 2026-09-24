import SwiftUI
#if os(macOS)
import AppKit
#endif

/// 通用顶栏：按槽位渲染 TopBarRegistry 中最优先的按钮组（同优先多组共存）。
/// 替代原 NavigationToolbar；各功能区通过注册/注销按钮组向槽位贡献按钮。
struct TopBarView: View {
    @ObservedObject private var registry = TopBarRegistry.shared
    @ObservedObject private var bookmarkStore = BookmarkStore.shared
    @ObservedObject var rightSidebar: RightSidebarManager
    let navLeadingOffset: CGFloat

    #if os(macOS)
    @Environment(\.openSettings) private var openSettings
    @EnvironmentObject private var colorSchemeManager: ColorSchemeManager
    @Environment(\.colorScheme) private var colorScheme
    #endif

    var body: some View {
        HStack(spacing: 8) {
            Spacer()
                .frame(width: navLeadingOffset)

            renderSlot(.left)

            // 左中两侧都有可见组时才显示分隔线，避免孤立胶囊
            if !visibleGroups(for: .left).isEmpty && !visibleGroups(for: .center).isEmpty {
                UnresizableDivider()
            }

            Spacer()

            renderSlot(.center)

            // 中右两侧都有可见组时才显示分隔线
            if !visibleGroups(for: .center).isEmpty && !visibleGroups(for: .right).isEmpty {
                UnresizableDivider()
            }
            renderSlot(.right)
        }
        // 双击顶栏空白区缩放窗口：contentShape 让空白区可命中，按钮区域由按钮自身
        // 手势优先消费（SwiftUI 子视图手势优先），双击按钮不会触发缩放。
        .contentShape(Rectangle())
        #if os(macOS)
        .onTapGesture(count: 2) { zoomWindow() }
        #endif
        .padding(.horizontal, Layout.toolbarHorizontalPadding)
        .padding(.vertical, Layout.toolbarVGap)
        .ignoresSafeArea(edges: .top)
        .onAppear { registerDefaultGroups() }
    }

    /// 双击顶栏空白区：窗口普通缩放（放大到屏幕可用区域）↔ 还原。仅空白 Spacer 响应，按钮区域不受影响。
    private func zoomWindow() {
        #if os(macOS)
        guard let window = NSApp.keyWindow else { return }
        if window.styleMask.contains(.fullScreen) { return } // 已在系统全屏时不处理
        window.zoom(nil)
        #endif
    }

    // MARK: - 默认组（永不注销；同 id 覆盖幂等）

    private func registerDefaultGroups() {
        // 默认左槽为空：无浏览器时左侧不放按钮（浏览器态由浏览器组 priority 10 填充）
        // 右槽位 priority 5：导航组（文档/账单/监控/浏览器）+ 工具组（侧栏/深浅色/设置），防被普通组覆盖。
        // 两组合并进同一优先级，注册顺序决定渲染顺序：导航组在左、工具组在右，组间自动分隔线。
        var buttons: [TopBarButton] = [
            TopBarButton(id: "enter-browser", icon: { "globe" },
                         enabled: { true },
                         action: {
                             BrowserToolProvider.shared.enterBrowser()
                         },
                         help: { "一键进入浏览器" },
                         keyEquivalent: ("4", .command)),
        ]
        #if os(macOS)
        buttons.insert(contentsOf: [
            TopBarButton(id: "open-documents", icon: { "doc.text" },
                         enabled: { true },
                         action: { openBrowsePanel(.documents) },
                         help: { "打开文档" },
                         keyEquivalent: ("1", .command)),
            TopBarButton(id: "open-bills", icon: { "yensign.circle" },
                         enabled: { true },
                         action: { openBrowsePanel(.bills) },
                         help: { "打开账单" },
                         keyEquivalent: ("2", .command)),
            TopBarButton(id: "open-monitor", icon: { "chart.bar.xaxis" },
                         enabled: { true },
                         action: { openBrowsePanel(.dashboard) },
                         help: { "打开监控" },
                         keyEquivalent: ("3", .command)),
        ], at: 0)
        // 记忆按钮排在浏览之后
        buttons.append(TopBarButton(id: "open-memories", icon: { "brain.head.profile" },
                         enabled: { true },
                         action: { openBrowsePanel(.memories) },
                         help: { "打开记忆" },
                         keyEquivalent: ("5", .command)))
        #endif
        TopBarRegistry.shared.register(TopBarGroup(
            id: "default-right",
            slot: .right,
            priority: 5,
            buttons: buttons
        ))

        #if os(macOS)
        TopBarRegistry.shared.register(TopBarGroup(
            id: "default-utility",
            slot: .right,
            priority: 5,
            buttons: [
                TopBarButton(id: "maximize-sidebar",
                             icon: { rightSidebar.isPipChat ? "arrow.down.right.and.arrow.up.left" : "arrow.up.left.and.arrow.down.right" },
                             enabled: { rightSidebar.isOpen },
                             action: { rightSidebar.isPipChat.toggle() },
                             help: { rightSidebar.isPipChat ? "还原侧栏" : "最大化侧栏内容" },
                             visible: { rightSidebar.isOpen }), // 侧栏未打开时按钮不显示
                TopBarButton(id: "sidebar", icon: { "sidebar.trailing" },
                             enabled: { true },
                             action: { rightSidebar.toggle() },
                             help: { "切换右侧栏" }),
                // 深浅色切换：深色=月亮、浅色=太阳；跟随系统时按当前实际配色显示，点击切到相反显式模式
                TopBarButton(id: "color-scheme",
                             icon: { colorSchemeManager.selectedScheme == .dark ? "moon.fill" : "sun.max.fill" },
                             enabled: { true },
                             action: { toggleColorScheme() },
                             help: { colorSchemeManager.selectedScheme == .dark ? "切换浅色模式" : "切换深色模式" }),
                // 设置固定最右
                TopBarButton(id: "settings", icon: { "gear" },
                             enabled: { true },
                             action: { openSettings() },
                             help: { "设置" }),
            ]
        ))
        #endif
    }

    /// 打开右侧栏浏览面板（文档/账单/监控），复用浏览面板态
    private func openBrowsePanel(_ panel: BrowsePanel) {
        rightSidebar.isOpen = true
        rightSidebar.showBrowsePanel()
        rightSidebar.browsePanelSelection = panel
    }

    /// 深浅色切换：light ↔ dark 互切；跟随系统时先落到与当前实际配色相反的模式。
    private func toggleColorScheme() {
        #if os(macOS)
        switch colorSchemeManager.selectedScheme {
        case .system:
            colorSchemeManager.selectedScheme = (colorScheme == .dark) ? .light : .dark
        case .light:
            colorSchemeManager.selectedScheme = .dark
        case .dark:
            colorSchemeManager.selectedScheme = .light
        }
        #endif
    }

    /// 某槽位的可见组列表：先按组级 visible 过滤，再过滤掉"组内无任何可见按钮"的空组
    /// （避免空胶囊：组级 visible 命中但按钮级全隐藏时）。
    private func visibleGroups(for slot: TopBarSlot) -> [TopBarGroup] {
        registry.visibleGroups(for: slot)
            .filter { group in group.buttons.contains { $0.visible() } }
    }

    @ViewBuilder
    private func renderSlot(_ slot: TopBarSlot) -> some View {
        let groups = visibleGroups(for: slot)
        if !groups.isEmpty {
            HStack(spacing: 0) {
                ForEach(Array(groups.enumerated()), id: \.element.id) { index, group in
                    if index > 0 {
                        Divider().frame(height: 14).padding(.horizontal, 4)
                    }
                    CapsuleGroup {
                        buttonRow(group)
                    }
                }
            }
        }
    }

    @ViewBuilder
    private func buttonRow(_ group: TopBarGroup) -> some View {
        // 先按 visible() 过滤，再基于可见按钮计算分隔线（避免隐藏按钮导致多/少 Divider）
        let visibleButtons = group.buttons.filter { $0.visible() }
        ForEach(Array(visibleButtons.enumerated()), id: \.element.id) { index, button in
            if index > 0 {
                Divider().frame(height: 14)
            }
            renderButton(button)
        }
    }

    @ViewBuilder
    private func renderButton(_ button: TopBarButton) -> some View {
        if button.isRefresh {
            CapsuleRefreshButton(disabled: !button.enabled(), disabledMessage: button.help()) {
                button.action()
            }
            .keyboardShortcutIfNeeded(button)
        } else {
            CapsuleButton(systemName: button.icon(), enabled: button.enabled(), tooltip: button.help()) {
                button.action()
            }
            .keyboardShortcutIfNeeded(button)
        }
    }
}

// MARK: - 键盘快捷键（可空）

private extension View {
    @ViewBuilder
    func keyboardShortcutIfNeeded(_ button: TopBarButton) -> some View {
        if let shortcut = button.keyEquivalent {
            keyboardShortcut(shortcut.key, modifiers: shortcut.modifiers)
        } else {
            self
        }
    }
}

// MARK: - 胶囊组件（自原 NavigationToolbar 迁移）

private struct CapsuleIcon: View {
    let systemName: String
    let showBackground: Bool

    var body: some View {
        Image(systemName: systemName)
            .font(.system(size: 16, weight: .medium))
            .foregroundColor(.primary.opacity(0.7))
            .frame(width: Layout.topBarIconSize, height: Layout.topBarIconSize)
            .modifier(CapsuleButtonBackground(show: showBackground))
    }
}

private struct CapsuleButtonBackground: ViewModifier {
    let show: Bool

    func body(content: Content) -> some View {
        if show {
            content.background(.quaternary.opacity(0.5), in: Circle())
        } else {
            content
        }
    }
}

private struct CapsuleButton: View {
    let systemName: String
    let enabled: Bool
    let tooltip: String
    let action: () -> Void
    @State private var isHovered = false

    var body: some View {
        Button(action: action) {
            CapsuleIcon(systemName: systemName, showBackground: isHovered && enabled)
        }
        .buttonStyle(.plain)
        .disabled(!enabled)
        .opacity(enabled ? 1 : 0.25)
        .onHover { isHovered = $0 }
        .hoverTooltip(tooltip, direction: .down, delay: 0.25)
    }
}

private struct CapsuleRefreshButton: View {
    let disabled: Bool
    let disabledMessage: String?
    let action: () -> Void
    @State private var isAnimating = false
    @State private var isHovered = false

    private var isDisabled: Bool { disabled || isAnimating }

    var body: some View {
        Button {
            guard !isDisabled else { return }
            action()
            withAnimation(.interactiveSpring(response: 0.3, dampingFraction: 0.5)) {
                isAnimating = true
            }
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.5) {
                withAnimation(.interactiveSpring(response: 0.3, dampingFraction: 0.5)) {
                    isAnimating = false
                }
            }
        } label: {
            CapsuleIcon(systemName: "arrow.clockwise", showBackground: isHovered && !isDisabled)
                .rotationEffect(.degrees(isAnimating ? 360 : 0))
                .scaleEffect(isAnimating ? 0.85 : 1.0)
        }
        .buttonStyle(.plain)
        .disabled(isDisabled)
        .opacity(isDisabled ? 0.25 : 1)
        .hoverTooltip(disabledMessage ?? "", direction: .down, delay: 0.25)
        .onHover { isHovered = $0 }
    }
}

private struct CapsuleGroup<Content: View>: View {
    @ViewBuilder let content: () -> Content
    @State private var isHovered = false

    var body: some View {
        HStack(spacing: 4) {
            content()
        }
        .padding(.horizontal, Layout.topBarCapsuleHPadding)
        .padding(.vertical, Layout.topBarCapsuleVPadding)
        .background(.ultraThinMaterial, in: Capsule())
        .overlay(
            Capsule()
                .stroke(.white.opacity(0.15), lineWidth: 0.5)
        )
        .shadow(
            color: .black.opacity(isHovered ? 0.2 : 0.12),
            radius: isHovered ? 8 : 4,
            y: isHovered ? 4 : 2
        )
        .animation(.easeInOut(duration: 0.2), value: isHovered)
        .onHover { isHovered = $0 }
    }
}
