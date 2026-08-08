import SwiftUI

struct ContentView: View {
    @ObservedObject var deviceManager: DeviceManager
    #if os(macOS)
    /// 拖拽调宽的共享幽灵线状态（由 App 持有并注入，本视图只改不观察，
    /// 避免拖拽逐帧重渲染整个 ContentView；子视图通过 @ObservedObject 观察）。
    let resizeGhost: ResizeGhostState
    #endif
    @State private var selectedTab = "chat"
    @State private var isInChat = false
    @State private var lastSyncAt = Date.distantPast
    @Environment(\.scenePhase) private var scenePhase
    @Environment(\.colorScheme) private var colorScheme
    @StateObject private var chatViewModel: ChatViewModel
    @State private var conversationLoadError: String?

    @StateObject private var notificationManager = NotificationManager()
    @State private var showAckLoading = false
    @State private var ackErrorMessage: String?
    @State private var showAckError = false

#if os(macOS)
    @StateObject private var router = NavigationRouter()
    @StateObject private var rightSidebar = RightSidebarManager()
    @State private var sidebarWidth: CGFloat = Layout.sidebarDefaultWidth
    @State private var contentHovered = false
    @State private var sidebarHovered = false
    /// 左/右面板分隔线（seam）的 x 坐标（content 坐标系），由 SeamMarker 上报。
    @State private var leftSeamX: CGFloat = 0
    @State private var rightSeamX: CGFloat = 0
    @AppStorage("xhan_sidebar_width") private var storedSidebarWidth: Double = Layout.sidebarDefaultWidth
    @State private var windowWidth: CGFloat = 800
    /// 侧栏是否为自动收起，窗口拉宽后自动恢复。
    @State private var sidebarAutoCollapsed = false
    /// 进入 PiP 前右栏宽度，退出时恢复。
    @State private var prePipSidebarWidth: Double?
    /// 进入浏览器聚焦（middleMinimized）前右栏宽度，退出时恢复。
    @State private var preFocusSidebarWidth: Double?
    /// PiP 自动退出原因（onChange 里消费，避免状态翻转与副作用耦合）。
    @State private var pipExitReason = "userExit"

    @StateObject private var agentVM: AgentViewModel
    @StateObject private var billVM: BillViewModel
    @StateObject private var memoryVM: MemoryViewModel
    @StateObject private var noteVM: NoteViewModel
    @StateObject private var documentVM: DocumentViewModel
    @StateObject private var themeVM: ThemeViewModel
    @StateObject private var chatTabManager: ChatTabManager
    @StateObject private var conversationListVM: ConversationListViewModel

    private var api: APIClient {
        APIClient(baseURL: deviceManager.baseURL, token: deviceManager.token ?? "")
    }

    private var sidebarTabBinding: Binding<SidebarTab> {
        Binding(
            get: { router.currentTab },
            set: { router.navigateTo($0) }
        )
    }

    private var allowExpand: Bool { windowWidth >= 600 }

    /// 中间区跨度：PiP 时聊天区消失，只剩一个 seam（布局让位给右栏浏览器）。
    private var middleSpan: Double {
        rightSidebar.isPipChat
            ? Double(Layout.seamWidth)
            : Double(Layout.seamWidth + Layout.middleMinWidth + Layout.seamWidth)
    }

    /// 中间区 + 右栏最小宽的横向跨度（pip 感知）。
    private var middleAndRightMinSpanPipAware: Double {
        middleSpan + Double(Layout.rightSidebarMinWidth + Layout.rightSidebarOuterPadding)
    }

    /// 右侧栏在当前窗口/侧栏布局下可用的最大宽度。
    /// 减去左栏元素（内容+外扩）、中间跨度、右栏外扩。PiP 或浏览器聚焦时中间让位，
    /// 右栏可铺满整窗（不设 1200 上限）。
    private var rightSidebarMaxAvailable: Double {
        let available = Double(windowWidth)
            - Double(sidebarWidth + Layout.sidebarOuterPadding)
            - middleSpan
            - Double(Layout.rightSidebarOuterPadding)
        let cap = (rightSidebar.isPipChat || rightSidebar.middleMinimized) ? 4000.0 : 1200.0
        return max(Double(Layout.rightSidebarMinWidth), min(cap, available))
    }

