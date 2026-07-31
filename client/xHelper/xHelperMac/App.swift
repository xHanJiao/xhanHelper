import SwiftUI
import AppKit

extension Notification.Name {
    static let holdToQuitChanged = Notification.Name("HoldToQuitChanged")
}

final class AppDelegate: NSObject, NSApplicationDelegate, NSWindowDelegate {
    private(set) var isHoldingQuit = false
    private(set) var holdProgress: Double = 0
    private var quitTimer: Timer?
    private var isQuittingByHold = false

    func applicationDidFinishLaunching(_ notification: Notification) {
        NSEvent.addLocalMonitorForEvents(matching: .keyDown) { [weak self] event in
            guard let self,
                  event.keyCode == 12,
                  event.modifierFlags.intersection(.deviceIndependentFlagsMask) == .command
            else { return event }

            if isHoldingQuit {
                return nil
            }
            startQuitHold()
            return nil
        }
        NSEvent.addLocalMonitorForEvents(matching: .keyUp) { [weak self] event in
            if event.keyCode == 12,
               event.modifierFlags.intersection(.deviceIndependentFlagsMask) == .command {
                self?.cancelQuitHold()
            }
            return event
        }

        NSEvent.addLocalMonitorForEvents(matching: .keyDown) { event in
            guard event.modifierFlags.contains(.command),
                  !event.isARepeat
            else { return event }

            if event.keyCode == 24 {
                FontSizeManager.shared.increase()
                return nil
            }
            if event.keyCode == 27 {
                FontSizeManager.shared.decrease()
                return nil
            }
            return event
        }

        for window in NSApp.windows {
            window.delegate = self
        }
    }

    // MARK: 红 X 关闭窗口

    func windowShouldClose(_ sender: NSWindow) -> Bool {
        let resources = MCPManager.shared.activeExternalResources()
        if resources.isEmpty {
            NSApp.terminate(nil)
            return true
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
                await MainActor.run { NSApp.terminate(nil) }
            }
        default:
            break
        }
        return false
    }

    // MARK: 长按 Cmd+Q 退出

    private func startQuitHold() {
        isHoldingQuit = true
        holdProgress = 0
        postQuitHoldChange()
        let holdDuration: TimeInterval = 0.5
        let interval: TimeInterval = 1.0 / 60.0
        quitTimer = Timer.scheduledTimer(withTimeInterval: interval, repeats: true) { [weak self] timer in
            guard let self else { timer.invalidate(); return }
            self.holdProgress += interval / holdDuration
            if self.holdProgress >= 1.0 {
                timer.invalidate()
                self.quitTimer = nil
                self.holdProgress = 1.0
                self.postQuitHoldChange()
                self.isQuittingByHold = true
                NSApp.terminate(nil)
                DispatchQueue.main.async {
                    self.isHoldingQuit = false
                    self.holdProgress = 0
                    self.postQuitHoldChange()
                }
            } else {
                self.postQuitHoldChange()
            }
        }
    }

    private func cancelQuitHold() {
        guard isHoldingQuit else { return }
        quitTimer?.invalidate()
        quitTimer = nil
        isHoldingQuit = false
        holdProgress = 0
        postQuitHoldChange()
    }

    private func postQuitHoldChange() {
        NotificationCenter.default.post(
            name: .holdToQuitChanged,
            object: nil,
            userInfo: ["isActive": isHoldingQuit, "progress": holdProgress]
        )
    }

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

    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply {
        let resources = MCPManager.shared.activeExternalResources()

        if isQuittingByHold {
            isQuittingByHold = false
            Task.detached {
                await MCPManager.shared.shutdown(closeExternal: true)
                await MainActor.run { NSApp.reply(toApplicationShouldTerminate: true) }
            }
            return .terminateLater
        }

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
        window.isMovableByWindowBackground = true
        window.toolbar = nil
    }
}

