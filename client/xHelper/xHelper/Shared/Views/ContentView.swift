import SwiftUI

struct ContentView: View {
    @ObservedObject var deviceManager: DeviceManager
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
    @State private var selectedSidebarTab: SidebarTab = .chat
    @State private var sidebarWidth: CGFloat = Layout.sidebarDefaultWidth
    @AppStorage("xhan_sidebar_width") private var storedSidebarWidth: Double = Layout.sidebarDefaultWidth
    @State private var windowWidth: CGFloat = 800

    private var api: APIClient {
        APIClient(baseURL: deviceManager.baseURL, token: deviceManager.token ?? "")
    }

    private var allowExpand: Bool { windowWidth >= 600 }
#endif

    init(deviceManager: DeviceManager) {
        self.deviceManager = deviceManager
        let api = APIClient(baseURL: deviceManager.baseURL, token: deviceManager.token ?? "")
        _chatViewModel = StateObject(wrappedValue: ChatViewModel(apiClient: api))
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
                GeometryReader { geo in
                    HStack(alignment: .top, spacing: 0) {
                        SidebarView(
                            selectedTab: $selectedSidebarTab,
                            sidebarWidth: $sidebarWidth,
                            allowExpand: allowExpand
                        )
                        .padding(.bottom, 12)
                        .padding(.horizontal, 12)
                        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 12))
                        .shadow(color: .black.opacity(0.08), radius: 6, y: 2)
                        .padding(.top, 34)
                        .padding(.bottom, 12)
                        .padding(.leading, 8)
                        .padding(.trailing, 12)

                        macOSContentArea
                    }
                    .onChange(of: geo.size.width) { _, new in
                        windowWidth = new
                        if new < 600 && sidebarWidth > Layout.sidebarSnapThreshold {
                            sidebarWidth = Layout.sidebarMinWidth
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
                    sidebarWidth = storedSidebarWidth
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
                guard !chatViewModel.isStreaming else { return }
                let now = Date()
                guard now.timeIntervalSince(lastSyncAt) >= 3 else { return }
                lastSyncAt = now

                guard let conv = deviceManager.defaultConversation else { return }
                LogBuffer.shared.debug("sync conversation \(conv.id) after scene active")
                Task { await chatViewModel.loadConversation(id: conv.id, title: conv.title) }

                Task { await syncReminders() }
            }
#if os(macOS)
            .onReceive(NotificationCenter.default.publisher(for: NSApplication.didBecomeActiveNotification)) { _ in
                guard !chatViewModel.isStreaming else { return }
                let now = Date()
                guard now.timeIntervalSince(lastSyncAt) >= 3 else { return }
                lastSyncAt = now

                guard let conv = deviceManager.defaultConversation else { return }
                LogBuffer.shared.debug("sync conversation \(conv.id) after app became active (macOS)")
                Task { await chatViewModel.loadConversation(id: conv.id, title: conv.title) }

                Task { await syncReminders() }
            }
#endif
            .task(id: deviceManager.defaultConversation?.id) {
                guard let conv = deviceManager.defaultConversation else {
                    chatViewModel.isInitialLoading = false
                    return
                }
                LogBuffer.shared.debug("loading conversation \(conv.id)")
                await chatViewModel.loadConversation(id: conv.id, title: conv.title)
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
        switch selectedSidebarTab {
        case .chat:
            ZStack {
                if let msg = conversationLoadError, chatViewModel.messages.isEmpty {
                    VStack(spacing: 12) {
                        Text(msg).foregroundColor(.secondary)
                    }
                } else {
                    ChatView(viewModel: chatViewModel, agentId: nil, baseURL: deviceManager.baseURL, token: deviceManager.token ?? "")
                        .onAppear { isInChat = true }
                        .onDisappear { isInChat = false }
                }
            }
        case .agents:
            AgentListView(viewModel: AgentViewModel(apiClient: api))
        case .bills:
            BillListView(
                viewModel: BillViewModel(apiClient: api),
                baseURL: deviceManager.baseURL,
                token: deviceManager.token ?? ""
            )
        case .memories:
            MemoryListView(viewModel: MemoryViewModel(apiClient: api))
        case .notes:
            NoteListView(viewModel: NoteViewModel(apiClient: api))
        case .documents:
            DocumentListView(viewModel: DocumentViewModel(apiClient: api))
        case .themes:
            ThemeListView(viewModel: ThemeViewModel(apiClient: api))
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
                    selectedTab = "chat"
                }
                await chatViewModel.loadConversation(id: conv.id, title: conv.title)
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