    /// 左侧栏可拖拽的最大宽度。右栏打开时受窗口可用空间钳制（保证右栏不被挤爆）；
    /// 右栏关闭时保持 Layout.sidebarMaxWidth。
    private var leftSidebarMaxAvailable: Double {
        guard rightSidebar.isOpen else { return Double(Layout.sidebarMaxWidth) }
        let available = Double(windowWidth)
            - Double(Layout.sidebarOuterPadding)
            - middleAndRightMinSpanPipAware
        return min(Double(Layout.sidebarMaxWidth), max(Double(Layout.sidebarMinWidth), available))
    }

    /// 右栏打开时所需的内容最小宽（由 Layout 派生）。
    private static let rightSidebarOpenMinWidth: CGFloat = Layout.rightSidebarOpenMinContentWidth
    /// 右栏关闭时窗口基准最小内容宽。
    private static let baseMinWidth: CGFloat =
        Layout.cardGap * 2
        + Layout.sidebarMinWidth + Layout.sidebarHPadding * 2
        + Layout.seamHalfGap * 2 + Layout.seamWidth
        + Layout.middleMinWidth
    /// PiP 时所需的内容最小宽（左栏收起 + seam + 右栏最小）。
    private static let pipMinContentWidth: CGFloat =
        Layout.sidebarMinWidth + Layout.sidebarOuterPadding
        + Layout.seamWidth
        + Layout.rightSidebarMinWidth + Layout.rightSidebarOuterPadding

    /// 窗口最小内容宽唯一派生入口：由 (isOpen, isPipChat) 实时推导，幂等设置，
    /// 消除"进入保存/退出恢复"的竞态。
    private func updateWindowMinWidth() {
        let minContent: CGFloat
        if rightSidebar.isOpen {
            minContent = rightSidebar.isPipChat ? Self.pipMinContentWidth : Self.rightSidebarOpenMinWidth
        } else {
            minContent = Self.baseMinWidth
        }
        AppDelegate.requiredMinContentWidth = minContent
    }

    /// 把右侧栏宽度钳制到可用范围，保证窄窗口下不溢出、内容可见。
    /// PiP 时改为"填满"：左栏变窄/窗口变宽后右栏自动补位占满释放的空间。
    private func clampRightSidebarWidth() {
        guard rightSidebar.isOpen, windowWidth > 0 else { return }
        if rightSidebar.isPipChat {
            let fill = rightSidebarMaxAvailable
            if fill != rightSidebar.width {
                pipLog("right sidebar fill: \(Int(rightSidebar.width)) -> \(Int(fill))")
                rightSidebar.width = fill
            }
        } else {
            let clamped = min(max(rightSidebar.width, Double(Layout.rightSidebarMinWidth)), rightSidebarMaxAvailable)
            if clamped != rightSidebar.width {
                pipLog("right sidebar clamp: \(Int(rightSidebar.width)) -> \(Int(clamped))")
                rightSidebar.width = clamped
            }
        }
    }

    /// 右栏打开时更新窗口：先收左栏腾空间 → 窗口太窄则自动放大 → 设最小内容宽（防再缩窄）。
    private func updateWindowForRightSidebarOpen() {
        // 1. 先收左栏：左栏 + 中间 + 右栏（含各自外扩、seam）放不下时
        if windowWidth < sidebarWidth + Layout.sidebarOuterPadding + Layout.middleAndRightMinSpan && sidebarWidth > Layout.sidebarSnapThreshold {
            sidebarWidth = Layout.sidebarMinWidth
        }

        // 2. 通知窗口最小内容宽（windowWillResize 会据此拒绝更窄）
        updateWindowMinWidth()

        // 3. 自动放大窗口到所需内容宽（按内容缺口增量放大，保留窗口 chrome）
        guard let window = NSApp.keyWindow else { return }
        let deficit = Self.rightSidebarOpenMinWidth - windowWidth
        if deficit > 0 {
            var frame = window.frame
            frame.size.width += deficit
            window.setFrame(frame, display: true, animate: true)
        }
    }