@main
struct xhanHelperMacApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) var appDelegate
    @StateObject private var deviceManager = DeviceManager()
    @StateObject private var colorSchemeManager = ColorSchemeManager()
    @State private var isHoldingQuit = false
    @State private var holdProgress: Double = 0

    var body: some Scene {
        WindowGroup {
            Group {
                if deviceManager.isLoggedIn {
                    ContentView(deviceManager: deviceManager)
                        .frame(minWidth: 320, minHeight: 400)
                } else {
                    LoginMacView(deviceManager: deviceManager)
                        .frame(maxWidth: .infinity, maxHeight: .infinity)
                }
            }
            .environmentObject(colorSchemeManager)
            .environmentObject(FontSizeManager.shared)
            .preferredColorScheme(colorSchemeManager.selectedScheme.colorScheme)
            .toast()
            .glassWindow()
            .task {
                await MCPManager.shared.refreshToolCache()
                for ns in await MCPManager.shared.configuredNamespaces() {
                    let wrapper = MCPProviderWrapper(namespace: ns, manager: MCPManager.shared)
                    await LocalToolRegistry.shared.register(wrapper)
                }
                LocalToolRegistry.shared.register(ChartTool())
            }
            .onReceive(NotificationCenter.default.publisher(for: .holdToQuitChanged)) { notification in
                if let active = notification.userInfo?["isActive"] as? Bool {
                    isHoldingQuit = active
                }
                if let progress = notification.userInfo?["progress"] as? Double {
                    holdProgress = progress
                }
            }
            .overlay {
                if isHoldingQuit {
                    holdToQuitOverlay
                }
            }
        }
        .windowResizability(.contentMinSize)
        .defaultSize(width: 800, height: 600)
        .windowStyle(.hiddenTitleBar)

        Settings {
            MacSettingsView(deviceManager: deviceManager)
                .environmentObject(colorSchemeManager)
                .environmentObject(FontSizeManager.shared)
        }
    }

    private var holdToQuitOverlay: some View {
        ZStack {
            Color.black.opacity(0.2)

            VStack(spacing: 20) {
                ZStack {
                    Circle()
                        .stroke(.secondary.opacity(0.15), lineWidth: 5)
                        .frame(width: 56, height: 56)
                    Circle()
                        .trim(from: 0, to: holdProgress)
                        .stroke(Color("appAccent"), style: StrokeStyle(lineWidth: 5, lineCap: .round))
                        .frame(width: 56, height: 56)
                        .rotationEffect(.degrees(-90))
                        .animation(.linear(duration: 0.05), value: holdProgress)
                    Image(systemName: "xmark")
                        .font(.system(size: 16, weight: .bold))
                        .foregroundColor(.secondary)
                }

                Text("长按 Command + Q 退出")
                    .font(.system(size: 16, weight: .medium))
            }
            .padding(32)
            .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 20))
            .shadow(color: .black.opacity(0.15), radius: 20, y: 8)
        }
        .ignoresSafeArea()
        .animation(.easeOut(duration: 0.15), value: isHoldingQuit)
    }
}

// MARK: - Settings

struct MacSettingsView: View {
    @ObservedObject var deviceManager: DeviceManager
    @EnvironmentObject var colorSchemeManager: ColorSchemeManager
    @AppStorage("xhan_server_url") private var serverURL = DeviceManager.defaultBaseURL
    @State private var showClearCacheAlert = false

    private var apiClient: APIClient {
        APIClient(baseURL: deviceManager.baseURL, token: deviceManager.token ?? "")
    }

    var body: some View {
        Form {
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

#if DEBUG
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
                BrowseRow("账单记录", icon: "list.bullet.rectangle", destination: BillListView(
                    viewModel: BillViewModel(apiClient: apiClient),
                    baseURL: deviceManager.baseURL,
                    token: deviceManager.token ?? ""
                ))
                BrowseRow("诊断日志", icon: "exclamationmark.bubble", destination: LogDiagnosticView())
            }

            Section("缓存") {
                Button {
                    showClearCacheAlert = true
                } label: {
                    Label("清除本地缓存", systemImage: "trash")
                }
                .buttonStyle(.borderless)
                .foregroundStyle(.secondary)
            }
            .alert("确认清除", isPresented: $showClearCacheAlert) {
                Button("取消", role: .cancel) {}
                Button("清除", role: .destructive) {
                    Task { await LocalToolResultStore.shared.clearAll() }
                }
            } message: {
                Text("将清除所有本地缓存数据（图表等），下次打开会话时需重新生成。")
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
