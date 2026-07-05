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
            .toolbar(isInChat ? .hidden : .visible, for: .tabBar)

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
        .task {
            let vm = ChatViewModel(apiClient: apiClient)
            await vm.refreshAndCacheLLMConfig()
        }
    }
}

struct ConversationListContentView: View {
    @ObservedObject var deviceManager: DeviceManager
    @Binding var pendingAgentID: String?
    @Binding var isInChat: Bool
    @State private var selectedConversationId: String?
    @State private var showNewChat = false
    @State private var chatVM: ChatViewModel?

    private var apiClient: APIClient {
        APIClient(baseURL: deviceManager.baseURL, token: deviceManager.token ?? "")
    }
    
    private func enterConversation(_ conv: ConversationData) {
        selectedConversationId = conv.id
        let vm = ChatViewModel(apiClient: apiClient)
        Task {await vm.loadConversation(id: conv.id, title: conv.title) }
        chatVM = vm
    }

    var body: some View {
        ZStack {
            if selectedConversationId != nil, let vm = chatVM {
                ChatView(viewModel: vm, agentId: nil, baseURL: deviceManager.baseURL, token: deviceManager.token ?? "")
                    .onAppear { isInChat = true }
                    .onDisappear { isInChat = false }
            } else if showNewChat {
                let vm = ChatViewModel(apiClient: apiClient)
                let aid = pendingAgentID
                ChatView(viewModel: vm, agentId: aid, baseURL: deviceManager.baseURL, token: deviceManager.token ?? "")
                    .toolbar {
                        ToolbarItem(placement: .navigationBarLeading) {
                            Button("返回") {
                                pendingAgentID = nil
                                showNewChat = false
                            }
                        }
                    }
                    .onAppear {
                        isInChat = true
                        if aid != nil {
                            vm.conversationId = nil
                        }
                    }
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