    /// 右栏关闭时恢复窗口最小内容宽。
    private func restoreWindowMinWidth() {
        updateWindowMinWidth()
    }

    // MARK: - 聊天画中画（PiP）

    /// 进入 PiP 副作用（由 onChange(of: isPipChat) 驱动，幂等）。
    private func enterPipEffects() {
        guard rightSidebar.isPipChat else { return }
        rightSidebar.pipCollapsed = false
        // 非聊天 tab 时进入 PiP 会隐藏该页面，先切到聊天，让悬浮面板承载对话
        if router.currentTab != .chat {
            router.navigateTo(.chat)
        }
        prePipSidebarWidth = rightSidebar.width
        pipExitReason = "userExit"
        rightSidebar.width = rightSidebarMaxAvailable
        updateWindowMinWidth()
        guard let window = NSApp.keyWindow else { return }
        let deficit = Self.pipMinContentWidth - windowWidth
        if deficit > 0 {
            var frame = window.frame
            frame.size.width += deficit
            window.setFrame(frame, display: true, animate: true)
        }
        pipLog("enter: prePipWidth=\(Int(prePipSidebarWidth ?? 0)) windowW=\(Int(windowWidth))")
    }

    /// 退出 PiP 副作用（onChange 统一入口，reason 用于诊断）。
    private func exitPipEffects(reason: String) {
        let restored = min(prePipSidebarWidth ?? rightSidebar.width, rightSidebarMaxAvailable)
        prePipSidebarWidth = nil
        withAnimation(.easeInOut(duration: 0.2)) {
            rightSidebar.width = restored
        }
        updateWindowMinWidth()
        pipLog("exit: reason=\(reason) restoredWidth=\(Int(restored)) maxAvailable=\(Int(rightSidebarMaxAvailable))")
    }

    /// 自动退出触发点统一入口：先记 reason，再翻转状态（副作用由 onChange 消费）。
    private func autoExitPip(reason: String) {
        guard rightSidebar.isPipChat else { return }
        pipExitReason = reason
        rightSidebar.isPipChat = false
    }

    // MARK: - 浏览器聚焦（中间栏缩到最小宽）

    /// 进入浏览器聚焦副作用（onChange(of: middleMinimized) 驱动，幂等）。
    private func enterBrowserFocusEffects() {
        guard rightSidebar.middleMinimized else { return }
        if rightSidebar.isPipChat { return } // PiP 已让中间栏让位，无需重复处理
        preFocusSidebarWidth = rightSidebar.width
        withAnimation(.easeInOut(duration: 0.2)) {
            rightSidebar.width = rightSidebarMaxAvailable
        }
        updateWindowMinWidth()
    }

    /// 窗口变宽后浏览器聚焦态补满（onChange(of: geo.size.width) 触发，幂等）。
    private func refillBrowserFocus() {
        guard rightSidebar.middleMinimized, !rightSidebar.isPipChat, rightSidebar.isOpen else { return }
        let fill = rightSidebarMaxAvailable
        if fill != rightSidebar.width {
            withAnimation(.easeInOut(duration: 0.2)) {
                rightSidebar.width = fill
            }
            pipLog("browser focus: refill \(Int(rightSidebar.width)) -> \(Int(fill))")
        }
    }

    /// 退出浏览器聚焦副作用（onChange 统一入口，reason 用于诊断）。
    private func exitBrowserFocusEffects(reason: String) {
        let restored = min(preFocusSidebarWidth ?? rightSidebar.width, rightSidebarMaxAvailable)
        preFocusSidebarWidth = nil
        withAnimation(.easeInOut(duration: 0.2)) {
            rightSidebar.width = restored
        }
        updateWindowMinWidth()
        pipLog("browser focus: exit reason=\(reason) restoredWidth=\(Int(restored))")
    }
#endif

    /// 当前活跃的聊天 VM（macOS 为当前聊天 tab；iOS 为全局单例）
    private var activeChatViewModel: ChatViewModel? {
        #if os(macOS)
        return chatTabManager.currentTab?.viewModel
        #else
        return chatViewModel
        #endif
    }

