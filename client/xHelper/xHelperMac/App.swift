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
        // dev 视觉标识（UI-7）：Dock 图标右下角打一个 dev 角标（两个 .app 图标否则一模一样）。
        applyDevIconBadgeIfNeeded()

        // 本地模式：先把内嵌后端拉起来（窗口秒开，后端在后台初始化）。
        // 未配密钥时不起后端——先走首次配置（UI-1），配置完成后再 start。
        if LocalBackendManager.isActive && LocalBackendManager.shared.isConfigured {
            LocalBackendManager.shared.start()
        }

        // 任意交互刷新空闲计时（Touch ID 空闲锁，UI-10）。
        NSEvent.addLocalMonitorForEvents(
            matching: [.keyDown, .leftMouseDown, .rightMouseDown, .scrollWheel]
        ) { event in
            LocalLockController.shared.noteActivity()
            return event
        }

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
            // 浮层（diff 放大预览 / 大图）打开时，Esc 先收起浮层，不落到下面的「Esc=拒绝」
            if event.keyCode == 53, LightboxCenter.shared.content != nil {
                LightboxCenter.shared.dismiss()
                return nil
            }

            // L3 工具确认：Tab=允许，Esc=拒绝（无需 Command，优先级最高）。
            // 作用于队列队首（多条并发到时逐条处理），移除后自动轮到下一条，连按安全。
            if AppShortcutBridge.shared.confirmationPending?() == true {
                if event.keyCode == 48 { // Tab
                    AppShortcutBridge.shared.resolveConfirmation?(true)
                    return nil
                }
                if event.keyCode == 53 { // Esc
                    AppShortcutBridge.shared.resolveConfirmation?(false)
                    return nil
                }
            }

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

            // Cmd+Shift+B：切换右侧栏（焦点无关，无模态时生效，对齐 VS Code）
            if event.keyCode == 11,
               event.modifierFlags.contains(.shift),
               NSApp.modalWindow == nil {
                BrowserToolProvider.shared.rightSidebar?.toggle()
                return nil
            }

            // Cmd+Shift+M：最大化/还原侧栏内容（焦点无关，无模态时生效；侧栏未开时静默不生效）。
            // M 与系统 Cmd+M（最小化窗口）语义对仗，方便记忆。
            if event.keyCode == 46,
               event.modifierFlags.contains(.shift),
               NSApp.modalWindow == nil,
               BrowserToolProvider.shared.rightSidebar?.isOpen == true {
                BrowserToolProvider.shared.rightSidebar?.isPipChat.toggle()
                return nil
            }

            // Cmd+Shift+E：展开/收起 PiP 聊天悬浮球（仅 PiP 生效；未进 PiP 时静默不生效）。
            if event.keyCode == 14,                                    // 14 = E
               event.modifierFlags.contains(.shift),
               NSApp.modalWindow == nil,
               BrowserToolProvider.shared.rightSidebar?.isPipChat == true {
                BrowserToolProvider.shared.rightSidebar?.pipCollapsed.toggle()
                return nil
            }

            // Cmd+N：新建对话（焦点无关，无模态时生效）
            if event.keyCode == 45,
               !event.modifierFlags.contains(.shift),
               NSApp.modalWindow == nil {
                AppShortcutBridge.shared.onNewChat?()
                return nil
            }

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

            // Cmd+Shift+[ / Cmd+Shift+]：切换上一个/下一个 tab（焦点无关，只要浏览器有 tab）
            if (event.keyCode == 33 || event.keyCode == 30),
               event.modifierFlags.contains(.shift),
               NSApp.modalWindow == nil,
               BrowserToolProvider.shared.rightSidebar?.hasTabs == true {
                BrowserToolProvider.shared.rightSidebar?.switchTab(delta: event.keyCode == 33 ? -1 : 1)
                return nil
            }

            // Cmd+Shift+↑ / Cmd+Shift+↓：会话列表上/下切换（焦点无关，无模态时生效；
            // 已通过上方 guard 的 !isARepeat 拦截按住重复）
            if event.keyCode == 126 || event.keyCode == 125,
               event.modifierFlags.contains(.shift),
               NSApp.modalWindow == nil {
                AppShortcutBridge.shared.onSwitchConversation?(event.keyCode == 126 ? -1 : 1)
                return nil
            }

            // Cmd+W：全局拦截，禁用默认"关闭窗口"菜单快捷键（防止误触退出）。
            // 浏览器聚焦时对齐 Chrome 关闭当前 tab；否则忽略，不做任何事。
            if event.keyCode == 13 {
                if BrowserFocus.isFocused() {
                    BrowserToolProvider.shared.closeActiveTab()
                }
                return nil
            }

            // Cmd+V：聊天输入框聚焦且剪贴板里有图时接管（图进待发附件，不落成文本）。
            // 其余情况一律放行给 TextEditor —— 普通粘字、PiP 小窗粘贴都走原路。
            if event.keyCode == 9,
               event.modifierFlags.intersection(.deviceIndependentFlagsMask) == .command,
               ChatInputFocus.shared.isFocused,
               NSApp.modalWindow == nil,
               let datas = Self.pasteboardImages() {
                AppShortcutBridge.shared.onPasteImages?(datas)
                return nil
            }

            // 浏览器聚焦时拦截浏览器快捷键（事件不下发 → 工具栏 keyboardShortcut/默认菜单不触发）
            if BrowserFocus.isFocused() {
                switch event.keyCode {
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
                case 2: // Cmd+D 收藏/取消收藏当前页（对齐 Chrome）
                    Self.toggleBookmarkForActivePage()
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

    /// 当前要求的最小内容宽（右栏打开时 ≈778，否则 750；再取顶栏内在宽的较大者）。由 ContentView 更新。
    static var requiredMinContentWidth: CGFloat = 400

    /// 浏览器页面缩放（Cmd+±，钳制 0.5x–2.5x）
    private static func zoomWebView(by delta: CGFloat) {
        guard let wv = BrowserToolProvider.shared.activeWebView else { return }
        wv.pageZoom = min(max(wv.pageZoom + delta, 0.5), 2.5)
    }

    /// 收藏/取消收藏当前页（Cmd+D，与地址栏星标按钮逻辑一致）
    private static func toggleBookmarkForActivePage() {
        guard let wv = BrowserToolProvider.shared.activeWebView,
              let url = wv.url,
              let scheme = url.scheme?.lowercased(),
              scheme == "http" || scheme == "https" else { return }
        BookmarkStore.shared.toggle(title: wv.title ?? url.host ?? "", url: url.absoluteString)
    }

    /// 读剪贴板里的图片字节：Finder 复制的图片文件 → 位图（截图/网页复制）→ 都没有则 nil。
    ///
    /// 剪贴板同时带普通文本时不接管（从网页复制图片会带上链接/文字）：让用户拿到文本，
    /// 符合系统预期，也比默默替他决定"要图不要字"好。缩略/重编码由 ChatView 统一做。
    private static func pasteboardImages() -> [Data]? {
        let pb = NSPasteboard.general

        // Finder 复制的图片文件：剪贴板带的是文件路径文本，那不是"要粘的文字"
        if let urls = pb.readObjects(forClasses: [NSURL.self], options: nil) as? [URL] {
            let datas = urls
                .filter { $0.isFileURL && imageExtensions.contains($0.pathExtension.lowercased()) }
                .compactMap { try? Data(contentsOf: $0) }
            if !datas.isEmpty { return datas }
        }

        if let text = pb.string(forType: .string)?.trimmingCharacters(in: .whitespacesAndNewlines),
           !text.isEmpty,
           !text.hasPrefix("/"),
           !text.hasPrefix("file://") {
            return nil
        }

        // 截图给的是 TIFF；网页复制的图常带 PNG。TIFF 由 ImageCompressor 转成 JPEG/PNG
        if let png = pb.data(forType: .png) { return [png] }
        if let tiff = pb.data(forType: .tiff) { return [tiff] }
        return nil
    }

    /// 剪贴板/文件选择允许的图片后缀（HEIC/TIFF/BMP 会在 ChatView 里换格式，服务端只认 png/jpg/gif/webp）
    private static let imageExtensions: Set<String> = [
        "png", "jpg", "jpeg", "gif", "webp", "heic", "heif", "tiff", "tif", "bmp",
    ]

    /// 浏览器快捷键键码集合（诊断用：命中但被焦点路由跳过时打日志）
    private static func isBrowserShortcutKey(_ keyCode: UInt16) -> Bool {
        switch keyCode {
        case 33, 30, 15, 24, 27, 3, 5, 2:
            return true
        default:
            return false
        }
    }

    /// 拦截窗口缩放：宽度不得低于要求的最小内容宽（+ 窗口 chrome）。
    /// 用 delegate 而不是 minSize——SwiftUI 会在布局时用内容 min 覆盖 minSize。
    /// `requiredMinContentWidth` 已由 ContentView 取「底栏推导值 / 顶栏内在宽」的较大者：
    /// 顶栏是 VStack 最宽子项，漏算会让窗口缩到比顶栏还窄 → 顶栏溢出居中、红绿灯压住下拉框。
    func windowWillResize(_ sender: NSWindow, to frameSize: NSSize) -> NSSize {
        let contentWidth = sender.contentView?.frame.width ?? sender.frame.width
        let chrome = sender.frame.width - contentWidth
        let minFrameWidth = Self.requiredMinContentWidth + chrome
        return NSSize(width: max(frameSize.width, minFrameWidth), height: frameSize.height)
    }

    // MARK: 红 X 关闭窗口

    func windowShouldClose(_ sender: NSWindow) -> Bool {
        // 驻留（改动点 9 / UI-9）：红 X 只关窗，app 与本地后端继续在后台跑；
        // Cmd+Q（长按 / 右键退出）才真正终止。首次关窗给小浮窗提示。
        ResidentHint.showIfNeeded()
        return true
    }

    // 关窗后点 Dock 图标回来：交给 SwiftUI 自己处理（`WindowGroup` 的默认行为）。
    // 别在这里手动 `makeKeyAndOrderFront`——那是多余的第二只手，会和 SwiftUI 各拉一个窗口
    // （旧窗口有时已被释放、有时还活着，所以表现为「偶尔出现两个主窗口」）。
    // 以前 front 所有窗口还有个副作用：连 Settings 窗口一起弹出来。

    /// 驻留：关掉最后一个窗口也不退出（后台任务继续跑）。
    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool {
        false
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
        LocalLockController.shared.lockOnActivate()
    }

    func applicationDidResignActive(_ notification: Notification) {
        LocalLockController.shared.noteResignActive()
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
        confirmQuitIfNeeded { confirmed in
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

    func applicationWillTerminate(_ notification: Notification) {
        // 同步停内嵌后端（最多等 2 秒，超时 SIGKILL），保证不留孤儿进程占端口。
        LocalBackendManager.shared.stopBlocking()
    }

    // MARK: 退出确认（非阻塞 sheet）

    /// 退出确认 sheet 是否正在展示（防重入）
    private var isQuitConfirmShowing = false

    /// 退出确认（非阻塞 sheet）：避免 runModal 卡主线程导致终止流程死锁（→ 外部 SIGTERM）。
    /// 已有一张确认 sheet 时不再弹。
    ///
    /// 驻留态（关窗后 Cmd+Q）可能一个可见窗口都没有：此时**把主窗口叫回来**当宿主再弹 sheet，
    /// 而不是静默放行——否则助手拉起的外部 app 会被不打招呼地关掉。
    private func confirmQuitIfNeeded(completion: @escaping (Bool) -> Void) {
        guard !isQuitConfirmShowing else { return }
        let host = quitConfirmHost()
        guard let host else {
            LogBuffer.shared.warning("退出确认：没有可挂 sheet 的窗口，直接放行")
            completion(true)
            return
        }
        if !host.isVisible {
            host.makeKeyAndOrderFront(nil)
        }
        let resources = MCPManager.shared.activeExternalResources()
        let alert = NSAlert()
        alert.messageText = "退出确认"
        alert.informativeText = "退出时可能导致以下由助手启动的应用随之关闭：\n"
            + resources.map { "• \($0)" }.joined(separator: "\n")
        alert.addButton(withTitle: "确认退出")
        alert.addButton(withTitle: "取消")

        isQuitConfirmShowing = true
        alert.beginSheetModal(for: host) { response in
            self.isQuitConfirmShowing = false
            completion(response == .alertFirstButtonReturn)
        }
    }

    /// 选一个能挂 sheet 的宿主窗口（排除浮动面板；都没有则回退第一个普通窗口
    /// ——它可能只是被「关窗驻留」关掉了，叫回来即可）。
    private func quitConfirmHost() -> NSWindow? {
        if let key = NSApp.keyWindow, !(key is NSPanel) { return key }
        if let main = NSApp.mainWindow, !(main is NSPanel) { return main }
        let normal = NSApp.windows.filter { !($0 is NSPanel) && $0.canBecomeKey }
        return normal.first(where: { $0.isVisible }) ?? normal.first
    }

    /// dev 构建：给 Dock 图标右下角打一个 `dev` 角标。两个 .app 的图标资源是同一份，
    /// 不加角标在 Dock / ⌘Tab 里根本分不出谁是谁（窗口内另有顶栏 DEV 徽标）。
    private func applyDevIconBadgeIfNeeded() {
        guard LocalPaths.isDev, let base = NSApp.applicationIconImage else { return }
        let size = base.size
        let label = "dev" as NSString
        let font = NSFont.systemFont(ofSize: size.height * 0.115, weight: .bold)
        let attrs: [NSAttributedString.Key: Any] = [.font: font, .foregroundColor: NSColor.white]
        let textSize = label.size(withAttributes: attrs)
        let padX = textSize.height * 0.5
        let badgeSize = CGSize(width: textSize.width + padX * 2, height: textSize.height * 1.45)
        // 不贴死右下角：macOS 26 会给全出血图标套圆角遮罩，太靠边会被啃掉一角。
        let badgeRect = NSRect(
            x: size.width * 0.965 - badgeSize.width,
            y: size.height * 0.035,
            width: badgeSize.width,
            height: badgeSize.height
        )

        let badged = NSImage(size: size, flipped: false) { _ in
            base.draw(in: NSRect(origin: .zero, size: size))
            // 黑白角标（不引色相）：蓝底图标上无论深浅模式都读得清。
            NSColor.black.withAlphaComponent(0.75).setFill()
            NSBezierPath(roundedRect: badgeRect, xRadius: badgeRect.height / 2, yRadius: badgeRect.height / 2).fill()
            label.draw(
                at: CGPoint(x: badgeRect.minX + padX, y: badgeRect.minY + (badgeRect.height - textSize.height) / 2),
                withAttributes: attrs
            )
            return true
        }
        NSApp.applicationIconImage = badged
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
    init() {
        NotificationDelegateRegistrar.register()
    }

    @NSApplicationDelegateAdaptor(AppDelegate.self) var appDelegate
    @StateObject private var deviceManager = DeviceManager()
    @ObservedObject private var localBackend = LocalBackendManager.shared
    @ObservedObject private var lockController = LocalLockController.shared
    @StateObject private var colorSchemeManager = ColorSchemeManager()
    @StateObject private var resizeGhost = ResizeGhostState()
    @StateObject private var windowHandle = LocalWindowHandle()
    @State private var isHoldingQuit = false
    @State private var holdProgress: Double = 0
    /// 用户首次配置窗口点了「稍后配置」→ 主窗口显示未配置空态（UI-4）。
    @State private var onboardingSkipped = false
    /// 首次配置页内容实测高度（`LocalOnboardingView.onContentHeight`）：窗口高度贴内容，无滚动条。
    @State private var onboardingContentHeight: CGFloat = 650
    /// 首次配置窗口是否已居中过（之后内容变化只改高度、顶部锚定，不再 center 免得窗口跳）。
    @State private var onboardingWindowCentered = false
    /// 是否已把窗口套用「主窗口默认尺寸」（一次性；老版本遗留的过小窗口借此放大到新默认）。
    @AppStorage("xhan_main_window_default_applied") private var windowDefaultSizeApplied = false

    var body: some Scene {
        WindowGroup {
            Group {
                localBackendRoot
            }
            .environmentObject(colorSchemeManager)
            .environmentObject(FontSizeManager.shared)
            .preferredColorScheme(colorSchemeManager.selectedScheme.colorScheme)
            .toast()
            .lightbox()
            .browserDiagnostics()
            .glassWindow()
            .background(LocalWindowReader { window in
                windowHandle.window = window
                applyWindowSizeForOnboarding()
            })
            .onAppear { applyWindowSizeForOnboarding() }
            .onChange(of: onboardingGateVisible) { _, _ in applyWindowSizeForOnboarding() }
            .onChange(of: onboardingContentHeight) { _, _ in applyWindowSizeForOnboarding() }
            .onChange(of: localBackend.isConfigured) { _, configured in
                // 密钥被清掉（设置页「清除密钥」）→ 回到首次配置。
                if !configured { revealOnboarding() }
            }
            .task {
                // 后端可能在窗口渲染前就已 ready（空库秒起），补一次；否则交给 onChange。
                if localBackend.state == .ready {
                    LocalLockController.shared.start()
                    await deviceManager.onLocalBackendReady()
                }
                await LocalToolRegistry.shared.register(BrowserToolProvider.shared)
                ContentBlocker.load()
                await MCPManager.shared.refreshToolCache()
                for ns in await MCPManager.shared.configuredNamespaces() {
                    let wrapper = MCPProviderWrapper(namespace: ns, manager: MCPManager.shared)
                    await LocalToolRegistry.shared.register(wrapper)
                }
                LocalToolRegistry.shared.register(ChartTool())
                LocalToolRegistry.shared.register(FileSystemToolProvider())
                LocalToolRegistry.shared.register(ShellToolProvider(scope: .full))
                LocalToolRegistry.shared.register(ShellToolProvider(scope: .peek))
                AgentWorkspace.ensureExists()
                ToolOutputStore.shared.startHousekeeping()
            }
            .onChange(of: localBackend.state) { _, newState in
                if newState == .ready {
                    LocalLockController.shared.start()
                    Task { await deviceManager.onLocalBackendReady() }
                }
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
            .overlay {
                // UI-10：运行期重新锁定（回前台 / 空闲 15 分钟）时罩住主窗口。
                if LocalBackendManager.isActive && lockController.isLocked {
                    LocalUnlockView {
                        Task { await LocalLockController.shared.unlock() }
                    }
                    .transition(.opacity)
                }
            }
        }
        .windowResizability(.contentMinSize)
        .defaultSize(Self.chatDefaultContentSize)
        .windowStyle(.hiddenTitleBar)

        Settings {
            MacSettingsView(deviceManager: deviceManager)
                .environmentObject(colorSchemeManager)
                .environmentObject(FontSizeManager.shared)
                .preferredColorScheme(colorSchemeManager.selectedScheme.colorScheme)
                .overlay {
                    // 设置窗口是独立 Scene：不盖的话锁定期间照样能看明文密钥 / 点「浏览」。
                    if LocalBackendManager.isActive && lockController.isLocked {
                        LocalUnlockView {
                            Task { await LocalLockController.shared.unlock() }
                        }
                    }
                }
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

    // MARK: - 本地后端启动门（UI-2 / UI-3）

    @ViewBuilder
    private var localBackendRoot: some View {
        if !LocalBackendManager.isActive {
            // 逃生口：设置里显式填了「服务地址」，走原来的登录 / 云端寻址。
            accountRoot
        } else if !localBackend.isConfigured && !onboardingSkipped {
            // UI-1：首次配置（无密钥 → 不起后端，先填密钥）。
            LocalOnboardingView(
                onDone: { startLocalAfterConfig() },
                onSkip: { onboardingSkipped = true },
                onContentHeight: { onboardingContentHeight = $0 }
            )
        } else if !localBackend.isConfigured {
            // UI-4：跳过配置后的未配置空态。
            LocalNotConfiguredView(onConfigure: { onboardingSkipped = false })
        } else {
            switch localBackend.state {
            case .locked:
                // UI-10：Touch ID 解锁门（token 读取需要生物认证）。
                LocalUnlockView { localBackend.unlock() }
            case .ready:
                if deviceManager.isLoggedIn {
                    ContentView(deviceManager: deviceManager, resizeGhost: resizeGhost)
                        .frame(minWidth: 400, minHeight: 400)
                } else if let err = deviceManager.localBootstrapError {
                    // 后端起来了但载入会话失败：给错误态 + 重试，别让 UI 永久停在 spinner。
                    localBackendFailed(err) { Task { await deviceManager.onLocalBackendReady() } }
                } else {
                    localBackendStarting
                }
            case .idle, .starting:
                localBackendStarting
            case .failed(let reason):
                localBackendFailed(reason) { Task { await localBackend.restart() } }
            }
        }
    }

    /// UI-1 配置完成 → 起本地后端。
    private func startLocalAfterConfig() {
        LocalBackendManager.shared.start()
    }

    /// 是否正处于「首次配置」门（UI-1）。
    private var onboardingGateVisible: Bool {
        LocalBackendManager.isActive && !localBackend.isConfigured && !onboardingSkipped
    }

    /// 回到首次配置（UI-1）：清掉密钥后不能还杵在设置页里。
    /// 关掉设置等普通窗口（`canBecomeKey` 排除导出 PDF 的离屏窗口、`NSPanel` 排除驻留提示），把主窗口叫到前面。
    private func revealOnboarding() {
        onboardingSkipped = false
        let main = windowHandle.window
        for window in NSApp.windows
        where !(window is NSPanel) && window.canBecomeKey && window !== main {
            window.close()
        }
        main?.makeKeyAndOrderFront(nil)
    }

    /// 主窗口默认内容尺寸（全新安装 / 首次进主界面用；也用于把老版本遗留的过小窗口一次性放大）。
    private static let chatDefaultContentSize = CGSize(width: 1180, height: 800)

    /// 按当前屏幕可用区收一下，小外接屏不溢出（左右/上下各留 20）。
    private static func fittedChatDefaultSize(for window: NSWindow) -> CGSize {
        let visible = window.screen?.visibleFrame ?? NSScreen.main?.visibleFrame
            ?? NSRect(x: 0, y: 0, width: 1440, height: 900)
        return CGSize(width: min(chatDefaultContentSize.width, visible.width - 40),
                      height: min(chatDefaultContentSize.height, visible.height - 40))
    }

    /// UI-1 把窗口贴到内容：高度用 `LocalOnboardingView` 实测值（本页不滚动，故不预留死区），
    /// 宽度 = 内容 540 + 左右 24 内边距。内容变化时顶部锚定，避免展开/收起「高级」时窗口上下跳；
    /// 配置完成后恢复聊天默认尺寸。
    private func applyWindowSizeForOnboarding() {
        guard let window = windowHandle.window else { return }
        if onboardingGateVisible {
            let maxHeight = (window.screen?.visibleFrame.height ?? 900) - 24
            let height = min(max(onboardingContentHeight, 400), maxHeight)
            // hiddenTitleBar 下 setContentSize 设的是「内容矩形」，窗口 frame 会再加一个标题栏高；
            // 内容自己用负的底部留白（见 LocalOnboardingView）抵掉那个死区，这里按实测高设即可。
            let top = window.frame.maxY
            window.setContentSize(NSSize(width: 588, height: height))
            // setContentSize 以左下角为锚；改完把顶边放回原处，内容变化时窗口不上下跳。
            window.setFrameOrigin(NSPoint(x: window.frame.minX, y: top - window.frame.height))
            if !onboardingWindowCentered {
                onboardingWindowCentered = true
                window.center()
            }
        } else {
            onboardingWindowCentered = false
            let width = window.contentView?.frame.width ?? 0
            // <700：刚离开首次配置页（内容宽 588）→ 恢复聊天默认尺寸
            // !flag：老版本遗留的过小窗口（如 900×450）→ 一次性放大，之后尊重手动调整
            if width < 700 || !windowDefaultSizeApplied {
                windowDefaultSizeApplied = true
                window.setContentSize(Self.fittedChatDefaultSize(for: window))
                window.center()
            }
        }
    }

    @ViewBuilder
    private var accountRoot: some View {
        if deviceManager.isLoggedIn {
            ContentView(deviceManager: deviceManager, resizeGhost: resizeGhost)
                .frame(minWidth: 400, minHeight: 400)
        } else {
            LoginMacView(deviceManager: deviceManager)
                .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
    }

    private var localBackendStarting: some View {
        VStack(spacing: 14) {
            ProgressView()
            Text("正在启动本地服务…")
                .foregroundColor(.secondary)
            Text("首次启动需初始化数据库，稍等片刻")
                .font(.caption)
                .foregroundColor(.secondary)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }

    private func localBackendFailed(_ reason: String, retry: @escaping () -> Void) -> some View {
        VStack(spacing: 16) {
            Image(systemName: "exclamationmark.triangle")
                .font(.system(size: 36))
                .foregroundColor(.secondary)
            Text(reason)
                .font(.body)
                .foregroundColor(.secondary)
                .multilineTextAlignment(.center)
                .textSelection(.enabled)
                .frame(maxWidth: 560)
            HStack(spacing: 12) {
                Button("查看日志") { NSWorkspace.shared.open(localBackend.logFileURL) }
                Button("重试", action: retry)
                    .keyboardShortcut(.defaultAction)
            }
            // Touch ID 凭据读不出来时，「重试」只会再撞同一堵墙——给一个真正的出口。
            if LocalAuthService.shared.isActive {
                LocalTouchIDRecoveryLink()
            }
        }
        .padding(32)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}

// MARK: - Settings

struct MacSettingsView: View {
    @ObservedObject var deviceManager: DeviceManager
    @ObservedObject private var localBackend = LocalBackendManager.shared
    @EnvironmentObject var colorSchemeManager: ColorSchemeManager
    // 空 = 默认后端（Debug=localhost:8080 / Release=云端）；填了 = 逃生口（改连该地址，不启动本地后端）。
    @AppStorage(LocalPaths.scoped("xhan_server_url")) private var serverURL = ""
    // 「使用本地后端」开关：默认关（dev/prod 一致）。仅在 serverURL 为空时生效。
    @AppStorage(LocalBackendPreference.useLocalKey) private var useLocalBackend = LocalBackendPreference.useLocalDefault
    @AppStorage(LocalPaths.scoped("xhan_l3_alert_sound")) private var l3AlertSound = true
    // key/默认值见 LongReplyAlert（唯一事实来源）；key 带环境后缀，dev/prod 不串味
    @AppStorage(LongReplyAlert.soundKey) private var longReplyAlertSound = LongReplyAlert.defaultSoundEnabled
    @AppStorage(LongReplyAlert.secondsKey) private var longReplyAlertSeconds = LongReplyAlert.defaultSeconds
    @State private var showClearCacheAlert = false
    @State private var showAddDirSheet = false
    @State private var showAddExemptSheet = false
    @State private var authorizedDirs: [String] = DirectoryAuthorizationManager.shared.permanentDirs
    @State private var exemptPaths: [String] = SensitiveFileBlocker.exemptPaths
    @State private var showHardBlockedAlert = false
    @State private var hardBlockedPath = ""

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

            Section("提醒") {
                Toggle("提示音", isOn: $l3AlertSound)
                Toggle("久等回复完成提示音", isOn: $longReplyAlertSound)
                Picker("久等阈值", selection: $longReplyAlertSeconds) {
                    if LocalPaths.isDev {
                        Text("1 秒（测试用）").tag(1)
                    }
                    Text("5 秒").tag(5)
                    Text("2 分钟").tag(120)
                    Text("3 分钟").tag(180)
                    Text("5 分钟").tag(300)
                }
                .disabled(!longReplyAlertSound)
            }

            Section("文件访问授权") {
                ForEach(authorizedDirs, id: \.self) { dir in
                    HStack {
                        Text(dir)
                            .font(.system(.body, design: .monospaced))
                            .lineLimit(1)
                            .truncationMode(.middle)
                        Spacer()
                        Button {
                            DirectoryAuthorizationManager.shared.removePermanent(dir)
                            authorizedDirs = DirectoryAuthorizationManager.shared.permanentDirs
                        } label: {
                            Image(systemName: "xmark.circle.fill")
                                .foregroundStyle(.secondary)
                        }
                        .buttonStyle(.plain)
                    }
                }
                if authorizedDirs.isEmpty {
                    Text("暂无永久授权目录")
                        .foregroundStyle(.secondary)
                        .font(.subheadline)
                }
                Button {
                    showAddDirSheet = true
                } label: {
                    Label("添加授权目录", systemImage: "plus")
                }
                .buttonStyle(.borderless)
                .padding(.top, 4)

                Divider()

                HStack {
                    VStack(alignment: .leading, spacing: 2) {
                        Text("Agent 工作区（隐式授权，可读写）")
                            .font(.system(.body, design: .monospaced))
                        Text(AgentWorkspace.rootPath)
                            .font(.system(.caption, design: .monospaced))
                            .foregroundStyle(.secondary)
                            .lineLimit(1)
                            .truncationMode(.middle)
                    }
                    Spacer()
                    Button("在访达中打开") {
                        AgentWorkspace.ensureExists()
                        NSWorkspace.shared.open(URL(fileURLWithPath: AgentWorkspace.rootPath))
                    }
                    .buttonStyle(.borderless)
                }

                Divider()

                HStack {
                    VStack(alignment: .leading, spacing: 2) {
                        Text("工具输出溢出目录（只读，仅 fs 工具可读）")
                            .font(.system(.body, design: .monospaced))
                        Text(ToolOutputStore.shared.rootPath)
                            .font(.system(.caption, design: .monospaced))
                            .foregroundStyle(.secondary)
                            .lineLimit(1)
                            .truncationMode(.middle)
                    }
                    Spacer()
                    Button("在访达中打开") {
                        NSWorkspace.shared.open(URL(fileURLWithPath: ToolOutputStore.shared.rootPath))
                    }
                    .buttonStyle(.borderless)
                }
            }
            .sheet(isPresented: $showAddDirSheet) {
                AddDirView { dir in
                    DirectoryAuthorizationManager.shared.authorize(dir, permanent: true)
                    authorizedDirs = DirectoryAuthorizationManager.shared.permanentDirs
                }
            }

            Section("敏感文件豁免") {
                ForEach(exemptPaths, id: \.self) { path in
                    HStack {
                        Text(path)
                            .font(.system(.body, design: .monospaced))
                            .lineLimit(1)
                            .truncationMode(.middle)
                        Spacer()
                        Button {
                            SensitiveFileBlocker.removeExemption(path)
                            exemptPaths = SensitiveFileBlocker.exemptPaths
                        } label: {
                            Image(systemName: "xmark.circle.fill")
                                .foregroundStyle(.secondary)
                        }
                        .buttonStyle(.plain)
                    }
                }
                if exemptPaths.isEmpty {
                    Text("暂无豁免路径")
                        .foregroundStyle(.secondary)
                        .font(.subheadline)
                }
                Button {
                    showAddExemptSheet = true
                } label: {
                    Label("添加豁免路径", systemImage: "plus")
                }
                .buttonStyle(.borderless)
                .padding(.top, 4)
            }
            .sheet(isPresented: $showAddExemptSheet) {
                AddDirView(
                    title: "添加敏感文件豁免",
                    message: "添加后，该路径将跳过敏感文件拦截（如 .env、.git 内部等）。\n豁免只解除敏感锁，仍受「文件访问授权」约束。",
                    allowFiles: true,
                    chooseMessage: "选择要豁免的文件或目录"
                ) { path in
                    if SensitiveFileBlocker.isHardBlocked(path) {
                        hardBlockedPath = path
                        showHardBlockedAlert = true
                    } else {
                        SensitiveFileBlocker.addExemption(path)
                        exemptPaths = SensitiveFileBlocker.exemptPaths
                    }
                }
            }
            .alert("该路径硬性拦截，不可豁免", isPresented: $showHardBlockedAlert) {
                Button("知道了", role: .cancel) {}
            } message: {
                Text("\(hardBlockedPath)\n\n属于高危路径（如 shell 启动文件、.ssh、系统凭证等），为防止任意代码执行与凭证泄露，agent 与用户都无法放行。")
            }

#if DEBUG
            Section("账号") {
                LabeledContent("用户名", value: deviceManager.username ?? "-")
                LabeledContent("用户 ID", value: deviceManager.userId ?? "-")
                    .fontDesign(.monospaced)
            }
#endif

            if LocalBackendManager.isActive {
                // UI-5：模型与密钥（Release 也可见）。
                LocalModelKeySection(onLock: { LocalLockController.shared.lock() })
                // UI-6：本地服务状态 / 端口 / 数据目录 / 日志 / 重启。
                LocalServiceSection(backend: localBackend)
                // UI-11：本地数据备份与恢复。
                LocalBackupSection()
                // dev 专属：导入任意备份（含 prod）/ 清除 dev 数据。
                if LocalPaths.isDev {
                    LocalDevDataSection()
                }
            }

            Section {
                Toggle("使用本地后端（离线 / 私有，重启生效）", isOn: $useLocalBackend)
                TextField("服务地址（留空 = 用上面开关的默认后端）", text: $serverURL)
                    .textFieldStyle(.roundedBorder)
            } header: {
                Text("服务器")
            } footer: {
                Text("服务地址留空、未开「使用本地后端」时连默认后端（Debug=localhost:8080 / Release=云端）；打开开关 = app 自启动内嵌后端（127.0.0.1:\(LocalPaths.backendPort)）。填写服务地址则一律改连该地址。三者改动均需重启 app 生效。")
                    + (LocalPaths.isDev ? Text("\nDebug 构建连的是 dev 后端（连 prod 云端会被 403 拒绝）。") : Text(""))
            }

            Section("浏览") {
                BrowseRow("账单记录", icon: "list.bullet.rectangle", destination: BillListView(
                    viewModel: BillViewModel(apiClient: apiClient),
                    baseURL: deviceManager.baseURL,
                    token: deviceManager.token ?? ""
                ))
                BrowseRow("已归档会话", icon: "archivebox", destination: ArchivedConversationListView(apiClient: apiClient))
                BrowseRow("诊断日志", icon: "exclamationmark.bubble", destination: LogDiagnosticView())
            }

            Section("帮助") {
                BrowseRow("快捷键", icon: "keyboard", destination: ShortcutGuideView(), sheetSize: CGSize(width: 560, height: 640))
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
                    Task {
                        do {
                            try await LocalToolResultStore.shared.clearAll()
                        } catch {
                            LogBuffer.shared.error("clearAll local tool results: \(error)")
                        }
                    }
                    ToolOutputStore.shared.clearAll()
                }
            } message: {
                Text("将清除所有本地缓存数据（图表等）及子任务中间结果文件，下次打开会话时需重新生成。")
            }

            if !LocalBackendManager.isActive {
                Section {
                    Button("退出登录") {
                        Task { await deviceManager.logout() }
                    }
                    .buttonStyle(.borderless)
                    .foregroundStyle(.red)
                }
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
    let sheetSize: CGSize?
    @State private var showDetail = false

    init(_ title: String, icon: String, destination: Destination, sheetSize: CGSize? = nil) {
        self.title = title
        self.icon = icon
        self.destination = destination
        self.sheetSize = sheetSize
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
                .frame(
                    minWidth: sheetSize?.width ?? 500,
                    maxWidth: sheetSize?.width,
                    minHeight: sheetSize?.height ?? 400,
                    maxHeight: sheetSize?.height
                )
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
    @State private var highlightedIndex = 0
    @FocusState private var focusedField: Field?

    private enum Field { case username, password }

    private var usernameSuggestions: [String] {
        let typed = username.trimmingCharacters(in: .whitespaces)
        guard !typed.isEmpty else { return [] }
        return deviceManager.knownUsernames.filter {
            $0 != typed && $0.lowercased().hasPrefix(typed.lowercased())
        }
    }

    private func moveHighlight(_ delta: Int) {
        let count = usernameSuggestions.count
        guard count > 0 else { return }
        highlightedIndex = min(max(highlightedIndex + delta, 0), count - 1)
    }

    private func applyHighlightedSuggestion() {
        guard !usernameSuggestions.isEmpty else { return }
        username = usernameSuggestions[min(highlightedIndex, usernameSuggestions.count - 1)]
        focusedField = .password
    }

    private var suggestionList: some View {
        VStack(spacing: 0) {
            ForEach(Array(usernameSuggestions.enumerated()), id: \.element) { index, name in
                Button {
                    username = name
                    focusedField = .password
                } label: {
                    HStack(spacing: 8) {
                        Image(systemName: "person.circle")
                            .foregroundStyle(.secondary)
                        Text(name)
                        Spacer()
                    }
                    .padding(.horizontal, 8)
                    .padding(.vertical, 6)
                    .background(
                        highlightedIndex == index
                            ? Color.accentColor.opacity(0.2)
                            : Color.clear
                    )
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .onHover { hovering in
                    if hovering { highlightedIndex = index }
                }
            }
        }
        .frame(width: 260)
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 6))
        .overlay(
            RoundedRectangle(cornerRadius: 6)
                .stroke(Color.primary.opacity(0.1), lineWidth: 1)
        )
        .shadow(radius: 6, y: 2)
    }

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
                    .focused($focusedField, equals: .username)
                    .onChange(of: username) { _, _ in highlightedIndex = 0 }
                    .onKeyPress(.downArrow) {
                        guard !usernameSuggestions.isEmpty else { return .ignored }
                        moveHighlight(1)
                        return .handled
                    }
                    .onKeyPress(.upArrow) {
                        guard !usernameSuggestions.isEmpty else { return .ignored }
                        moveHighlight(-1)
                        return .handled
                    }
                    .overlay(alignment: .top) {
                        if focusedField == .username && !usernameSuggestions.isEmpty {
                            suggestionList.offset(y: 30)
                        }
                    }
                    .zIndex(1)

                SecureField("密码", text: $password)
                    .textFieldStyle(.roundedBorder)
                    .frame(maxWidth: 260)
                    .focused($focusedField, equals: .password)
            }
            .onSubmit {
                if focusedField == .username && !usernameSuggestions.isEmpty {
                    applyHighlightedSuggestion()
                } else {
                    Task { await performAuth() }
                }
            }

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

// MARK: - 添加授权目录

struct AddDirView: View {
    var title = "添加授权目录"
    var message = "xhanHelper 将永久获得此目录及其子目录的只读访问权限。\n敏感文件（如 .env、SSH 密钥等）始终会被拦截。"
    var allowFiles = false
    var chooseMessage = "选择要授权的目录"
    let onSelect: (String) -> Void
    @Environment(\.dismiss) private var dismiss
    @State private var pathText = ""
    @State private var showOpenPanel = false

    var body: some View {
        VStack(spacing: 20) {
            Text(title)
                .font(.headline)

            Text(message)
                .font(.subheadline)
                .foregroundStyle(.secondary)
                .multilineTextAlignment(.center)
                .fixedSize(horizontal: false, vertical: true)

            HStack {
                TextField("输入目录路径", text: $pathText)
                    .textFieldStyle(.roundedBorder)
                Button("浏览…") {
                    showOpenPanel = true
                }
                .buttonStyle(.bordered)
            }
            .frame(maxWidth: 400)
            .onAppear {
                DispatchQueue.main.async {
                    showOpenPanel = true
                }
            }

            HStack(spacing: 12) {
                Button("取消") {
                    dismiss()
                }
                .keyboardShortcut(.escape)

                Button("添加") {
                    let resolved = (pathText as NSString).expandingTildeInPath
                    let standardized = (resolved as NSString).standardizingPath
                    guard !standardized.isEmpty else { return }
                    onSelect(standardized)
                    dismiss()
                }
                .keyboardShortcut(.return)
                .disabled(pathText.trimmingCharacters(in: .whitespaces).isEmpty)
            }
            .padding(.top, 8)
        }
        .padding(24)
        .frame(minWidth: 400)
        .background(
            OpenDirectoryPanel(
                isPresented: $showOpenPanel,
                allowFiles: allowFiles,
                chooseMessage: chooseMessage,
                onSelect: { url in
                    pathText = url.path
                }
            )
        )
    }
}

struct OpenDirectoryPanel: NSViewRepresentable {
    @Binding var isPresented: Bool
    var allowFiles = false
    var chooseMessage = "选择要授权的目录"
    let onSelect: (URL) -> Void

    func makeNSView(context: Context) -> NSView {
        let view = NSView()
        DispatchQueue.main.async {
            guard isPresented else { return }
            let panel = NSOpenPanel()
            panel.canChooseDirectories = true
            panel.canChooseFiles = allowFiles
            panel.allowsMultipleSelection = false
            panel.prompt = "选择"
            panel.message = chooseMessage
            panel.begin { response in
                if response == .OK, let url = panel.url {
                    onSelect(url)
                }
                isPresented = false
            }
        }
        return view
    }

    func updateNSView(_ nsView: NSView, context: Context) {}
}
