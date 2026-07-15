import SwiftUI

struct ContentView: View {
    @ObservedObject var deviceManager: DeviceManager
    @State private var selectedTab = "chat"
    @State private var isInChat = false
    @State private var lastSyncAt = Date.distantPast
    @Environment(\.scenePhase) private var scenePhase
    @StateObject private var chatViewModel: ChatViewModel
    @State private var conversationLoadError: String?

    @StateObject private var notificationManager = NotificationManager()
    @State private var showAckLoading = false
    @State private var ackErrorMessage: String?
    @State private var showAckError = false

    init(deviceManager: DeviceManager) {
        self.deviceManager = deviceManager
        let api = APIClient(baseURL: deviceManager.baseURL, token: deviceManager.token ?? "")
        _chatViewModel = StateObject(wrappedValue: ChatViewModel(apiClient: api))
    }

    var body: some View {
        ZStack {
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
            .onChange(of: scenePhase) { _, newPhase in
                guard newPhase == .active else { return }
                guard !chatViewModel.isStreaming else { return }
                let now = Date()
                guard now.timeIntervalSince(lastSyncAt) >= 3 else { return }
                lastSyncAt = now

                guard let conv = deviceManager.defaultConversation else { return }
                LogBuffer.shared.debug("sync conversation \(conv.id) after scene active")
                Task { await chatViewModel.loadConversation(id: conv.id, title: conv.title) }

                print("[Reminder] scene active, triggering sync")
                Task {
                    await syncReminders()
                }
            }
            .task {
                guard let conv = deviceManager.defaultConversation else {
                    LogBuffer.shared.error("defaultConversation is nil")
                    conversationLoadError = "无法加载默认对话"
                    return
                }
                LogBuffer.shared.debug("loading conversation \(conv.id)")
                await chatViewModel.loadConversation(id: conv.id, title: conv.title)
            }
            .onReceive(NotificationCenter.default.publisher(for: .reminderTapped)) { notification in
                guard let reminderId = notification.userInfo?["reminder_id"] as? String, !reminderId.isEmpty else { return }
                print("[Reminder] tap: processing reminder_id=\(reminderId)")
                Task { await handleReminderTap(reminderId: reminderId) }
            }
            .onReceive(NotificationCenter.default.publisher(for: .remindersSyncTriggered)) { _ in
                print("[Reminder] SSE triggered, will sync in 15s")
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

    // MARK: - Reminder sync

    private func syncReminders() async {
        guard let token = deviceManager.token else {
            print("[Reminder] sync: skipped, no token")
            return
        }
        print("[Reminder] sync: fetching pending from server")
        let api = APIClient(baseURL: deviceManager.baseURL, token: token)
        do {
            let response = try await api.listReminders(status: "pending")
            print("[Reminder] sync: got \(response.total) pending, items=\(response.items.count)")
            await notificationManager.syncWithServer(pending: response.items)
        } catch {
            print("[Reminder] sync error: \(error)")
        }
    }

    // MARK: - Notification tap → ack → navigate

    private func handleReminderTap(reminderId: String) async {
        print("[Reminder] tap: start processing reminder_id=\(reminderId)")
        showAckLoading = true
        defer {
            showAckLoading = false
            print("[Reminder] tap: loading dismissed")
        }

        guard let token = deviceManager.token else {
            print("[Reminder] tap: no token, aborting")
            ackErrorMessage = "未登录"
            showAckError = true
            return
        }

        print("[Reminder] tap: calling ack API")
        let api = APIClient(baseURL: deviceManager.baseURL, token: token)

        var lastError: Error?
        for attempt in 1...3 {
            print("[Reminder] tap: ack attempt \(attempt)/3")
            do {
                let response = try await api.ackReminder(id: reminderId)
                print("[Reminder] tap: ack success id=\(response.id) status=\(response.status) msg_id=\(response.triggerMessageMsgId) trigger=\(response.triggerMessage.prefix(40))")

                guard response.status == "acknowledged" else {
                    if response.status == "cancelled" {
                        print("[Reminder] tap: reminder already cancelled")
                        ackErrorMessage = "此提醒已取消"
                        showAckError = true
                    } else {
                        print("[Reminder] tap: unexpected status=\(response.status)")
                    }
                    return
                }

                guard let conv = deviceManager.defaultConversation else {
                    print("[Reminder] tap: no default conversation")
                    return
                }
                print("[Reminder] tap: switching to chat tab, loading messages conv=\(conv.id)")
                DispatchQueue.main.async {
                    selectedTab = "chat"
                }
                await chatViewModel.loadConversation(id: conv.id, title: conv.title)
                print("[Reminder] tap: done, chat loaded")
                return
            } catch {
                lastError = error
                let nsErr = error as NSError
                print("[Reminder] tap: ack attempt \(attempt) error code=\(nsErr.code) msg=\(nsErr.localizedDescription)")
                if nsErr.code >= 400 && nsErr.code < 500 {
                    ackErrorMessage = nsErr.localizedDescription
                    showAckError = true
                    return
                }
                if attempt < 3 {
                    let delay = UInt64(attempt) * 1_000_000_000
                    print("[Reminder] tap: retrying in \(attempt)s")
                    try? await Task.sleep(nanoseconds: delay)
                }
            }
        }

        print("[Reminder] tap: all 3 retries failed, last=\(String(describing: lastError))")
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
            if chatViewModel.isInitialLoading && chatViewModel.messages.isEmpty {
                ProgressView()
            } else if let msg = loadError, chatViewModel.messages.isEmpty {
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