    private func syncActiveChat() async {
        #if os(macOS)
        // 激活时刷新当前所在对话（而非默认会话），避免劫持其他会话 tab
        guard let tab = chatTabManager.currentTab,
              !tab.isBlank,
              let cid = tab.conversationId, !cid.isEmpty else { return }
        LogBuffer.shared.debug("sync conversation \(cid) after scene active (macOS)")
        await tab.viewModel.loadConversation(id: cid, title: tab.title, mode: tab.viewModel.mode, preserveTrustedMode: true) // scene 激活刷新：保持当前模式/信任模式
        #else
        guard let conv = deviceManager.defaultConversation else { return }
        LogBuffer.shared.debug("sync conversation \(conv.id) after scene active")
        await chatViewModel.loadConversation(id: conv.id, title: conv.title, mode: conv.mode)
        #endif
    }

#if os(macOS)
    /// Cmd+B：切换左侧栏。收起时恢复到记忆的展开宽度，展开时收到最小宽（48）。
    private func toggleLeftSidebar() {
        let collapsed = sidebarWidth <= Layout.sidebarSnapThreshold
        if collapsed {
            sidebarWidth = min(max(CGFloat(storedSidebarWidth), Layout.sidebarDefaultWidth), Layout.sidebarMaxWidth)
        } else {
            sidebarWidth = Layout.sidebarMinWidth
        }
    }

    /// Cmd+Shift+↑ / ↓：在会话列表中上/下切换会话（delta：-1 上移，+1 下移）。
    /// 当前会话不在已加载列表（新建空 tab / 分页未加载）时回退到列表第一项（最近会话）。
    private func switchConversation(delta: Int) async {
        if !conversationListVM.hasLoadedOnce {
            await conversationListVM.load()
        }
        let conversations = conversationListVM.conversations
        guard !conversations.isEmpty else { return }

        let currentID = chatTabManager.currentConversationId
        let target: Conversation
        if let idx = conversations.firstIndex(where: { $0.id == currentID }) {
            let newIdx = min(max(idx + delta, 0), conversations.count - 1)
            target = conversations[newIdx]
        } else {
            target = conversations[0]
        }

        router.navigateTo(.chat)
        await chatTabManager.openConversation(id: target.id, title: target.title, pushHistory: true, mode: target.mode)
    }

    /// 注册 App 级快捷键桥（macOS：Cmd+B / Tab·Esc L3 确认）。
    private func registerAppShortcutBridge() {
        // ContentView 是 struct，闭包捕获的是值拷贝，无循环引用，不需 weak
        AppShortcutBridge.shared.onToggleLeftSidebar = {
            self.toggleLeftSidebar()
        }
        AppShortcutBridge.shared.onNewChat = {
            self.chatTabManager.newChat()
        }
        AppShortcutBridge.shared.onSwitchConversation = { delta in
            Task { await self.switchConversation(delta: delta) }
        }
        AppShortcutBridge.shared.confirmationPending = {
            self.activeChatViewModel?.pendingToolConfirmation != nil
        }
        AppShortcutBridge.shared.resolveConfirmation = { allow in
            self.activeChatViewModel?.resolveToolConfirmation(allow: allow)
        }
    }
#endif

    #if os(macOS)
    init(deviceManager: DeviceManager, resizeGhost: ResizeGhostState) {
        self.deviceManager = deviceManager
        self.resizeGhost = resizeGhost
        let api = APIClient(baseURL: deviceManager.baseURL, token: deviceManager.token ?? "")
        _chatViewModel = StateObject(wrappedValue: ChatViewModel(apiClient: api))
        _agentVM = StateObject(wrappedValue: AgentViewModel(apiClient: api))
        _billVM = StateObject(wrappedValue: BillViewModel(apiClient: api))
        _memoryVM = StateObject(wrappedValue: MemoryViewModel(apiClient: api))
        _noteVM = StateObject(wrappedValue: NoteViewModel(apiClient: api))
        _documentVM = StateObject(wrappedValue: DocumentViewModel(apiClient: api))
        _themeVM = StateObject(wrappedValue: ThemeViewModel(apiClient: api))
        _chatTabManager = StateObject(wrappedValue: ChatTabManager {
            ChatViewModel(apiClient: APIClient(baseURL: deviceManager.baseURL, token: deviceManager.token ?? ""))
        })
        _conversationListVM = StateObject(wrappedValue: ConversationListViewModel(
            apiClient: APIClient(baseURL: deviceManager.baseURL, token: deviceManager.token ?? "")
        ))
    }
    #else
    init(deviceManager: DeviceManager) {
        self.deviceManager = deviceManager
        let api = APIClient(baseURL: deviceManager.baseURL, token: deviceManager.token ?? "")
        _chatViewModel = StateObject(wrappedValue: ChatViewModel(apiClient: api))
    }
    #endif

