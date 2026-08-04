import SwiftUI
import AppKit
import WebKit

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
            // Esc：浏览器查找栏打开时关闭（无需 Command 修饰）
            if event.keyCode == 53,
               BrowserFindCoordinator.shared.isPresented,
               BrowserFocus.isFocused() {
                BrowserFindCoordinator.shared.close()
                return nil
            }

            guard event.modifierFlags.contains(.command),
                  !event.isARepeat
            else { return event }

            // Cmd+T / Cmd+Shift+T：焦点无关，只要浏览器有 tab 就生效（对齐 Chrome）。
            // NSApp.modalWindow == nil：模态弹窗（NSAlert.runModal）期间不触发——避免模态 run loop 内改浏览器层级。
            if event.keyCode == 17,
               NSApp.modalWindow == nil,
               BrowserToolProvider.shared.rightSidebar?.hasTabs == true {
                if event.modifierFlags.contains(.shift) {
                    BrowserToolProvider.shared.restoreLastClosedTab()
                } else {
                    BrowserToolProvider.shared.openBlankTab()
                }
                return nil
            }

            // 浏览器聚焦时拦截浏览器快捷键（事件不下发 → 工具栏 keyboardShortcut/默认菜单不触发）
            if BrowserFocus.isFocused() {
                switch event.keyCode {
                case 13: // Cmd+W 关闭当前 tab（替代默认关窗）
                    BrowserToolProvider.shared.closeActiveTab()
                    return nil
                case 33: // Cmd+[ 后退
                    BrowserToolProvider.shared.activeWebView?.goBack()
                    return nil
                case 30: // Cmd+] 前进
                    BrowserToolProvider.shared.activeWebView?.goForward()
                    return nil
                case 15: // Cmd+R 刷新
                    BrowserToolProvider.shared.activeWebView?.reload()
                    return nil
                case 24: // Cmd+= 页面放大
                    Self.zoomWebView(by: 0.1)
                    return nil
                case 27: // Cmd+- 页面缩小
                    Self.zoomWebView(by: -0.1)
                    return nil
                case 3: // Cmd+F 页内查找
                    if let wv = BrowserToolProvider.shared.activeWebView {
                        BrowserFindCoordinator.shared.requestShow(wv)
                    }
                    return nil
                case 5: // Cmd+G / Cmd+Shift+G 查找跳转（栏显示时才拦截）
                    guard BrowserFindCoordinator.shared.isPresented else { break }
                    if event.modifierFlags.contains(.shift) {
                        BrowserFindCoordinator.shared.previous()
                    } else {
                        BrowserFindCoordinator.shared.next()
                    }
                    return nil
                default:
                    break
                }
            } else if Self.isBrowserShortcutKey(event.keyCode) {
                // 诊断：浏览器快捷键因焦点路由被跳过
                let rs = BrowserToolProvider.shared.rightSidebar
                pipLog("shortcut key=\(event.keyCode) skipped: browserNotFocused activeTab=\(rs?.activeTabId ?? "nil") tabs=\(rs?.tabs.count ?? 0) webViewWindow=\(BrowserToolProvider.shared.activeWebView?.window != nil)")
            }

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

    /// 当前要求的最小内容宽（右栏打开时 730，否则 400）。由 ContentView 更新。
    static var requiredMinContentWidth: CGFloat = 400

    /// 浏览器页面缩放（Cmd+±，钳制 0.5x–2.5x）
    private static func zoomWebView(by delta: CGFloat) {
        guard let wv = BrowserToolProvider.shared.activeWebView else { return }
        wv.pageZoom = min(max(wv.pageZoom + delta, 0.5), 2.5)
    }

    /// 浏览器快捷键键码集合（诊断用：命中但被焦点路由跳过时打日志）
    private static func isBrowserShortcutKey(_ keyCode: UInt16) -> Bool {
        switch keyCode {
        case 13, 33, 30, 15, 24, 27, 3, 5:
            return true
        default:
            return false
        }
    }

    /// 拦截窗口缩放：宽度不得低于要求的最小内容宽（+ 窗口 chrome）。
    /// 用 delegate 而不是 minSize——SwiftUI 会在布局时用内容 min 覆盖 minSize。
    func windowWillResize(_ sender: NSWindow, to frameSize: NSSize) -> NSSize {
        let contentWidth = sender.contentView?.frame.width ?? sender.frame.width
        let chrome = sender.frame.width - contentWidth
        let minFrameWidth = Self.requiredMinContentWidth + chrome
        return NSSize(width: max(frameSize.width, minFrameWidth), height: frameSize.height)
    }

    // MARK: 红 X 关闭窗口

    func windowShouldClose(_ sender: NSWindow) -> Bool {
        let resources = MCPManager.shared.activeExternalResources()
        if resources.isEmpty {
            NSApp.terminate(nil)
            return true
        }

        // 非阻塞 sheet 确认（runModal 会卡主线程 → 终止 reply 死锁 → 外部 SIGTERM）
        confirmQuitIfNeeded(on: sender) { confirmed in
            if confirmed {
                Task.detached {
                    await MCPManager.shared.shutdown(closeExternal: true)
                    await MainActor.run { NSApp.terminate(nil) }
                }
            }
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

        // 非阻塞 sheet 确认（runModal 会卡主线程 → MainActor.run reply 死锁 → 外部 SIGTERM）
        confirmQuitIfNeeded(on: NSApp.keyWindow ?? NSApp.mainWindow ?? NSApp.windows.first) { confirmed in
            if confirmed {
                Task.detached {
                    await MCPManager.shared.shutdown(closeExternal: true)
                    await MainActor.run { NSApp.reply(toApplicationShouldTerminate: true) }
                }
            } else {
                NSApp.reply(toApplicationShouldTerminate: false)
            }
        }
        return .terminateLater
    }

    // MARK: 退出确认（非阻塞 sheet）

    /// 退出确认 sheet 是否正在展示（防重入）
    private var isQuitConfirmShowing = false

    /// 退出确认（非阻塞 sheet）：避免 runModal 卡主线程导致终止流程死锁（→ 外部 SIGTERM）。
    /// 已有一张确认 sheet 时不再弹；无窗口时回退 runModal（罕见）。
    private func confirmQuitIfNeeded(on window: NSWindow?, completion: @escaping (Bool) -> Void) {
        guard !isQuitConfirmShowing else { return }
        let resources = MCPManager.shared.activeExternalResources()
        let alert = NSAlert()
        alert.messageText = "退出确认"
        alert.informativeText = "退出时可能导致以下由助手启动的应用随之关闭：\n"
            + resources.map { "• \($0)" }.joined(separator: "\n")
        alert.addButton(withTitle: "确认退出")
        alert.addButton(withTitle: "取消")

        guard let window else {
            DispatchQueue.main.async {
                completion(alert.runModal() == .alertFirstButtonReturn)
            }
            return
        }
        isQuitConfirmShowing = true
        alert.beginSheetModal(for: window) { response in
            self.isQuitConfirmShowing = false
            completion(response == .alertFirstButtonReturn)
        }
    }

    private static func configureWindow(_ window: NSWindow) {
        guard window.isOpaque else { return }
        window.isOpaque = false
        window.backgroundColor = .clear
        window.contentView?.wantsLayer = true
        window.contentView?.layer?.backgroundColor = NSColor.clear.cgColor
        window.toolbar = nil
    }
}

@main
struct xhanHelperMacApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) var appDelegate
    @StateObject private var deviceManager = DeviceManager()
    @StateObject private var colorSchemeManager = ColorSchemeManager()
    @StateObject private var resizeGhost = ResizeGhostState()
    @State private var isHoldingQuit = false
    @State private var holdProgress: Double = 0

    var body: some Scene {
        WindowGroup {
            Group {
                if deviceManager.isLoggedIn {
                    ContentView(deviceManager: deviceManager, resizeGhost: resizeGhost)
                        .frame(minWidth: 400, minHeight: 400)
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
                MainThreadWatchdog.shared.start()
                await LocalToolRegistry.shared.register(BrowserToolProvider.shared)
                ContentBlocker.load()
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
