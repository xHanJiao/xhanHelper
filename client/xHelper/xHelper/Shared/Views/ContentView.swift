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

    private enum LoadState {
        case loading
        case loaded(ChatViewModel)
        case error(String)
    }

    @State private var loadState: LoadState = .loading
    @State private var retryToken: Int = 0

    var body: some View {
        ZStack {
            switch loadState {
            case .loading:
                ProgressView()
            case .loaded(let vm):
                ChatView(viewModel: vm, agentId: nil, baseURL: deviceManager.baseURL, token: deviceManager.token ?? "")
                    .onAppear { isInChat = true }
                    .onDisappear { isInChat = false }
            case .error(let msg):
                VStack(spacing: 12) {
                    Text(msg).foregroundColor(.secondary)
                    Button("重试") {
                        loadState = .loading
                        retryToken += 1
                    }
                }
            }
        }
        .task(id: retryToken) {
            guard let conv = deviceManager.defaultConversation else {
                loadState = .error("无法加载默认对话")
                return
            }
            let api = APIClient(baseURL: deviceManager.baseURL, token: deviceManager.token ?? "")
            let vm = ChatViewModel(apiClient: api)
            loadState = .loaded(vm)
            await vm.loadConversation(id: conv.id, title: conv.title)
        }
    }
}
