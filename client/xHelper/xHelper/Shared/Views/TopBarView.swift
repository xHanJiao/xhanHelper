import SwiftUI
#if os(macOS)
import AppKit
#endif

/// 通用顶栏：按槽位渲染 TopBarRegistry 中最优先的按钮组（同优先多组共存）。
/// 替代原 NavigationToolbar；各功能区通过注册/注销按钮组向槽位贡献按钮。
struct TopBarView: View {
    /// 可用宽（窗口内容宽）。顶栏据此自适应降级；见 docs/design/mac-topbar-degrade/design.md。
    let availableWidth: CGFloat
    @ObservedObject private var registry = TopBarRegistry.shared
    @ObservedObject private var bookmarksBarState = BookmarksBarState.shared
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
        topBarHStack(flexible: true, interactive: true, plan: plan)
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

    /// 降级计划：由「可用宽 + 当前位置」经纯函数算出（`TopBarLayout`）。
    /// 注意它是**算出来**而不是量出来的，所以真实栏与隐藏探针必然同档（不会自激）。
    private var plan: TopBarPlan {
        TopBarLayout.plan(available: availableWidth, model: widthModel)
    }

    /// 右槽两个内建组的 id：宽度模型要数它们的可见按钮数（`⋯` 的落位不再依赖 id）。
    private static let navGroupID = "default-right"
    private static let utilityGroupID = "default-utility"

    /// 顶栏宽度模型：各组当前有几个可见按钮。
    private var widthModel: TopBarWidthModel {
        let right = visibleGroups(for: .right)
        return TopBarWidthModel(
            centerGroups: visibleGroups(for: .center).map { group in
                group.buttons.filter { $0.visible() }.count
            },
            navGroupButtons: right.first { $0.id == Self.navGroupID }?
                .buttons.filter { $0.visible() }.count ?? 0,
            utilityButtons: right.first { $0.id == Self.utilityGroupID }?
                .buttons.filter { $0.visible() }.count ?? 0,
            isDev: isDevBuild,
            hasEphemeralBadge: currentIsEphemeral
        )
    }

    private var isDevBuild: Bool {
        #if os(macOS)
        return LocalPaths.isDev
        #else
        return false
        #endif
    }

    /// 顶栏内容。`flexible`：真实栏含弹性 `Spacer()` 撑满可用宽；探针副本把它换成 0 宽
    /// （否则内在宽 = ∞）。`interactive`：探针副本渲染静态图标，不挂 Button/快捷键/popover，
    /// 几何与真实按钮一致但不产生重复快捷键、不共享 `@State`。
    @ViewBuilder
    private func topBarHStack(flexible: Bool, interactive: Bool, plan: TopBarPlan) -> some View {
        HStack(spacing: Layout.topBarGroupSpacing) {
            // 红绿灯让位（顶栏铺到标题栏，左区内容需避开窗口按钮）
            Spacer().frame(width: Layout.topBarTrafficClearance)

            // 左侧：当前会话下拉。**常驻**（PiP 也显示，不再有 isPipChat 守卫）。
            conversationSwitcher(interactive: interactive, labelCap: plan.switcherLabelCap)

            if flexible { Spacer(minLength: 0) } else { Spacer().frame(width: 0) }

            #if os(macOS)
            // dev 视觉标识（UI-7）：Debug 构建在胶囊簇左侧挂一枚 DEV 徽标，
            // 一眼认出沙盒窗口（Dock 图标另有 dev 角标）。窄窗降级时第一个被收掉。
            if plan.showsDevChip { devChip }
            #endif

            if plan.showsCenter {
                renderSlot(.center, interactive: interactive)
            }

            // 中右两侧都有可见组时才显示分隔线
            if plan.showsCenter
                && !visibleGroups(for: .center).isEmpty
                && !visibleGroups(for: .right).isEmpty {
                UnresizableDivider()
            }
            renderRightSlot(plan: plan, interactive: interactive)
        }
    }

