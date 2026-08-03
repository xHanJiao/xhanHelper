import SwiftUI

struct ContentView: View {
    @ObservedObject var deviceManager: DeviceManager
    /// 拖拽调宽的共享幽灵线状态（由 App 持有并注入，本视图只改不观察，
    /// 避免拖拽逐帧重渲染整个 ContentView；子视图通过 @ObservedObject 观察）。
    let resizeGhost: ResizeGhostState
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

    /// 右侧栏在当前窗口/侧栏布局下可用的最大宽度。
    /// 减去左栏元素（内容+外扩）、两个 seam、中间、右栏外扩。
    private var rightSidebarMaxAvailable: Double {
        let available = Double(windowWidth)
            - Double(sidebarWidth + Layout.sidebarOuterPadding)
            - Double(Layout.seamWidth + Layout.middleMinWidth + Layout.seamWidth)
            - Double(Layout.rightSidebarOuterPadding)
        return max(Double(Layout.rightSidebarMinWidth), min(1200, available))
    }

    /// 左侧栏可拖拽的最大宽度。右栏打开时受窗口可用空间钳制（保证右栏不被挤爆）；
    /// 右栏关闭时保持 Layout.sidebarMaxWidth。
    private var leftSidebarMaxAvailable: Double {
        guard rightSidebar.isOpen else { return Double(Layout.sidebarMaxWidth) }
        let available = Double(windowWidth)
            - Double(Layout.sidebarOuterPadding)
            - Double(Layout.middleAndRightMinSpan)
        return min(Double(Layout.sidebarMaxWidth), max(Double(Layout.sidebarMinWidth), available))
    }

    /// 右栏打开时所需的内容最小宽（由 Layout 派生）。
    private static let rightSidebarOpenMinWidth: CGFloat = Layout.rightSidebarOpenMinContentWidth
    /// 右栏关闭时窗口基准最小内容宽。
    private static let baseMinWidth: CGFloat = 400

    /// 把右侧栏宽度钳制到可用范围，保证窄窗口下不溢出、内容可见。
    private func clampRightSidebarWidth() {
        guard rightSidebar.isOpen, windowWidth > 0 else { return }
        rightSidebar.width = min(max(rightSidebar.width, Double(Layout.rightSidebarMinWidth)), rightSidebarMaxAvailable)
    }

    /// 右栏打开时更新窗口：先收左栏腾空间 → 窗口太窄则自动放大 → 设最小内容宽（防再缩窄）。
    private func updateWindowForRightSidebarOpen() {
        // 1. 先收左栏：左栏 + 中间 + 右栏（含各自外扩、seam）放不下时
        if windowWidth < sidebarWidth + Layout.sidebarOuterPadding + Layout.middleAndRightMinSpan && sidebarWidth > Layout.sidebarSnapThreshold {
            sidebarWidth = Layout.sidebarMinWidth
        }

        // 2. 通知窗口最小内容宽（windowWillResize 会据此拒绝更窄）
        AppDelegate.requiredMinContentWidth = Self.rightSidebarOpenMinWidth

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
        AppDelegate.requiredMinContentWidth = Self.baseMinWidth
    }

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
#endif

    init(deviceManager: DeviceManager, resizeGhost: ResizeGhostState) {
        self.deviceManager = deviceManager
        self.resizeGhost = resizeGhost
        let api = APIClient(baseURL: deviceManager.baseURL, token: deviceManager.token ?? "")
        _chatViewModel = StateObject(wrappedValue: ChatViewModel(apiClient: api))
#if os(macOS)
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
#endif
    }

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
                    NavigationToolbar(
                        router: router,
                        rightSidebar: rightSidebar,
                        chatTabManager: chatTabManager,
                        navLeadingOffset: sidebarWidth + Layout.navToolbarContentOffset
                    )

                    GeometryReader { geo in
                        HStack(alignment: .top, spacing: 0) {
                            // 左侧栏卡片
                            SidebarView(
                                selectedTab: sidebarTabBinding,
                                sidebarWidth: $sidebarWidth,
                                chatTabManager: chatTabManager,
                                conversationListVM: conversationListVM,
                                defaultConversationID: deviceManager.defaultConversation?.id
                            )
                            .padding(.bottom, 12)
                            .padding(.horizontal, Layout.sidebarHPadding)
                            .modifier(CardBackgroundModifier(isLifted: sidebarHovered))
                            .padding(.top, 4)
                            .padding(.bottom, 12)
                            .padding(.leading, Layout.sidebarLeadingPadding)
                            .padding(.trailing, Layout.sidebarTrailingPadding)
                            .onHover { sidebarHovered = $0 }
                            SeamMarker(x: $leftSeamX)
                            macOSContentArea
                            if rightSidebar.isOpen {
                                SeamMarker(x: $rightSeamX)
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
                                .padding(.top, 4)
                                .padding(.leading, Layout.rightSidebarLeadingPadding)
                                .padding(.trailing, Layout.rightSidebarTrailingPadding)
                                .transition(.move(edge: .trailing))
                            }
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
                                // 右手柄：浮在右分隔线（seam）上方
                                if rightSidebar.isOpen {
                                    ResizableDivider(
                                        id: "right",
                                        width: $rightSidebar.width,
                                        minWidth: Double(Layout.rightSidebarMinWidth),
                                        maxWidth: rightSidebarMaxAvailable,
                                        reversed: true,
                                        ghost: resizeGhost,
                                        seamX: $rightSeamX
                                    )
                                }
                            }
                        }
                        .animation(.easeInOut(duration: 0.2), value: rightSidebar.isOpen)
                        .onChange(of: geo.size.width) { _, new in
                            windowWidth = new
                            let needCollapse = new < 600
                                || (rightSidebar.isOpen && new < sidebarWidth + Layout.sidebarOuterPadding + Layout.middleAndRightMinSpan)
                            if needCollapse && sidebarWidth > Layout.sidebarSnapThreshold {
                                sidebarWidth = Layout.sidebarMinWidth
                            }
                            clampRightSidebarWidth()
                        }
                        .onChange(of: rightSidebar.isOpen) { _, isOpen in
                            if isOpen {
                                updateWindowForRightSidebarOpen()
                                clampRightSidebarWidth()
                            } else {
                                restoreWindowMinWidth()
                            }
                        }
                        .onChange(of: sidebarWidth) { _, _ in
                            clampRightSidebarWidth()
                        }
                    }
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
                    conversationListVM.defaultConversationID = deviceManager.defaultConversation?.id
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
            .modifier(CardBackgroundModifier(isLifted: contentHovered || (activeChatViewModel?.isEditing ?? false)))
            .padding(.top, 4)
            .padding(.horizontal, 12)
            .padding(.bottom, 12)
            .frame(minWidth: Layout.middleMinWidth, alignment: .leading)
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
