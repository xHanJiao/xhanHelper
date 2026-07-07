import SwiftUI

struct ContentView: View {
    @ObservedObject var deviceManager: DeviceManager
    @State private var pendingAgentID: String?
    @State private var selectedTab = "chat"
    @State private var isInChat = false

    private var apiClient: APIClient {
        APIClient(baseURL: deviceManager.baseURL, token: deviceManager.token ?? "")
    }

    var body: some View {
        TabView(selection: $selectedTab) {
            NavigationStack {
                ConversationListContentView(deviceManager: deviceManager, pendingAgentID: $pendingAgentID, isInChat: $isInChat)
            }
            .tabItem {
                Label("聊天", systemImage: "bubble.left.and.bubble.right")
            }
            .tag("chat")

            NavigationStack {
                SettingsView(deviceManager: deviceManager, onChatWithAgent: { agentID in
                    pendingAgentID = agentID
                    selectedTab = "chat"
                })
            }
            .tabItem {
                Label("设置", systemImage: "gear")
            }
            .tag("settings")
        }
    }
}

struct ConversationListContentView: View {
    @ObservedObject var deviceManager: DeviceManager
    @Binding var pendingAgentID: String?
    @Binding var isInChat: Bool
    @State private var chatVM: ChatViewModel?

    private var apiClient: APIClient {
        APIClient(baseURL: deviceManager.baseURL, token: deviceManager.token ?? "")
    }
    
    private func enterConversation(_ conv: ConversationData) {
        let vm = ChatViewModel(apiClient: apiClient)
        Task {await vm.loadConversation(id: conv.id, title: conv.title) }
        chatVM = vm
    }

    var body: some View {
        ZStack {
            if let vm = chatVM {
                ChatView(viewModel: vm, agentId: nil, baseURL: deviceManager.baseURL, token: deviceManager.token ?? "")
                    .onAppear { isInChat = true }
                    .onDisappear { isInChat = false }
            }
        }.onChange(of: deviceManager.defaultConversation?.id) { _, _ in
            guard let conv = deviceManager.defaultConversation else { return }
            enterConversation(conv)
        }.onAppear {
            if let conv = deviceManager.defaultConversation {
                enterConversation(conv)
            }
        }
    }
}