    var body: some View {
        ZStack {
            Group {
#if os(iOS)
                TabView(selection: $selectedTab) {
                    NavigationStack {
                        ConversationListContentView(deviceManager: deviceManager, isInChat: $isInChat, chatViewModel: chatViewModel, loadError: conversationLoadError)
                    }
                    .toolbarBackground(Color("chatBackground"), for: .tabBar)
                    .toolbarBackground(.visible, for: .tabBar)
                    .tabItem {
                        Label("聊天", systemImage: "bubble.left.and.bubble.right")
                    }
                    .tag("chat")

                    NavigationStack {
                        SettingsView(deviceManager: deviceManager)
                    }
                    .toolbarBackground(Color("chatBackground"), for: .tabBar)
                    .toolbarBackground(.visible, for: .tabBar)
                    .tabItem {
                        Label("设置", systemImage: "gear")
                    }
                    .tag("settings")
                }
#else
                VStack(spacing: 0) {
                    TopBarView(
                        rightSidebar: rightSidebar,
                        navLeadingOffset: sidebarWidth + Layout.navToolbarContentOffset
                    )
                    .zIndex(1)

                    GeometryReader { geo in
                        let rightMax: Double = max(Double(Layout.rightSidebarMinWidth),
                            min((rightSidebar.isPipChat || rightSidebar.middleMinimized) ? 4000 : 1200,
                                Double(geo.size.width)
                                - Double(sidebarWidth + Layout.sidebarOuterPadding)
                                - middleSpan
                                - Double(Layout.rightSidebarOuterPadding)))
                        HStack(alignment: .top, spacing: 0) {
                            HSpace(Layout.cardGap)

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

                            if !rightSidebar.isPipChat {
                                macOSContentArea
                            }
                            if rightSidebar.isOpen {
                                HSpace(Layout.seamHalfGap)
                                SeamMarker(x: $rightSeamX)
                                HSpace(Layout.seamHalfGap)

                                RightSidebarView(
                                    manager: rightSidebar,
                                    billVM: billVM,
                                    memoryVM: memoryVM,
                                    noteVM: noteVM,
                                    documentVM: documentVM,
                                    themeVM: themeVM,
                                    baseURL: deviceManager.baseURL,
                                    token: deviceManager.token ?? ""
                                )
                                .frame(width: rightSidebar.isPipChat ? nil : rightSidebar.width)
                                .transition(.move(edge: .trailing))
                            }

                            HSpace(Layout.cardGap)
                        }
                        .coordinateSpace(name: ResizeDividerSpace.name)
                        .overlay(alignment: .topLeading) {
                            ZStack(alignment: .topLeading) {
                                // 左手柄：浮在左分隔线（seam）上方，不占布局空间
                                if allowExpand {
                                    ResizableDivider(
                                        id: "left",
                                        width: Binding(
                                            get: { Double(sidebarWidth) },
                                            set: { sidebarWidth = CGFloat($0) }
                                        ),
                                        minWidth: Double(Layout.sidebarMinWidth),
                                        maxWidth: leftSidebarMaxAvailable,
                                        ghost: resizeGhost,
                                        seamX: $leftSeamX
                                    )
                                }
                                // 右手柄：浮在右分隔线（seam）上方（PiP 时中间区消失、两 seam 重合，隐藏）
                                if rightSidebar.isOpen && !rightSidebar.isPipChat {
                                    ResizableDivider(
                                        id: "right",
                                        width: $rightSidebar.width,
                                        minWidth: Double(Layout.rightSidebarMinWidth),
                                        maxWidth: rightMax,
                                        reversed: true,
                                        ghost: resizeGhost,
                                        seamX: $rightSeamX,
                                        onDragStart: {
                                            // 手动拖动中↔右缝隙 = 退出浏览器聚焦：
                                            // 清空 preFocus 避免恢复跳变（拖拽本身提供新宽度），
                                            // 中栏随之可自由伸缩接管余量。
                                            if rightSidebar.middleMinimized {
                                                preFocusSidebarWidth = nil
                                                rightSidebar.middleMinimized = false
                                            }
                                        }
                                    )
                                }
                            }
                        }
                        .overlay(alignment: .topLeading) {
                            // 聊天画中画面板：悬浮在右栏浏览器之上，可拖动/吸附/收起
                            if rightSidebar.isPipChat, let vm = chatTabManager.currentTab?.viewModel {
                                FloatingChatPanel(
                                    viewModel: vm,
                                    manager: rightSidebar,
                                    loadError: conversationLoadError,
                                    baseURL: deviceManager.baseURL,
                                    token: deviceManager.token ?? "",
                                    containerSize: geo.size,
                                    onExit: { autoExitPip(reason: "userExit") }
                                )
                            }
                        }
                        .animation(.easeInOut(duration: 0.2), value: rightSidebar.isOpen)
                        .onChange(of: geo.size.width, initial: true) { _, new in
                            windowWidth = new
                            let extra: CGFloat = rightSidebar.isPipChat ? 396
                                : rightSidebar.isOpen ? 736
                                : 385
                            if new < sidebarWidth + extra && sidebarWidth > Layout.sidebarMinWidth {
                                sidebarWidth = Layout.sidebarMinWidth
                                sidebarAutoCollapsed = true
                            } else if sidebarAutoCollapsed && new >= CGFloat(storedSidebarWidth) + extra {
                                sidebarWidth = min(CGFloat(storedSidebarWidth), Layout.sidebarMaxWidth)
                                sidebarAutoCollapsed = false
                            }
                            clampRightSidebarWidth()
                            refillBrowserFocus() // 窗口放大后浏览器聚焦态补满（含进入时异步放大窗口的场景）
                        }
                        .onChange(of: rightSidebar.isOpen) { _, isOpen in
                            if !isOpen {
                                autoExitPip(reason: "sidebarClosed")
                                // 关闭右侧栏时退出浏览器聚焦，恢复中间栏宽度
                                rightSidebar.middleMinimized = false
                            }
                            if isOpen {
                                updateWindowForRightSidebarOpen()
                                clampRightSidebarWidth()
                            } else {
                                restoreWindowMinWidth()
                            }
                            // 浏览器按钮组随侧栏可见性注册/注销（修复：侧栏收起时左槽组残留）
                            BrowserToolProvider.shared.refreshBrowserGroup()
                        }
                        .onChange(of: rightSidebar.middleMinimized) { _, minimized in
                            if minimized {
                                enterBrowserFocusEffects()
                            } else {
                                exitBrowserFocusEffects(reason: "userExit")
                            }
                            clampRightSidebarWidth()
                            refillBrowserFocus()
                        }
                        .onChange(of: rightSidebar.isPipChat) { _, isPip in
                            if isPip {
                                enterPipEffects()
                            } else {
                                exitPipEffects(reason: pipExitReason)
                            }
                            clampRightSidebarWidth()
                        }
                        .onChange(of: router.currentTab) { _, newTab in
                            if newTab != .chat {
                                autoExitPip(reason: "tabChanged")
                            }
                        }
                        .onChange(of: rightSidebar.tabs.count) { _, count in
                            if count == 0 {
                                autoExitPip(reason: "browserEmpty")
                            }
                        }
                        .onChange(of: rightSidebar.activeTabId) { _, id in
                            if id == nil {
                                rightSidebar.middleMinimized = false
                                autoExitPip(reason: "browsePanel")
                            }
                            BrowserToolProvider.shared.refreshBrowserGroup()
                        }
                        .onChange(of: sidebarWidth) { _, new in
                            if new > Layout.sidebarMinWidth {
                                sidebarAutoCollapsed = false
                            }
                            clampRightSidebarWidth()
                            refillBrowserFocus()
                        }
                    }
                    VSpace(Layout.cardBottomGap)
                }
                .overlay(alignment: .top) {
                    VStack(spacing: 0) {
                        Color(nsColor: .clear)
                            .frame(height: 26)
                    }
                    .allowsHitTesting(false)
                }
                .ignoresSafeArea(edges: .top)
                .onAppear {
                    sidebarWidth = min(storedSidebarWidth, Layout.sidebarMaxWidth)
                    BrowserToolProvider.shared.rightSidebar = rightSidebar
                    DocumentSelectionCoordinator.shared.rightSidebar = rightSidebar
                    DocumentSelectionCoordinator.shared.ensureRegisteredGroup()
                    conversationListVM.defaultConversationID = deviceManager.defaultConversation?.id
                    registerAppShortcutBridge()
                }
                .onChange(of: sidebarWidth) { _, new in
                    if new > Layout.sidebarSnapThreshold {
                        storedSidebarWidth = new
                    }
                }
#endif
            }
            .onChange(of: scenePhase) { _, newPhase in
                guard newPhase == .active else { return }
                guard !(activeChatViewModel?.isStreaming ?? false) else { return }
                let now = Date()
                guard now.timeIntervalSince(lastSyncAt) >= 3 else { return }
                lastSyncAt = now

                Task { await syncActiveChat() }

                Task { await syncReminders() }
            }
#if os(macOS)
            .onReceive(NotificationCenter.default.publisher(for: NSApplication.didBecomeActiveNotification)) { _ in
                guard !(activeChatViewModel?.isStreaming ?? false) else { return }
                let now = Date()
                guard now.timeIntervalSince(lastSyncAt) >= 3 else { return }
                lastSyncAt = now

                Task { await syncActiveChat() }

                Task { await syncReminders() }
            }
#endif
            .task(id: deviceManager.defaultConversation?.id) {
                guard let conv = deviceManager.defaultConversation else {
                    #if os(macOS)
                    #else
                    chatViewModel.isInitialLoading = false
                    #endif
                    return
                }
                LogBuffer.shared.debug("loading conversation \(conv.id)")
                #if os(macOS)
                await chatTabManager.openConversation(id: conv.id, title: conv.title, mode: conv.mode)
                #else
                await chatViewModel.loadConversation(id: conv.id, title: conv.title, mode: conv.mode)
                #endif
            }
            .onReceive(NotificationCenter.default.publisher(for: .reminderTapped)) { notification in
                guard let reminderId = notification.userInfo?["reminder_id"] as? String, !reminderId.isEmpty else { return }
                LogBuffer.shared.info("[Reminder] tap: processing reminder_id=\(reminderId)")
                Task { await handleReminderTap(reminderId: reminderId) }
            }
            .onReceive(NotificationCenter.default.publisher(for: .remindersSyncTriggered)) { _ in
                DispatchQueue.main.async {
                    Task {
                        try? await Task.sleep(nanoseconds: 15_000_000_000)
                        await syncReminders()
                    }
                }
            }

            if showAckLoading {
                Color.black.opacity(0.4).ignoresSafeArea()
                VStack(spacing: 16) {
                    ProgressView()
                        .scaleEffect(1.5)
                    Text("处理中...")
                        .foregroundColor(.white)
                        .font(.headline)
                }
                .frame(minWidth: 120, minHeight: 120)
                .background(.ultraThinMaterial, in: RoundedRectangle(cornerRadius: 16))
            }
        }
        .alert("提醒通知", isPresented: $showAckError) {
            Button("好", role: .cancel) {}
        } message: {
            Text(ackErrorMessage ?? "未知错误")
        }
    }

#if os(macOS)
    @ViewBuilder
    private var macOSContentArea: some View {
        Group {
            switch router.currentTab {
                case .chat:
                    if let vm = chatTabManager.currentTab?.viewModel {
                        ChatPage(
                            chatViewModel: vm,
                            loadError: conversationLoadError,
                            baseURL: deviceManager.baseURL,
                            token: deviceManager.token ?? ""
                        )
                    } else {
                        ProgressView()
                            .frame(maxWidth: .infinity, maxHeight: .infinity)
                    }
                case .agents:
                    AgentPage(router: router, viewModel: agentVM)
                }
            }
            .padding(.horizontal, 12)
            .modifier(CardBackgroundModifier(isLifted: contentHovered || (activeChatViewModel?.isEditing ?? false)))
            .frame(minWidth: Layout.middleMinWidth, idealWidth: Layout.middleMinWidth,
                   maxWidth: .infinity,
                   alignment: .leading)
            .layoutPriority(1)
            .onHover { contentHovered = $0 }
        .onChange(of: activeChatViewModel?.isStreaming) { _, streaming in
            router.setRefreshDisabled(for: .chat,
                message: streaming == true ? "对话进行中，无法刷新" : nil)
        }
        .onChange(of: router.refreshCounts[.chat] ?? 0) { _, _ in
            if let vm = activeChatViewModel, let cid = vm.conversationId, !cid.isEmpty {
                Task { await vm.loadConversation(id: cid, title: vm.conversationTitle ?? "", mode: vm.mode, preserveTrustedMode: true) } // 刷新保持当前模式/信任模式
            }
            Task { await conversationListVM.reload() }
        }
    }
#endif

