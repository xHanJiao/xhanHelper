import SwiftUI
import AppKit

final class AppDelegate: NSObject, NSApplicationDelegate {
    func applicationWillBecomeActive(_ notification: Notification) {
        for window in NSApp.windows {
            Self.configureWindow(window)
        }
    }

    func applicationDidBecomeActive(_ notification: Notification) {
        for window in NSApp.windows {
            Self.configureWindow(window)
        }
    }

    // 退出确认：助手启动的外部程序（浏览器等）仍在运行时，询问是否一并关闭
    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply {
        let resources = MCPManager.shared.activeExternalResources()

        if resources.isEmpty {
            Task.detached {
                await MCPManager.shared.shutdown(closeExternal: false)
                await MainActor.run { NSApp.reply(toApplicationShouldTerminate: true) }
            }
            return .terminateLater
        }

        let alert = NSAlert()
        alert.messageText = "退出确认"
        alert.informativeText = "退出时可能导致以下由助手启动的应用随之关闭：\n"
            + resources.map { "• \($0)" }.joined(separator: "\n")
        alert.addButton(withTitle: "确认退出")
        alert.addButton(withTitle: "取消")

        switch alert.runModal() {
        case .alertFirstButtonReturn:
            Task.detached {
                await MCPManager.shared.shutdown(closeExternal: true)
                await MainActor.run { NSApp.reply(toApplicationShouldTerminate: true) }
            }
            return .terminateLater
        default:
            return .terminateCancel
        }
    }

    private static func configureWindow(_ window: NSWindow) {
        guard window.isOpaque else { return }
        window.isOpaque = false
        window.backgroundColor = .clear
        window.contentView?.wantsLayer = true
        window.contentView?.layer?.backgroundColor = NSColor.clear.cgColor
        window.titlebarAppearsTransparent = true
        window.styleMask.insert(.fullSizeContentView)
    }
}

@main
struct xhanHelperMacApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) var appDelegate
    @StateObject private var deviceManager = DeviceManager()
    @StateObject private var colorSchemeManager = ColorSchemeManager()

    var body: some Scene {
        WindowGroup {
            Group {
                if deviceManager.isLoggedIn {
                    ContentView(deviceManager: deviceManager)
                        .frame(minWidth: 750, minHeight: 500)
                } else {
                    LoginMacView(deviceManager: deviceManager)
                        .frame(maxWidth: .infinity, maxHeight: .infinity)
                }
            }
            .environmentObject(colorSchemeManager)
            .preferredColorScheme(colorSchemeManager.selectedScheme.colorScheme)
            .toast()
            .glassWindow()
            .task {
                await MCPManager.shared.refreshToolCache()
                for ns in await MCPManager.shared.configuredNamespaces() {
                    let wrapper = MCPProviderWrapper(namespace: ns, manager: MCPManager.shared)
                    await LocalToolRegistry.shared.register(wrapper)
                }
                await LocalToolRegistry.shared.register(ChartTool())
            }
        }
        .windowResizability(.contentMinSize)
        .defaultSize(width: 800, height: 600)

        Settings {
            MacSettingsView(deviceManager: deviceManager)
                .environmentObject(colorSchemeManager)
        }
    }
}

// MARK: - Settings

struct MacSettingsView: View {
    @ObservedObject var deviceManager: DeviceManager
    @EnvironmentObject var colorSchemeManager: ColorSchemeManager
    @AppStorage("xhan_server_url") private var serverURL = DeviceManager.defaultBaseURL

    private var apiClient: APIClient {
        APIClient(baseURL: deviceManager.baseURL, token: deviceManager.token ?? "")
    }

