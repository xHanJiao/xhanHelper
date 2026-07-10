import SwiftUI

struct ContentView: View {
    @ObservedObject var deviceManager: DeviceManager
    @State private var selectedTab = "chat"
    @State private var isInChat = false
    @State private var lastSyncAt = Date.distantPast
    @Environment(\.scenePhase) private var scenePhase
    @StateObject private var chatViewModel: ChatViewModel
    @State private var conversationLoadError: String?

    init(deviceManager: DeviceManager) {
        self.deviceManager = deviceManager
        let api = APIClient(baseURL: deviceManager.baseURL, token: deviceManager.token ?? "")
        _chatViewModel = StateObject(wrappedValue: ChatViewModel(apiClient: api))
    }

    var body: some View {
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
