import SwiftUI

struct SettingsView: View {
    @ObservedObject var deviceManager: DeviceManager
    @EnvironmentObject var colorSchemeManager: ColorSchemeManager
    @AppStorage(LocalPaths.scoped("xhan_server_url")) private var serverURL = DeviceManager.defaultBaseURL
    @AppStorage(LocalPaths.scoped("xhan_web_url")) private var webURL = DeviceManager.defaultWebBaseURL
    // 近场电平闸：off / loose / standard / near，默认 off（与加闸之前完全一致）
    @AppStorage(VoicePreference.nearFieldKey) private var nearFieldRaw = VoicePreference.nearFieldDefault

    private var apiClient: APIClient {
        APIClient(baseURL: deviceManager.baseURL, token: deviceManager.token ?? "")
    }

    var body: some View {
        ZStack {
            GlassBackground()

            Form {
            #if DEBUG
            Section {
                if deviceManager.isLoggedIn {
                    HStack {
                        Text("用户名")
                        Spacer()
                        Text(deviceManager.username ?? "-").font(.caption)
                    }
                    HStack {
                        Text("用户 ID")
                        Spacer()
                        Text(deviceManager.userId ?? "-").font(.caption).monospaced().lineLimit(1)
                    }
                } else {
                    Text("未登录")
                        .foregroundColor(.secondary)
                }
            } header: {
                Text("账号信息")
            }
            .listRowBackground(Rectangle().fill(.ultraThinMaterial))
            #endif

            #if DEBUG
            Section {
                TextField("服务地址", text: $serverURL)
                    .autocorrectionDisabled()
                #if os(iOS)
                    .keyboardType(.URL)
                #endif
                TextField("网页地址", text: $webURL)
                    .autocorrectionDisabled()
                #if os(iOS)
                    .keyboardType(.URL)
                #endif
            } header: {
                Text("服务器")
            } footer: {
                Text("服务地址为后端 API；网页地址为前端页面（分享链接使用），本地开发两者分端口")
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
                Picker("只响应近处的声音", selection: $nearFieldRaw) {
                    ForEach(NearFieldGate.Sensitivity.allCases) { option in
                        Text(option.label).tag(option.rawValue)
                    }
                }
            } header: {
                Text("语音")
            } footer: {
                Text("关闭＝和以前一样。标准档会忽略远处（隔壁房间）的说话声；被忽略时会有提示。")
            }
            .listRowBackground(Rectangle().fill(.ultraThinMaterial))

            Section {
                NavigationLink(destination: BillListView(
                    viewModel: BillViewModel(apiClient: apiClient),
                    baseURL: apiClient.baseURL,
                    token: apiClient.token
                )) {
                    Label("账单记录", systemImage: "list.bullet.rectangle")
                }
                NavigationLink(destination: DocumentListView(viewModel: DocumentViewModel(apiClient: apiClient))) {
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
