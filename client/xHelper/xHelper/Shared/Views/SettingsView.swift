import SwiftUI

struct SettingsView: View {
    @ObservedObject var deviceManager: DeviceManager
    @EnvironmentObject var colorSchemeManager: ColorSchemeManager
    @AppStorage("xhan_server_url") private var serverURL = DeviceManager.defaultBaseURL
    var onChatWithAgent: ((String) -> Void)?

    private var apiClient: APIClient {
        APIClient(baseURL: deviceManager.baseURL, token: deviceManager.token ?? "")
    }

    var body: some View {
        ZStack {
            GlassBackground()

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
            .listRowBackground(Rectangle().fill(.ultraThinMaterial))

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
            .listRowBackground(Rectangle().fill(.ultraThinMaterial))
            #endif

            Section {
                Picker("外观", selection: $colorSchemeManager.selectedScheme) {
                    ForEach(AppColorScheme.allCases, id: \.self) { scheme in
                        Text(scheme.label).tag(scheme)
                    }
                }
            } header: {
                Text("显示")
            }
            .listRowBackground(Rectangle().fill(.ultraThinMaterial))

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
            .listRowBackground(Rectangle().fill(.ultraThinMaterial))

            Section {
                Button("退出登录", role: .destructive) {
                    Task { await deviceManager.logout() }
                }
                .foregroundStyle(.red)
            }
            .listRowBackground(Rectangle().fill(.ultraThinMaterial))
        }
        .scrollContentBackground(.hidden)
        }
        .tint(Color("appAccent"))
        .navigationTitle("设置")
    }
}
