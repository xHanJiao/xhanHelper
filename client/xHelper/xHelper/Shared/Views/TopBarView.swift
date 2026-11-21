import SwiftUI
#if os(macOS)
import AppKit
#endif

/// 通用顶栏：按槽位渲染 TopBarRegistry 中最优先的按钮组（同优先多组共存）。
/// 替代原 NavigationToolbar；各功能区通过注册/注销按钮组向槽位贡献按钮。
struct TopBarView: View {
    /// 可用宽（窗口内容宽）。顶栏据此自适应降级；见 docs/design/mac-topbar-degrade/design.md。
    let availableWidth: CGFloat
    /// 右簇（DEV / 中心槽 / 右槽）能用的宽：右栏打开时 = 中栏卡片右侧那段（按钮簇右对齐，
    /// 压到卡片上就难看了）；右栏关着 / PiP 时中栏不占位 → `nil` = 不限制。
    let trailingLimit: CGFloat?
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
            // 上 12 / 下 6 —— `Layout.topBarHeight` 由这两处 + 胶囊高派生，也决定中栏卡片的上溢量
            .padding(.top, Layout.topBarTopGap)
            .padding(.bottom, Layout.toolbarVGap)
            .ignoresSafeArea(edges: .top)
            .background(intrinsicWidthProbe)
            .onAppear { registerDefaultGroups() }
    }

    /// 降级计划：由「可用宽 + 当前位置」经纯函数算出（`TopBarLayout`）。
    /// 注意它是**算出来**而不是量出来的，所以真实栏与隐藏探针必然同档（不会自激）。
    private var plan: TopBarPlan {
        TopBarLayout.plan(available: availableWidth, trailingLimit: trailingLimit, model: widthModel)
    }

    /// 右槽两个内建组的 id：宽度模型要数它们的可见按钮数（`⋯` 的落位不再依赖 id）。
    private static let navGroupID = "default-right"
    private static let utilityGroupID = "default-utility"

    /// 顶栏宽度模型：各组当前有几个可见按钮。
    private var widthModel: TopBarWidthModel {
        let right = visibleGroups(for: .right)
        return TopBarWidthModel(
            centerGroups: visibleGroups(for: .center).map { group in
                TopBarCenterGroup(
                    buttons: group.buttons.filter { $0.visible() }.count,
                    autoOverflow: group.autoOverflow
                )
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
            // dev 视觉标识（UI-7）：Debug 构建在胶囊簇左侧挂一枚红底 DEV 徽标，
            // 一眼认出沙盒窗口（Dock 图标另有 dev 角标）。**降级不收**（见 TopBarLayout）。
            if plan.showsDevChip { devChip }
            #endif

            if plan.showsCenter {
                renderCenterSlot(plan: plan, interactive: interactive)
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
                // 常驻阴影：顶栏胶囊靠投影才读得出"是个按钮"（材质在卡片/照片底上对比不足），
                // 参数与 CapsuleGroup 逐字一致 —— 三处胶囊必须同款，改一处记得改另两处。
                .shadow(color: .black.opacity(0.12), radius: 4, y: 2)
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
                // 与「切换会话」胶囊/ CapsuleGroup 同款常驻阴影，见上一条注释
                .shadow(color: .black.opacity(0.12), radius: 4, y: 2)
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
        // `.buttonStyle(.plain)` 只认画出来的形状：chevron 笔画只有几 pt，点箭头/右侧 padding 全落空。
        // 兜住整颗胶囊，点哪儿都开（与 plusButtonLabel 的 contentShape(Circle()) 同理）。
        .contentShape(Capsule())
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
            .foregroundStyle(.white)
            .padding(.horizontal, 8)
            .frame(height: Layout.topBarCapsuleHeight)
            .background(Color.red, in: Capsule())
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
        // 右槽位 priority 5：导航组（与右栏首页入口逐项对应，⌘1–N）+ 工具组（侧栏/深浅色/设置），防被普通组覆盖。
        // 两组合并进同一优先级，注册顺序决定渲染顺序：导航组在左、工具组在右，组间自动分隔线。
        //
        // 导航组**不再手写**：顺序/图标/文案都来自 `SidebarQuickEntry.all`（右栏首页网格共用同一份），
        // 未来加/换入口只改那一处，顶栏、快捷键、快捷键说明一起变。
        Self.checkQuickEntries()
        let navButtons = SidebarQuickEntry.all.enumerated().map { index, entry in
            TopBarButton(id: "open-\(entry.id)",
                         icon: { entry.icon },
                         enabled: { true },
                         action: { openQuickEntry(entry) },
                         help: { entry.help },
                         keyEquivalent: Self.quickEntryShortcut(index))
        }
        TopBarRegistry.shared.register(TopBarGroup(
            id: "default-right",
            slot: .right,
            priority: 5,
            buttons: navButtons
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
                // 深浅色切换：深色=太阳、浅色=月亮；跟随系统时按当前实际配色显示，点击切到相反显式模式
                TopBarButton(id: "color-scheme",
                             icon: { effectiveIsDark ? "sun.max.fill" : "moon.fill" },
                             enabled: { true },
                             action: { toggleColorScheme() },
                             help: { effectiveIsDark ? "切换浅色模式" : "切换深色模式" }),
                // 设置固定最右
                TopBarButton(id: "settings", icon: { "gear" },
                             enabled: { true },
                             action: { openSettings() },
                             help: { "设置" }),
            ]
        ))
        #endif
    }

    /// 打开右侧栏列表 tab（浏览器之外的首页入口都在这里），幂等
    private func openBrowsePanel(_ panel: BrowsePanel) {
        rightSidebar.isOpen = true
        rightSidebar.openPanel(panel)
    }

    /// 顶栏快捷入口的动作：浏览器走"进入"语义（已有网页 tab 就切过去，没有才开），面板走打开 tab。
    private func openQuickEntry(_ entry: SidebarQuickEntry) {
        switch entry {
        case .browser: BrowserToolProvider.shared.enterBrowser()
        case .panel(let panel): openBrowsePanel(panel)
        }
    }

    /// ⌘1–⌘9：`KeyEquivalent` 只吃单字符，第 10 项起不给键（iOS 无键盘快捷键）。
    private static func quickEntryShortcut(_ index: Int) -> (key: KeyEquivalent, modifiers: EventModifiers)? {
        #if os(macOS)
        guard index < 9 else { return nil }
        return (KeyEquivalent(Character("\(index + 1)")), .command)
        #else
        return nil
        #endif
    }

    /// 条目数与 id 唯一性：超过 9 项没键、id 撞了 `ForEach` 会错位，都在注册时炸在明处。
    private static func checkQuickEntries() {
        assert(SidebarQuickEntry.all.count <= 9, "快捷入口超过 9 项：⌘ 数字键只有 1–9")
        assert(
            Set(SidebarQuickEntry.all.map(\.id)).count == SidebarQuickEntry.all.count,
            "快捷入口 id 重复：顶栏 ForEach / 快捷键会错位"
        )
    }

    #if os(macOS)
    /// 当前实际生效的是否为深色（跟随系统时看环境里的实际配色）
    private var effectiveIsDark: Bool {
        switch colorSchemeManager.selectedScheme {
        case .system: return colorScheme == .dark
        case .light:  return false
        case .dark:   return true
        }
    }
    #endif

    /// 深浅色切换：light ↔ dark 互切；跟随系统时先落到与当前实际配色相反的模式。
    private func toggleColorScheme() {
        #if os(macOS)
        colorSchemeManager.selectedScheme = effectiveIsDark ? .light : .dark
        #endif
    }

    /// 某槽位的可见组列表：先按组级 visible 过滤，再过滤掉"组内无任何可见按钮"的空组
    /// （避免空胶囊：组级 visible 命中但按钮级全隐藏时）。
    private func visibleGroups(for slot: TopBarSlot) -> [TopBarGroup] {
        registry.visibleGroups(for: slot)
            .filter { group in group.buttons.contains { $0.visible() } }
    }

    /// 中心槽：按 `plan.centerVisible` 每组只渲染前 k 个；`k == 0` 的组整组跳过
    /// （连它前后那根 Divider 一起跳过，否则会留一根孤立的线）。
    /// 有组"留了一部分"时，`⋯` 长在**最后一组**的末尾（`OverflowCapsuleGroup`），不再单开胶囊。
    /// 多中心组今天不会同时出现（浏览器 7 键 / 文档详情 3 键互斥），空间按注册顺序先到先得。
    @ViewBuilder
    private func renderCenterSlot(plan: TopBarPlan, interactive: Bool) -> some View {
        // ViewBuilder 里不能裸写 `assert`（Void 表达式），用 `let _ =` 过一遍
        let _ = checkCenterCounts(plan)
        let shown = Array(zip(visibleGroups(for: .center), plan.centerVisible)).filter { $0.1 > 0 }
        if !shown.isEmpty {
            HStack(spacing: 0) {
                ForEach(Array(shown.enumerated()), id: \.element.0.id) { index, entry in
                    if index > 0 {
                        Self.groupDivider
                    }
                    OverflowCapsuleGroup(
                        interactive: interactive,
                        hostsOverflow: plan.overflowInline && index == shown.count - 1,
                        tooltip: "更多按钮"
                    ) {
                        buttonRow(entry.0, interactive: interactive, upTo: entry.1)
                    } menuContent: {
                        overflowMenuRows(inlineOverflowButtons)
                    }
                }
            }
        }
    }

    /// 视图与宽度账靠「最后一组末尾挂 `⋯`」这条约定**手工对齐**：`TopBarLayout.centerSlotWidth` 的
    /// `counts[last] += 1` ↔ 这里的 `index == shown.count - 1`。长度不一致时 `zip` 会静默截断、两边各按
    /// 自己那份渲染（宽度账少算一项 → 可能压卡片，正是 `docs/bugs/034` 那条红线）。
    /// 每次渲染（含探针）都过一遍，仅 debug 生效。
    private func checkCenterCounts(_ plan: TopBarPlan) {
        assert(
            plan.centerVisible.count == visibleGroups(for: .center).count,
            "centerVisible 与可见中心组数不一致：宽度账与视图会对不上（TopBarLayout.centerSlotWidth）"
        )
    }

    /// `⋯` 菜单里的行（`Label` + 按钮自己的闭包）。
    @ViewBuilder
    private func overflowMenuRows(_ buttons: [TopBarButton]) -> some View {
        ForEach(buttons) { button in
            Button {
                button.action()
            } label: {
                Label(button.help(), systemImage: button.icon())
            }
            .disabled(!button.enabled())
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

    /// 单元顺序：`⋯` 在最左（= ⌘1–6 导航组的左侧 —— 它替的就是原本在左边的中心槽组），
    /// 其后按注册顺序排可见组。
    private func rightSlotUnits(_ groups: [TopBarGroup], plan: TopBarPlan) -> [RightSlotUnit] {
        // 只有"中心组整组进了 ⋯"时才在右槽单开一颗；留了一部分的话 ⋯ 长在组内（见 renderCenterSlot）
        var units: [RightSlotUnit] = plan.overflowStandalone ? [.overflow] : []
        units += groups.map { .group($0) }
        return units
    }

    /// `⋯`：装降级时从栏上收下来的按钮（中心组整组进 `⋯` 的那些）。
    /// 用原生 `Menu`：行 = `help()` 文案 + `icon()` 图标，直接复用按钮自己的闭包。
    @ViewBuilder
    private func overflowCapsule(interactive: Bool) -> some View {
        if interactive {
            CapsuleOverflowMenu(tooltip: "更多按钮") {
                overflowMenuRows(standaloneOverflowButtons)
            }
        } else {
            // 探针副本：与真实 ⋯ 同尺寸（单图标组）
            CapsuleIcon(systemName: "ellipsis", showBackground: false)
        }
    }

    /// 长在中心组末尾那颗 `⋯` 里装的按钮：各中心组「留在栏上那些之后」的部分（按注册顺序）。
    private var inlineOverflowButtons: [TopBarButton] {
        guard plan.overflowInline else { return [] }
        return zip(visibleGroups(for: .center), plan.centerVisible).flatMap { group, keep in
            keep > 0 ? Array(group.buttons.filter { $0.visible() }.dropFirst(keep)) : []
        }
    }

    /// 右槽那颗独立 `⋯` 里装的按钮：整组进了 `⋯` 的中心组（`keep == 0`），按注册顺序。
    private var standaloneOverflowButtons: [TopBarButton] {
        guard plan.overflowStandalone else { return [] }
        return zip(visibleGroups(for: .center), plan.centerVisible).flatMap { group, keep in
            keep == 0 ? group.buttons.filter { $0.visible() } : []
        }
    }

    private static var groupDivider: some View {
        Divider().frame(height: 14).padding(.horizontal, 4)
    }

    /// 一组按钮。`upTo` = 最多渲染前几个（中心组部分收时用；`nil` = 全渲染）。
    private func buttonRow(_ group: TopBarGroup, interactive: Bool, upTo limit: Int? = nil) -> some View {
        // 先按 visible() 过滤，再基于可见按钮计算分隔线（避免隐藏按钮导致多/少 Divider）
        let visibleButtons = Array(group.buttons.filter { $0.visible() }.prefix(limit ?? Int.max))
        return ForEach(Array(visibleButtons.enumerated()), id: \.element.id) { index, button in
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
            // 圈自己定尺寸（不再吃满 26pt 图标盒），否则贴到 28pt 胶囊的边
            content.background(
                Circle()
                    .fill(.quaternary.opacity(0.5))
                    .frame(width: Layout.topBarHoverCircleSize, height: Layout.topBarHoverCircleSize)
            )
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

/// 带「自动省略」的胶囊组：露出来的按钮 + 末尾 `⋯`（点开是菜单）。
///
/// 露几个由容器算（`TopBarLayout` 的宽度账）—— 本组件**不量宽**，只按 `hostsOverflow` 决定末尾挂不挂 `⋯`，
/// 所以换个地方用只需接上"这次分到多宽"（侧栏按钮行 / 输入栏 pill 行）。
/// 但它是 `private`（同文件抽屉）：**真要去别处用，得先把类型和配套的宽度口径一起提出来**（现在不做）。
/// 见 `docs/design/overflow-collapse/design.md`。
private struct OverflowCapsuleGroup<Content: View, MenuContent: View>: View {
    let interactive: Bool
    let hostsOverflow: Bool
    let tooltip: String
    @ViewBuilder let content: () -> Content
    @ViewBuilder let menuContent: () -> MenuContent

    var body: some View {
        CapsuleGroup {
            content()
            if hostsOverflow {
                Divider().frame(height: 14)
                if interactive {
                    CapsuleOverflowMenu(tooltip: tooltip, menuContent: menuContent)
                } else {
                    // 探针副本：与真实 ⋯ 同尺寸（图标 26 + 组内线 9，宽度账按"多一项"算）
                    CapsuleIcon(systemName: "ellipsis", showBackground: false)
                }
            }
        }
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
