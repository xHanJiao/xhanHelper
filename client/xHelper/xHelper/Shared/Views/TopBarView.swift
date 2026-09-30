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
    @ObservedObject var chatTabManager: ChatTabManager
    @ObservedObject var conversationListVM: ConversationListViewModel
    @State private var showConversationMenu = false
    @State private var hoveredConversationID: String?

    #if os(macOS)
    @Environment(\.openSettings) private var openSettings
    @EnvironmentObject private var colorSchemeManager: ColorSchemeManager
    @Environment(\.colorScheme) private var colorScheme
    #endif

    var body: some View {
        topBarHStack(flexible: true, interactive: true)
            // 双击顶栏空白区缩放窗口：contentShape 让空白区可命中，按钮区域由按钮自身
            // 手势优先消费（SwiftUI 子视图手势优先），双击按钮不会触发缩放。
            .contentShape(Rectangle())
            #if os(macOS)
            .onTapGesture(count: 2) { zoomWindow() }
            #endif
            .padding(.horizontal, Layout.toolbarHorizontalPadding)
            .padding(.vertical, Layout.toolbarVGap)
            .ignoresSafeArea(edges: .top)
            .background(intrinsicWidthProbe)
            .onAppear { registerDefaultGroups() }
    }

    /// 顶栏内容。`flexible`：真实栏含弹性 `Spacer()` 撑满可用宽；探针副本把它换成 0 宽
    /// （否则内在宽 = ∞）。`interactive`：探针副本渲染静态图标，不挂 Button/快捷键/popover，
    /// 几何与真实按钮一致但不产生重复快捷键、不共享 `@State`。
    @ViewBuilder
    private func topBarHStack(flexible: Bool, interactive: Bool) -> some View {
        HStack(spacing: 8) {
            // 红绿灯让位（顶栏铺到标题栏，左区内容需避开窗口按钮）
            Spacer().frame(width: Layout.topBarTrafficClearance)

            // 左侧：当前会话下拉（不走 registry 的 leading View，PiP 时不显示）
            if !rightSidebar.isPipChat {
                conversationSwitcher(interactive: interactive)
            }

            if flexible { Spacer(minLength: 0) } else { Spacer().frame(width: 0) }

            #if os(macOS)
            // dev 视觉标识（UI-7）：Debug 构建在胶囊簇左侧挂一枚 DEV 徽标，
            // 一眼认出沙盒窗口（Dock 图标另有 dev 角标）。
            if LocalPaths.isDev { devChip }
            #endif

            renderSlot(.center, interactive: interactive)

            // 中右两侧都有可见组时才显示分隔线
            if !visibleGroups(for: .center).isEmpty && !visibleGroups(for: .right).isEmpty {
                UnresizableDivider()
            }
            renderSlot(.right, interactive: interactive)
        }
    }

    /// 隐藏探针：按内在宽（fixedSize）量测顶栏，经 `TopBarMinWidthKey` 回传。
    /// 用于把窗口最小宽抬到「顶栏装得下」，否则窗口能缩到比顶栏还窄 → 顶栏溢出居中
    /// → 红绿灯压住下拉框、中/右栏被裁。
    private var intrinsicWidthProbe: some View {
        topBarHStack(flexible: false, interactive: false)
            .padding(.horizontal, Layout.toolbarHorizontalPadding)
            .fixedSize()
            .hidden()
            .background(GeometryReader { g in
                Color.clear.preference(key: TopBarMinWidthKey.self, value: g.size.width)
            })
    }

    // MARK: - 会话切换（顶栏左侧 leading View）

    private var currentConversationTitle: String {
        let t = chatTabManager.currentTab?.title ?? ""
        return t.isEmpty ? "新对话" : t
    }

    private var currentIsEphemeral: Bool {
        chatTabManager.currentTab?.isEphemeral == true
    }

    /// 当前会话下拉 trigger + 新建（普通新建；无痕走输入栏 pill）。
    /// `interactive == false`：探针副本，只渲染同尺寸的静态标签，不挂 Button/popover。
    @ViewBuilder
    private func conversationSwitcher(interactive: Bool) -> some View {
        let switcher = HStack(spacing: 6) {
            if interactive {
                Button {
                    showConversationMenu.toggle()
                } label: {
                    conversationSwitcherLabel
                }
                .buttonStyle(.plain)
                .background(.ultraThinMaterial, in: Capsule())
                .overlay(Capsule().stroke(.white.opacity(0.15), lineWidth: 0.5))
                .help("切换会话")
                .popover(isPresented: $showConversationMenu, arrowEdge: .bottom) {
                    conversationMenu
                }

                Button {
                    chatTabManager.newChat()
                } label: {
                    plusButtonLabel
                }
                .buttonStyle(.plain)
                .background(.ultraThinMaterial, in: Circle())
                .overlay(Circle().stroke(.white.opacity(0.15), lineWidth: 0.5))
                .help("新会话")
            } else {
                conversationSwitcherLabel
                plusButtonLabel
            }
        }
        if interactive {
            switcher.task {
                // 会话列表首次加载：原挂在侧栏 .task 上，删栏后由此触发
                if !conversationListVM.hasLoadedOnce {
                    await conversationListVM.load()
                }
            }
        } else {
            switcher
        }
    }

    private var conversationSwitcherLabel: some View {
        HStack(spacing: 4) {
            if currentIsEphemeral {
                Image(systemName: "theatermasks.fill")
                    .font(.system(size: 12))
                    .foregroundStyle(Color("appAccent"))
            }
            Text(currentConversationTitle)
                .font(.system(size: 14, weight: .medium))
                .foregroundStyle(.primary.opacity(0.85))
                .lineLimit(1)
                .truncationMode(.tail)
                .frame(maxWidth: 240, alignment: .leading)
            Image(systemName: "chevron.down")
                .font(.system(size: 10, weight: .bold))
                .foregroundStyle(.secondary)
        }
        .padding(.horizontal, 12)
        .frame(height: Layout.topBarCapsuleHeight)
        .fixedSize(horizontal: true, vertical: false)
    }

    private var plusButtonLabel: some View {
        Image(systemName: "plus")
            .font(.system(size: 16, weight: .medium))
            .foregroundStyle(.primary.opacity(0.7))
            .frame(width: Layout.topBarCapsuleHeight, height: Layout.topBarCapsuleHeight)
            .contentShape(Circle())
    }

    private var conversationMenu: some View {
        VStack(alignment: .leading, spacing: 0) {
            Text("会话")
                .font(.system(size: 11, weight: .semibold))
                .foregroundStyle(.secondary)
                .padding(.horizontal, 16).padding(.top, 10).padding(.bottom, 6)

            ScrollView {
                VStack(alignment: .leading, spacing: 0) {
                    ForEach(conversationListVM.conversations) { conv in
                        conversationMenuRow(conv)
                    }

                    if conversationListVM.hasMore {
                        Button {
                            Task { await conversationListVM.loadMore() }
                        } label: {
                            Text(conversationListVM.isLoadingMore ? "加载中…" : "加载更多")
                                .font(.system(size: 11))
                                .foregroundStyle(.secondary)
                                .frame(maxWidth: .infinity)
                                .padding(.vertical, 8)
                        }
                        .buttonStyle(.plain)
                        .disabled(conversationListVM.isLoadingMore)
                    }
                }
                .padding(.horizontal, 6)
            }
            .frame(maxHeight: 420)
        }
        .frame(width: 280)
        .padding(.bottom, 6)
    }

    private func conversationMenuRow(_ conv: Conversation) -> some View {
        let isCurrent = chatTabManager.currentConversationId == conv.id
        let isHovered = hoveredConversationID == conv.id
        let isPinned = conv.pinned == true
        let isEphemeral = conv.ephemeral == true
        return HStack(spacing: 4) {
            Button {
                showConversationMenu = false
                Task {
                    await chatTabManager.openConversation(
                        id: conv.id, title: conv.title, pushHistory: true, mode: conv.mode,
                        ephemeral: conv.ephemeral == true
                    )
                }
            } label: {
                HStack(spacing: 6) {
                    if isEphemeral {
                        Image(systemName: "theatermasks")
                            .font(.system(size: 12)).foregroundStyle(.secondary)
                    }
                    Text(conv.title.isEmpty ? "新对话" : conv.title)
                        .font(.system(size: 14, weight: isCurrent ? .semibold : .regular))
                        .foregroundStyle(.primary)
                        .lineLimit(1)
                    Spacer(minLength: 4)
                    if isCurrent {
                        Image(systemName: "checkmark")
                            .font(.system(size: 12, weight: .bold))
                            .foregroundStyle(Color("appAccent"))
                    }
                }
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)

            if isHovered {
                Button {
                    Task {
                        await conversationListVM.delete(id: conv.id)
                        chatTabManager.close(conversationId: conv.id)
                    }
                } label: {
                    Image(systemName: isEphemeral ? "trash" : "archivebox")
                        .font(.system(size: 12))
                        .foregroundStyle(.secondary)
                        .frame(width: 20, height: 20)
                }
                .buttonStyle(.plain)
                .help(isEphemeral ? "销毁" : "归档")
            }

            if isHovered || isPinned {
                Button {
                    Task { await conversationListVM.setPinned(id: conv.id, pinned: !isPinned) }
                } label: {
                    Image(systemName: isPinned ? "pin.fill" : "pin")
                        .font(.system(size: 12))
                        .foregroundStyle(isPinned ? Color("appAccent") : .secondary)
                        .frame(width: 20, height: 20)
                }
                .buttonStyle(.plain)
                .help(isPinned ? "取消置顶" : "置顶")
            }
        }
        .padding(.horizontal, 10).padding(.vertical, 7)
        .background(
            isCurrent
                ? Color("appAccent").opacity(0.10)
                : (isHovered ? Color.primary.opacity(0.06) : .clear),
            in: RoundedRectangle(cornerRadius: 6)
        )
        .contentShape(Rectangle())
        .onHover { hoveredConversationID = $0 ? conv.id : nil }
    }

    // MARK: - dev 徽标（UI-7）

    #if os(macOS)
    private var devChip: some View {
        Text("DEV")
            .font(.system(size: 10, weight: .semibold, design: .rounded))
            .tracking(0.5)
            .foregroundStyle(Color("appAccent"))
            .padding(.horizontal, 8)
            .frame(height: Layout.topBarCapsuleHeight)
            .background(Color("appAccent").opacity(0.16), in: Capsule())
            .help("本地开发环境（Debug 构建）")
            .allowsHitTesting(false)
    }
    #endif

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
    private func renderSlot(_ slot: TopBarSlot, interactive: Bool) -> some View {
        let groups = visibleGroups(for: slot)
        if !groups.isEmpty {
            HStack(spacing: 0) {
                ForEach(Array(groups.enumerated()), id: \.element.id) { index, group in
                    if index > 0 {
                        Divider().frame(height: 14).padding(.horizontal, 4)
                    }
                    CapsuleGroup {
                        buttonRow(group, interactive: interactive)
                    }
                }
            }
        }
    }

    @ViewBuilder
    private func buttonRow(_ group: TopBarGroup, interactive: Bool) -> some View {
        // 先按 visible() 过滤，再基于可见按钮计算分隔线（避免隐藏按钮导致多/少 Divider）
        let visibleButtons = group.buttons.filter { $0.visible() }
        ForEach(Array(visibleButtons.enumerated()), id: \.element.id) { index, button in
            if index > 0 {
                Divider().frame(height: 14)
            }
            renderButton(button, interactive: interactive)
        }
    }

    @ViewBuilder
    private func renderButton(_ button: TopBarButton, interactive: Bool) -> some View {
        if !interactive {
            // 探针副本：与 CapsuleButton/CapsuleRefreshButton 同尺寸（CapsuleIcon 26pt），
            // 但不挂 Button/快捷键/悬浮，避免重复注册快捷键与共享状态。
            CapsuleIcon(systemName: button.icon(), showBackground: false)
        } else if button.isRefresh {
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

/// 顶栏内在最小宽（隐藏探针量测）→ 用作窗口最小宽下限。
struct TopBarMinWidthKey: PreferenceKey {
    static var defaultValue: CGFloat = 0
    static func reduce(value: inout CGFloat, nextValue: () -> CGFloat) {
        value = max(value, nextValue())
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