    var body: some View {
        Form {
#if DEBUG
            Section("外观") {
                Picker("", selection: Binding(
                    get: { colorSchemeManager.selectedScheme },
                    set: { newValue in
                        DispatchQueue.main.async {
                            colorSchemeManager.selectedScheme = newValue
                        }
                    }
                )) {
                    ForEach(AppColorScheme.allCases, id: \.self) { scheme in
                        Text(scheme.label).tag(scheme)
                    }
                }
                .pickerStyle(.segmented)
                .labelsHidden()
            }

            Section("账号") {
                LabeledContent("用户名", value: deviceManager.username ?? "-")
                LabeledContent("用户 ID", value: deviceManager.userId ?? "-")
                    .fontDesign(.monospaced)
            }

            Section("服务器") {
                TextField("服务地址", text: $serverURL)
                    .textFieldStyle(.roundedBorder)
            }
#endif

            Section("浏览") {
                BrowseRow("账单记录", icon: "list.bullet.rectangle", destination: BillListView(apiClient: apiClient))
                BrowseRow("诊断日志", icon: "exclamationmark.bubble", destination: LogDiagnosticView())
            }

            Section {
                Button("退出登录") {
                    Task { await deviceManager.logout() }
                }
                .buttonStyle(.borderless)
                .foregroundStyle(.red)
            }
        }
        .formStyle(.grouped)
        .tint(Color("appAccent"))
        .fixedSize(horizontal: true, vertical: false)
        .frame(minWidth: 380)
    }
}

// MARK: - Browse Row

struct BrowseRow<Destination: View>: View {
    let title: String
    let icon: String
    let destination: Destination
    @State private var showDetail = false

    init(_ title: String, icon: String, destination: Destination) {
        self.title = title
        self.icon = icon
        self.destination = destination
    }

    var body: some View {
        Button {
            showDetail = true
        } label: {
            Label(title, systemImage: icon)
                .frame(maxWidth: .infinity, alignment: .leading)
        }
        .buttonStyle(.borderless)
        .sheet(isPresented: $showDetail) {
            destination
                .toolbar {
                    ToolbarItem(placement: .cancellationAction) {
                        Button("关闭") { showDetail = false }
                    }
                }
                .frame(minWidth: 500, minHeight: 400)
        }
    }
}

// MARK: - Login

struct LoginMacView: View {
    @ObservedObject var deviceManager: DeviceManager
    @State private var username = ""
    @State private var password = ""
    @State private var isRegisterMode = false
    @State private var isLoading = false

    var body: some View {
        VStack(spacing: 24) {
            Spacer()

            Image(systemName: "bubble.left.and.bubble.right.fill")
                .font(.system(size: 56))
                .foregroundColor(Color("appAccent"))
                .padding(.bottom, 4)

            Text("xhanHelper")
                .font(.system(size: 28, weight: .bold))

            VStack(spacing: 12) {
                TextField("用户名", text: $username)
                    .textFieldStyle(.roundedBorder)
                    .frame(maxWidth: 260)

                SecureField("密码", text: $password)
                    .textFieldStyle(.roundedBorder)
                    .frame(maxWidth: 260)
            }
            .onSubmit { Task { await performAuth() } }

            if isLoading {
                ProgressView()
                    .scaleEffect(0.8)
            } else {
                Button(isRegisterMode ? "注册" : "登录") {
                    Task { await performAuth() }
                }
                .buttonStyle(.borderedProminent)
                .controlSize(.large)
                .disabled(username.isEmpty || password.isEmpty)
                .frame(maxWidth: 260)

                Button(isRegisterMode ? "已有账号？登录" : "没有账号？注册") {
                    isRegisterMode.toggle()
                }
                .font(.caption)
                .buttonStyle(.link)
            }

            Spacer()
        }
    }

    private func performAuth() async {
        isLoading = true
        do {
            if isRegisterMode {
                try await deviceManager.register(username: username, password: password)
            } else {
                try await deviceManager.login(username: username, password: password)
            }
        } catch {
            ToastCenter.shared.show(ToastCenter.friendlyErrorText(error, action: isRegisterMode ? "注册" : "登录"))
        }
        isLoading = false
    }
}