    /// 隐藏探针：按内在宽（fixedSize）量测**当前档**的顶栏，经 `TopBarMinWidthKey` 回传。
    /// 顶栏降级后它通常小于窗口底线、不再撑宽窗口；留作安全网 —— 万一档位算乐观了，
    /// 它会把窗口顶开而不是让顶栏溢出（见 docs/design/mac-topbar-degrade/design.md §3）。
    private var intrinsicWidthProbe: some View {
        topBarHStack(flexible: false, interactive: false, plan: plan)
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
    /// `labelCap`：标签上限（降级末档收窄，见 `TopBarLayout.switcherLabelCap`）。
    @ViewBuilder
    private func conversationSwitcher(interactive: Bool, labelCap: CGFloat) -> some View {
        let switcher = HStack(spacing: 6) {
            if interactive {
                Button {
                    showConversationMenu.toggle()
                } label: {
                    conversationSwitcherLabel(cap: labelCap)
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
                conversationSwitcherLabel(cap: labelCap)
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

    private func conversationSwitcherLabel(cap: CGFloat) -> some View {
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
                .frame(maxWidth: cap, alignment: .leading)
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

            // 时间固定在最右侧：hover 出的操作按钮排在其左侧
            Text(formatConversationDate(conv.updatedAt))
                .font(.system(size: 11))
                .foregroundStyle(.secondary)
                .lineLimit(1)
                .fixedSize()
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
                        Self.groupDivider
                    }
                    CapsuleGroup {
                        buttonRow(group, interactive: interactive)
                    }
                }
            }
        }
    }

    // MARK: - 右槽（含降级收进的 ⋯）

    /// 右槽渲染单元：`⋯` 在最左（替原本在左边的中心槽组），其后按注册顺序排可见组。
    private enum RightSlotUnit {
        case group(TopBarGroup)
        case overflow

        /// 稳定身份：组用组 id，`⋯` 用固定串 —— 否则档位切换时 offset 整体位移会让视图身份漂移
        /// （hover / 动画状态被重置）。
        var id: String {
            switch self {
            case .group(let group): return group.id
            case .overflow: return "__topbar_overflow"
            }
        }
    }

    @ViewBuilder
    private func renderRightSlot(plan: TopBarPlan, interactive: Bool) -> some View {
        let groups = visibleGroups(for: .right)
        // 判空用 units 而不是 groups：右槽无可见组时 ⋯ 仍要渲染（否则被收的按钮不可达）
        let units = rightSlotUnits(groups, plan: plan)
        if !units.isEmpty {
            HStack(spacing: 0) {
                ForEach(Array(units.enumerated()), id: \.element.id) { index, unit in
                    if index > 0 { Self.groupDivider }
                    switch unit {
                    case .group(let group):
                        CapsuleGroup {
                            buttonRow(group, interactive: interactive)
                        }
                    case .overflow:
                        // 与其它组同款胶囊（宽度模型按「单图标组 = groupWidth(1)」算，必须一致）
                        CapsuleGroup {
                            overflowCapsule(interactive: interactive)
                        }
                    }
                }
            }
        }
    }

    /// 单元顺序：`⋯` 在最左（= ⌘1–5 导航组的左侧 —— 它替的就是原本在左边的中心槽组），
    /// 其后按注册顺序排可见组。
    private func rightSlotUnits(_ groups: [TopBarGroup], plan: TopBarPlan) -> [RightSlotUnit] {
        var units: [RightSlotUnit] = plan.showsOverflow ? [.overflow] : []
        units += groups.map { .group($0) }
        return units
    }

    /// `⋯`：装降级时从栏上收下来的按钮（当前只有中心槽组）。
    /// 用原生 `Menu`：行 = `help()` 文案 + `icon()` 图标，直接复用按钮自己的闭包。
    @ViewBuilder
    private func overflowCapsule(interactive: Bool) -> some View {
        if interactive {
            CapsuleOverflowMenu(tooltip: "更多按钮") {
                ForEach(overflowButtons) { button in
                    Button {
                        button.action()
                    } label: {
                        Label(button.help(), systemImage: button.icon())
                    }
                    .disabled(!button.enabled())
                }
            }
        } else {
            // 探针副本：与真实 ⋯ 同尺寸（单图标组）
            CapsuleIcon(systemName: "ellipsis", showBackground: false)
        }
    }

    /// 被收进 `⋯` 的按钮（按栏上原顺序）。
    private var overflowButtons: [TopBarButton] {
        guard plan.showsOverflow else { return [] }
        return visibleGroups(for: .center).flatMap { $0.buttons.filter { $0.visible() } }
    }

    private static var groupDivider: some View {
        Divider().frame(height: 14).padding(.horizontal, 4)
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

private struct CapsuleOverflowMenu<MenuContent: View>: View {
    let tooltip: String
    @ViewBuilder let menuContent: () -> MenuContent
    @State private var isHovered = false

    var body: some View {
        Menu {
            menuContent()
        } label: {
            // 与 CapsuleButton 同尺寸同悬浮反馈（单图标组）
            CapsuleIcon(systemName: "ellipsis", showBackground: isHovered)
                .contentShape(Circle())
        }
        .menuStyle(.borderlessButton)
        .menuIndicator(.hidden)
        .fixedSize()
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
        HStack(spacing: Layout.topBarInGroupSpacing) {
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
