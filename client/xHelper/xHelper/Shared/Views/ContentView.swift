import SwiftUI

struct ContentView: View {
    @ObservedObject var deviceManager: DeviceManager

    var body: some View {
        TabView {
            NavigationStack {
                ConversationListContentView(deviceManager: deviceManager)
            }
            .tabItem {
                Label("聊天", systemImage: "bubble.left.and.bubble.right")
            }

            NavigationStack {
                SettingsView(deviceManager: deviceManager)
            }
            .tabItem {
                Label("设置", systemImage: "gear")
            }
        }
    }
}

struct ConversationListContentView: View {
    @ObservedObject var deviceManager: DeviceManager
    @State private var selectedConversationId: String?
    @State private var showNewChat = false
    @State private var chatVM: ChatViewModel?

    private var apiClient: APIClient {
        APIClient(baseURL: deviceManager.baseURL, token: deviceManager.token ?? "")
    }

    var body: some View {
        ZStack {
            if selectedConversationId != nil, let vm = chatVM {
                ChatView(viewModel: vm, baseURL: deviceManager.baseURL, token: deviceManager.token ?? "")
                    .toolbar {
                        ToolbarItem(placement: .navigationBarLeading) {
                            Button("返回") { selectedConversationId = nil }
                        }
                    }
            } else if showNewChat {
                ChatView(
                    viewModel: ChatViewModel(apiClient: apiClient),
                    baseURL: deviceManager.baseURL,
                    token: deviceManager.token ?? ""
                )
                .toolbar {
                    ToolbarItem(placement: .navigationBarLeading) {
                        Button("返回") { showNewChat = false }
                    }
                }
            } else {
                let listVM = ConversationListViewModel(apiClient: apiClient)
                ConversationListView(
                    viewModel: listVM,
                    onSelect: { cid, title in
                        let vm = ChatViewModel(apiClient: apiClient)
                        Task { await vm.loadConversation(id: cid, title: title) }
                        chatVM = vm
                        selectedConversationId = cid
                    },
                    onNewChat: {
                        showNewChat = true
                    }
                )
                .navigationTitle("xhanHelper")
                .onChange(of: listVM.isUnauthorized) {
                    if listVM.isUnauthorized {
                        Task { await deviceManager.logout() }
                    }
                }
            }
        }
    }
}