    // MARK: - Reminder sync

    private func syncReminders() async {
        guard let token = deviceManager.token else { return }
        let api = APIClient(baseURL: deviceManager.baseURL, token: token)
        do {
            let response = try await api.listReminders(status: "pending")
            await notificationManager.syncWithServer(pending: response.items)
        } catch {
            LogBuffer.shared.error("[Reminder] sync error: \(error)")
        }
    }

    // MARK: - Notification tap → ack → navigate

    private func handleReminderTap(reminderId: String) async {
        showAckLoading = true
        defer { showAckLoading = false }

        guard let token = deviceManager.token else {
            ackErrorMessage = "未登录"
            showAckError = true
            return
        }

        let api = APIClient(baseURL: deviceManager.baseURL, token: token)

        var lastError: Error?
        for attempt in 1...3 {
            do {
                let response = try await api.ackReminder(id: reminderId)
                LogBuffer.shared.info("[Reminder] ack: success id=\(response.id) trigger=\(response.triggerMessage.prefix(40))")

                guard response.status == "acknowledged" else {
                    if response.status == "cancelled" {
                        ackErrorMessage = "此提醒已取消"
                        showAckError = true
                    }
                    return
                }

                guard let conv = deviceManager.defaultConversation else { return }
                DispatchQueue.main.async {
#if os(macOS)
                    router.navigateTo(.chat)
#else
                    selectedTab = "chat"
#endif
                }
#if os(macOS)
                await chatTabManager.openConversation(id: conv.id, title: conv.title, mode: conv.mode)
#else
                await chatViewModel.loadConversation(id: conv.id, title: conv.title, mode: conv.mode)
#endif
                return
            } catch {
                lastError = error
                let nsErr = error as NSError
                if nsErr.code >= 400 && nsErr.code < 500 {
                    ackErrorMessage = nsErr.localizedDescription
                    showAckError = true
                    return
                }
                if attempt < 3 {
                    try? await Task.sleep(nanoseconds: UInt64(attempt) * 1_000_000_000)
                }
            }
        }

        LogBuffer.shared.error("[Reminder] ack: all retries failed, last=\(String(describing: lastError))")
        ackErrorMessage = "网络错误，请稍后重试"
        showAckError = true
    }
}

struct ConversationListContentView: View {
    @ObservedObject var deviceManager: DeviceManager
    @Binding var isInChat: Bool
    @ObservedObject var chatViewModel: ChatViewModel
    let loadError: String?

    var body: some View {
        ZStack {
            if let msg = loadError, chatViewModel.messages.isEmpty {
                VStack(spacing: 12) {
                    Text(msg).foregroundColor(.secondary)
                }
            } else {
                ChatView(viewModel: chatViewModel, agentId: nil, baseURL: deviceManager.baseURL, token: deviceManager.token ?? "")
                    .onAppear { isInChat = true }
                    .onDisappear { isInChat = false }
            }
        }
    }
}

#if os(macOS)
struct OpenSettingsButton: View {
    @Environment(\.openSettings) private var openSettings
    var body: some View {
        Button {
            openSettings()
        } label: {
            Image(systemName: "gear")
        }
    }
}
#endif
