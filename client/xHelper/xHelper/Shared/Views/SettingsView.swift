import SwiftUI

struct SettingsView: View {
    @ObservedObject var deviceManager: DeviceManager
    @AppStorage("xhan_server_url") private var serverURL = DeviceManager.defaultBaseURL
    var onChatWithAgent: ((String) -> Void)?

    private var apiClient: APIClient {
        APIClient(baseURL: deviceManager.baseURL, token: deviceManager.token ?? "")
    }

    var body: some View {
        Form {
            Section {
                if deviceManager.isLoggedIn {
                    HStack {
                        Text("用户名")
                        Spacer()
                        Text(deviceManager.username ?? "-").font(.caption)
                    }
                    #if DEBUG
                    HStack {
                        Text("用户 ID")
                        Spacer()
                        Text(deviceManager.userId ?? "-").font(.caption).monospaced().lineLimit(1)
                    }
                    #endif
                } else {
                    Text("未登录")
                        .foregroundColor(.secondary)
                }
            } header: {
                Text("账号信息")
            }

            #if DEBUG
            Section {
                TextField("服务地址", text: $serverURL)
                    .autocorrectionDisabled()
                #if os(iOS)
                    .keyboardType(.URL)
                #endif
            } header: {
                Text("服务器")
            }
            #endif

            Section {
                let agentVM = AgentViewModel(apiClient: apiClient)
                NavigationLink(destination: AgentListView(viewModel: agentVM)
                    .onAppear { agentVM.onChatWithAgent = onChatWithAgent }
                ) {
                    Label("智能体", systemImage: "person.badge.shield.checkmark")
                }
                NavigationLink(destination: MemoryListView(viewModel: MemoryViewModel(apiClient: apiClient))) {
                    Label("记忆", systemImage: "brain")
                }
                NavigationLink(destination: PromptListView(viewModel: PromptViewModel(apiClient: apiClient))) {
                    Label("系统提示词", systemImage: "terminal")
                }
                NavigationLink(destination: BillListView(apiClient: apiClient)) {
                    Label("账单记录", systemImage: "list.bullet.rectangle")
                }
                NavigationLink(destination: NoteListView(apiClient: apiClient)) {
                    Label("笔记记录", systemImage: "note.text")
                }
                NavigationLink(destination: DocumentListView(apiClient: apiClient)) {
                    Label("文档", systemImage: "doc.text")
                }
                NavigationLink(destination: LogDiagnosticView()) {
                    Label("诊断日志", systemImage: "exclamationmark.bubble")
                }
            } header: {
                Text("浏览")
            }

            Section {
                Button("退出登录", role: .destructive) {
                    Task { await deviceManager.logout() }
                }
            }
        }
        .navigationTitle("设置")
    }
}
