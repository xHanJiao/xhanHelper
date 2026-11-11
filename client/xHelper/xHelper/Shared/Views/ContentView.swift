import SwiftUI

struct ContentView: View {
    @ObservedObject var deviceManager: DeviceManager
    #if os(macOS)
    /// 拖拽调宽的共享幽灵线状态（由 App 持有并注入，本视图只改不观察，
    /// 避免拖拽逐帧重渲染整个 ContentView；子视图通过 @ObservedObject 观察）。
    let resizeGhost: ResizeGhostState
    #endif
    @State private var selectedTab = "chat"
    @State private var isInChat = false
    @State private var lastSyncAt = Date.distantPast
    @Environment(\.scenePhase) private var scenePhase
    @Environment(\.colorScheme) private var colorScheme
    @StateObject private var chatViewModel: ChatViewModel
    @State private var conversationLoadError: String?
    /// iOS：恢复未发草稿时打开的会话；默认会话随后到达时别把它顶掉。
    @State private var restoredConversationOverride: String?
    @State private var restoredConversationTitle: String?

    @StateObject private var notificationManager = NotificationManager()
    @State private var showAckLoading = false
    @State private var ackErrorMessage: String?
    @State private var showAckError = false

#if os(macOS)
    @StateObject private var rightSidebar = RightSidebarManager()
    /// 右面板分隔线（seam）的 x 坐标（content 坐标系），由 SeamMarker 上报。
    @State private var rightSeamX: CGFloat = 0
    /// 布局宽（geo 上报）。内容比窗口宽时它是「溢出后的内容宽」而非窗口宽，只作回退用；
    /// 判断"窗口还能放多宽"一律走 `realWindowContentWidth()`（见 docs/bugs/033）。
    @State private var windowWidth: CGFloat = 800
    /// 顶栏内在最小宽（由 TopBarView 隐藏探针经 `TopBarMinWidthKey` 上报，量的是**当前降级档**）。
    /// 顶栏现在会按窗口宽自己降级（见 `docs/design/mac-topbar-degrade/design.md`），所以这个值
    /// 正常小于窗口底线、不再撑宽窗口；留作安全网——万一档位算乐观了，顶开窗口而不是让顶栏溢出。
    @State private var topBarMinWidth: CGFloat = 0
    /// 进入 PiP 前右栏宽度，退出时恢复。
    @State private var prePipSidebarWidth: Double?
    /// PiP 自动退出原因（onChange 里消费，避免状态翻转与副作用耦合）。
    @State private var pipExitReason = "userExit"

    @StateObject private var agentVM: AgentViewModel
    @StateObject private var billVM: BillViewModel
    @StateObject private var memoryVM: MemoryViewModel
    @StateObject private var artifactVM: ArtifactViewModel
    @StateObject private var documentVM: DocumentViewModel
    @StateObject private var eventVM: EventViewModel
    @StateObject private var imageVM: ImageViewModel
    @StateObject private var chatTabManager: ChatTabManager
    @StateObject private var conversationListVM: ConversationListViewModel

    private var api: APIClient {
        APIClient(baseURL: deviceManager.baseURL, token: deviceManager.token ?? "")
    }

    /// 中间区跨度：PiP 时聊天区让位给右栏（无中栏）；否则中栏 + 右侧 seam。
    private var middleSpan: Double {
        rightSidebar.isPipChat
            ? 0
            : Double(Layout.middleMinWidth + Layout.seamWidth)
    }

    /// 窗口真实内容宽。布局用的 `windowWidth`（geo）在内容比窗口宽时会报「溢出后的内容宽」，
    /// 不能当"窗口还能放多宽"用（见 docs/bugs/033）；取不到窗口时返回 nil，调用方回退到 geo。
    ///
    /// 单窗口假设：`NSApp.mainWindow/keyWindow` 即承载本视图的窗口 —— 由主场景是单窗口的 `Window`
    /// 保证（2026-10-15 起；此前 `WindowGroup` 的"新建窗口"/关窗重开会产生第二个窗口，见 docs/bugs/038）。
    /// 多窗口彻底收窄需要 per-window 状态，列为 docs/bugs/033 §5 的残余项。
    private func realWindowContentWidth() -> Double? {
        guard let window = NSApp.mainWindow ?? NSApp.keyWindow else { return nil }
        return Self.contentWidth(of: window)
    }

    private static func contentWidth(of window: NSWindow) -> Double {
        Double(window.contentView?.frame.width ?? window.frame.width)
    }

    /// 喂给顶栏降级的「可用宽」。用真实窗口内容宽而不是布局 `windowWidth`（geo）——
    /// 内容一旦比窗口宽，geo 报的是「溢出后的内容宽」（bug 033 的纪律），乐观输入会选出偏不降级的档。
    /// 取不到窗口时回退 geo。
    private var topBarAvailableWidth: CGFloat {
        CGFloat(realWindowContentWidth() ?? Double(windowWidth))
    }

    /// 喂给顶栏降级的「右簇可用宽」：右栏打开时顶栏按钮簇右对齐，只能待在**中栏卡片右侧那段**
    /// （卡片右缘 → 窗口内容右缘）再减顶栏自身右内边距 —— 否则中心槽组一显形就压在中栏卡片上
    /// （见 `docs/bugs/034`、`docs/design/mac-card-top-wrap`）。
    /// 右栏关着（卡片铺满整宽）/ PiP（整窗就是右栏）时中栏不占位 → `nil`，不限制。
    private var topBarTrailingLimit: CGFloat? {
        guard rightSidebar.isOpen, !rightSidebar.isPipChat else { return nil }
        return Layout.cardGap
            + Layout.seamHalfGap * 2 + Layout.seamWidth
            + CGFloat(rightSidebar.width)
            - Layout.toolbarHorizontalPadding
    }

    /// 右侧栏在当前窗口布局下可用的最大宽度（以窗口真实内容宽为准）。
    /// 减去中间跨度、右栏外扩。PiP 时中间让位，右栏可铺满整窗（不设 1200 上限）。
    private var rightSidebarMaxAvailable: Double {
        let available = (realWindowContentWidth() ?? Double(windowWidth))
            - middleSpan
            - Double(Layout.rightSidebarOuterPadding)
        let cap = rightSidebar.isPipChat ? 4000.0 : 1200.0
        return max(Double(Layout.rightSidebarMinWidth), min(cap, available))
    }

    /// 右栏打开时所需的内容最小宽（由 Layout 派生）。
    private static let rightSidebarOpenMinWidth: CGFloat = Layout.rightSidebarOpenMinContentWidth
    /// 右栏关闭时窗口最小内容宽：左右窗口边距 + 中栏最小宽。
    private var baseMinWidth: CGFloat {
        Layout.cardGap * 2 + Layout.middleMinWidth
    }
    /// PiP 时所需的内容最小宽：左右窗口边距 + 右栏最小宽（中栏让位）。
    private var pipMinContentWidth: CGFloat {
        Layout.cardGap * 2 + Layout.rightSidebarMinWidth
    }

    /// 窗口最小内容宽唯一派生入口：由 (isOpen, isPipChat) 实时推导，幂等设置，
    /// 消除"进入保存/退出恢复"的竞态。
    private func updateWindowMinWidth() {
        let bottomRowMin: CGFloat
        if rightSidebar.isOpen {
            bottomRowMin = rightSidebar.isPipChat ? pipMinContentWidth : Self.rightSidebarOpenMinWidth
        } else {
            bottomRowMin = baseMinWidth
        }
        // 取「底栏推导值 / 顶栏内在宽 / 全局下限」的较大者。顶栏项如今只是安全网：
        // 顶栏会按窗口宽自己降级（docs/design/mac-topbar-degrade），正常不会撑宽窗口。
        let minContent = max(bottomRowMin, topBarMinWidth, Layout.minWindowContentWidth)
        // 最小宽只有一个来源：`WindowMinWidthStore`。它 → 根视图 `.frame(minWidth:)` →
        // SwiftUI 落到 AppKit `contentMinSize`（主约束，所有缩放路径都认）；
        // `windowWillResize` 也读同一个值（第二道栅栏，只覆盖交互拖拽）。
        if abs(WindowMinWidthStore.shared.width - minContent) > 0.5 {
            WindowMinWidthStore.shared.width = minContent
        }

        // 内容真实最小宽变大时（如从「新对话」切到长标题会话，顶栏变宽），已缩窄的窗口必须
        // 主动放大——contentMinSize 只防再缩窄，不会自己把窗口撑大；windowWillResize 只在
        // 用户拖拽时生效，也管不到这种情况。
        // 用 mainWindow：切换会话走 popover，此刻 keyWindow 可能是 popover 而非主窗口。
        guard let window = NSApp.mainWindow ?? NSApp.keyWindow else { return }
        let contentW = Self.contentWidth(of: window)
        let deficit = minContent - contentW
        if deficit > 0.5 {
            var frame = window.frame
            frame.size.width += deficit
            window.setFrame(frame, display: true, animate: false)
        }
    }

    /// 离开主界面（登出 / 未配置页 / 解锁门）时把最小宽复位：否则那些页面的窗口会被
    /// 上一个界面的底线卡住（delegate 现在挂在每个窗口上，见 docs/bugs/033）。
    private func resetWindowMinWidth() {
        WindowMinWidthStore.shared.width = WindowMinWidthStore.defaultWidth
    }

    /// 把右侧栏宽度钳制到可用范围，保证窄窗口下不溢出、内容可见。
    /// PiP 时改为"填满"：左栏变窄/窗口变宽后右栏自动补位占满释放的空间。
    private func clampRightSidebarWidth() {
        guard rightSidebar.isOpen, windowWidth > 0 else { return }
        if rightSidebar.isPipChat {
            let fill = rightSidebarMaxAvailable
            if fill != rightSidebar.width {
                pipLog("right sidebar fill: \(Int(rightSidebar.width)) -> \(Int(fill))")
                rightSidebar.width = fill
            }
        } else {
            let clamped = min(max(rightSidebar.width, Double(Layout.rightSidebarMinWidth)), rightSidebarMaxAvailable)
            if clamped != rightSidebar.width {
                pipLog("right sidebar clamp: \(Int(rightSidebar.width)) -> \(Int(clamped))")
                rightSidebar.width = clamped
            }
        }
    }

    /// 右栏打开时更新窗口：窗口太窄则自动放大 → 设最小内容宽（防再缩窄）。
    private func updateWindowForRightSidebarOpen() {
        // 通知窗口最小内容宽（windowWillResize 与根视图 minWidth 会据此拒绝更窄）
        updateWindowMinWidth()

        // 自动放大窗口到所需内容宽（按内容缺口增量放大，保留窗口 chrome）
        guard let window = NSApp.keyWindow else { return }
        let deficit = Self.rightSidebarOpenMinWidth - Self.contentWidth(of: window)
        if deficit > 0 {
            var frame = window.frame
            frame.size.width += deficit
            window.setFrame(frame, display: true, animate: true)
        }
    }

    /// 右栏关闭时恢复窗口最小内容宽。
    private func restoreWindowMinWidth() {
        updateWindowMinWidth()
    }

    // MARK: - 聊天画中画（PiP）

    /// 进入 PiP 副作用（由 onChange(of: isPipChat) 驱动，幂等）。
    private func enterPipEffects() {
        guard rightSidebar.isPipChat else { return }
        rightSidebar.pipCollapsed = true
        prePipSidebarWidth = rightSidebar.width
        pipExitReason = "userExit"
        rightSidebar.width = rightSidebarMaxAvailable
        updateWindowMinWidth()
        guard let window = NSApp.keyWindow else { return }
        let deficit = pipMinContentWidth - Self.contentWidth(of: window)
        if deficit > 0 {
            var frame = window.frame
            frame.size.width += deficit
            window.setFrame(frame, display: true, animate: true)
        }
        pipLog("enter: prePipWidth=\(Int(prePipSidebarWidth ?? 0)) windowW=\(Int(windowWidth))")
    }

    /// 退出 PiP 副作用（onChange 统一入口，reason 用于诊断）。
    private func exitPipEffects(reason: String) {
        let restored = min(prePipSidebarWidth ?? rightSidebar.width, rightSidebarMaxAvailable)
        prePipSidebarWidth = nil
        withAnimation(.easeInOut(duration: 0.2)) {
            rightSidebar.width = restored
        }
        updateWindowMinWidth()
        pipLog("exit: reason=\(reason) restoredWidth=\(Int(restored)) maxAvailable=\(Int(rightSidebarMaxAvailable))")
    }

    /// 自动退出触发点统一入口：先记 reason，再翻转状态（副作用由 onChange 消费）。
    private func autoExitPip(reason: String) {
        guard rightSidebar.isPipChat else { return }
        pipExitReason = reason
        rightSidebar.isPipChat = false
    }
#endif

    /// 当前活跃的聊天 VM（macOS 为当前聊天 tab；iOS 为全局单例）
    private var activeChatViewModel: ChatViewModel? {
        #if os(macOS)
        return chatTabManager.currentTab?.viewModel
        #else
        return chatViewModel
        #endif
    }

    /// 回到前台：正在流式的会话优先续传（补发断点后的事件并继续跟流）；
    /// 续不上（没有 turn id / 老后端）时由 resumeStreaming 自己收尾，再走拉历史。
    private func onReturnedToForeground() {
        Task { await deviceManager.verifySessionOnForeground() }
        #if os(macOS)
        // 多 tab：每个在流式的 tab 各自续（hub buffer 按会话区分，不会串台）
        for tab in chatTabManager.tabs where tab.viewModel.isStreaming {
            _ = tab.viewModel.resumeStreaming()
        }
        #else
        _ = chatViewModel.resumeStreaming()
        #endif

        let now = Date()
        if !(activeChatViewModel?.isStreaming ?? false), now.timeIntervalSince(lastSyncAt) >= 3 {
            lastSyncAt = now
            Task { await syncActiveChat() }
        }

        Task { await syncReminders() }
        #if os(macOS)
        // 回到前台时刷新会话列表，清掉服务端已销毁（如无痕）的幽灵条目
        Task { await conversationListVM.reload() }
        #endif
    }

    /// 会话失效后登录回来：恢复未发消息（只填输入框/排队区，不自动发送）。
    @MainActor
    private func restoreUnsentDraftIfAny() async {
        guard let draft = UnsentDraftStore.take() else { return }

        #if os(macOS)
        if let cid = draft.conversationId {
            await chatTabManager.openConversation(id: cid, title: draft.title ?? "会话")
        }
        guard let vm = chatTabManager.currentTab?.viewModel else { return }
        #else
        if let cid = draft.conversationId, cid != chatViewModel.conversationId {
            restoredConversationOverride = cid
            restoredConversationTitle = draft.title ?? "会话"
            await chatViewModel.loadConversation(id: cid, title: draft.title ?? "会话")
        }
        let vm = chatViewModel
        #endif

        let client = APIClient(baseURL: deviceManager.baseURL, token: deviceManager.token ?? "")
        if let first = draft.items.first {
            vm.draftText = first.text
            for imageID in first.imageIds {
                guard let data = try? await client.fetchImageData(id: imageID),
                      let image = PlatformImage(data: data) else { continue }
                vm.draftAttachments.append(
                    DraftAttachment(data: data, image: image, filename: "\(imageID).png"))
            }
        }
        // 队列里剩余的消息：文本入队并暂停，等用户点「继续」，避免一登录就连发。
        for item in draft.items.dropFirst() where !item.text.isEmpty {
            vm.pendingSends.append(PendingSend(
                text: item.text,
                attachments: [],
                baseURL: deviceManager.baseURL,
                token: deviceManager.token ?? "",
                agentId: nil,
                speakReply: false))
        }
        if !vm.pendingSends.isEmpty { vm.pendingPaused = true }
        LogBuffer.shared.info("restored unsent draft: \(draft.items.count) item(s)")
    }

    /// 会话失效前抓取当前会话的未发内容（回前台校验等不经过 SSE 的 401 路径）。
    @MainActor
    private func captureUnsentBeforeLogout() {
        guard !UnsentDraftStore.hasDraft else { return }
        #if os(macOS)
        guard let vm = chatTabManager.currentTab?.viewModel else { return }
        #else
        let vm = chatViewModel
        #endif
        var items: [UnsentDraftItem] = []
        if !vm.draftText.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            items.append(UnsentDraftItem(text: vm.draftText, imageIds: []))
        }
        for pending in vm.pendingSends where !pending.text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            items.append(UnsentDraftItem(text: pending.text, imageIds: []))
        }
        guard !items.isEmpty else { return }
        let cid = (vm.conversationId ?? "").isEmpty ? nil : vm.conversationId
        UnsentDraftStore.save(UnsentDraft(conversationId: cid, title: vm.conversationTitle, items: items))
    }

    private func syncActiveChat() async {        #if os(macOS)
        // 激活时刷新当前所在对话（而非默认会话），避免劫持其他会话 tab
        guard let tab = chatTabManager.currentTab,
              !tab.isBlank,
              let cid = tab.conversationId, !cid.isEmpty else { return }
        LogBuffer.shared.debug("sync conversation \(cid) after scene active (macOS)")
        await tab.viewModel.loadConversation(id: cid, title: tab.title, mode: tab.viewModel.mode, preserveTransientState: true) // scene 激活刷新：保持当前模式/临时态
        #else
        guard let conv = deviceManager.defaultConversation else { return }
        LogBuffer.shared.debug("sync conversation \(conv.id) after scene active")
        await chatViewModel.loadConversation(id: conv.id, title: conv.title, mode: conv.mode)
        #endif
    }

#if os(macOS)
    /// Cmd+Shift+↑ / ↓：在会话列表中上/下切换会话（delta：-1 上移，+1 下移）。
    /// 当前会话不在已加载列表（新建空 tab / 分页未加载）时回退到列表第一项（最近会话）。
    private func switchConversation(delta: Int) async {
        if !conversationListVM.hasLoadedOnce {
            await conversationListVM.load()
        }
        let conversations = conversationListVM.conversations
        guard !conversations.isEmpty else { return }

        let currentID = chatTabManager.currentConversationId
        let target: Conversation
        if let idx = conversations.firstIndex(where: { $0.id == currentID }) {
            let newIdx = min(max(idx + delta, 0), conversations.count - 1)
            target = conversations[newIdx]
        } else {
            target = conversations[0]
        }

        await chatTabManager.openConversation(id: target.id, title: target.title, pushHistory: true, mode: target.mode, ephemeral: target.ephemeral == true)
    }

    /// 注册 App 级快捷键桥（macOS：Cmd+B / Tab·Esc L3 确认）。
    private func registerAppShortcutBridge() {
        // ContentView 是 struct，闭包捕获的是值拷贝，无循环引用，不需 weak
        AppShortcutBridge.shared.onNewChat = {
            self.chatTabManager.newChat()
        }
        AppShortcutBridge.shared.onSwitchConversation = { delta in
            Task { await self.switchConversation(delta: delta) }
        }
        AppShortcutBridge.shared.onPasteImages = { datas in
            self.activeChatViewModel?.enqueuePastedImages(datas)
        }
        AppShortcutBridge.shared.confirmationPending = {
            !(self.activeChatViewModel?.pendingToolConfirmations.isEmpty ?? true)
        }
        AppShortcutBridge.shared.resolveConfirmation = { allow in
            self.activeChatViewModel?.resolveTopToolConfirmation(allow: allow)
        }
        // 全局语音热键（app 级常驻）：窗口关着 / PiP 收起时也要能拿到当前会话和后端凭据
        let tabs = chatTabManager
        AppShortcutBridge.shared.currentChatViewModel = { [weak tabs] in
            tabs?.currentTab?.viewModel
        }
        let device = deviceManager
        AppShortcutBridge.shared.voiceCredentials = {
            let token = device.token ?? ""
            guard !token.isEmpty else { return nil }
            return (baseURL: device.baseURL, token: token)
        }
    }
#endif

    #if os(macOS)
    init(deviceManager: DeviceManager, resizeGhost: ResizeGhostState) {
        self.deviceManager = deviceManager
        self.resizeGhost = resizeGhost
        let api = APIClient(baseURL: deviceManager.baseURL, token: deviceManager.token ?? "")
        _chatViewModel = StateObject(wrappedValue: ChatViewModel(apiClient: api))
        _agentVM = StateObject(wrappedValue: AgentViewModel(apiClient: api))
        _billVM = StateObject(wrappedValue: BillViewModel(apiClient: api))
        _memoryVM = StateObject(wrappedValue: MemoryViewModel(apiClient: api))
        _artifactVM = StateObject(wrappedValue: ArtifactViewModel(apiClient: api))
        _documentVM = StateObject(wrappedValue: DocumentViewModel(apiClient: api))
        _eventVM = StateObject(wrappedValue: EventViewModel(apiClient: api))
        _imageVM = StateObject(wrappedValue: ImageViewModel(apiClient: api))
        _chatTabManager = StateObject(wrappedValue: ChatTabManager {
            ChatViewModel(apiClient: APIClient(baseURL: deviceManager.baseURL, token: deviceManager.token ?? ""))
        })
        _conversationListVM = StateObject(wrappedValue: ConversationListViewModel(
            apiClient: APIClient(baseURL: deviceManager.baseURL, token: deviceManager.token ?? "")
        ))
    }
    #else
    init(deviceManager: DeviceManager) {
        self.deviceManager = deviceManager
        let api = APIClient(baseURL: deviceManager.baseURL, token: deviceManager.token ?? "")
        let vm = ChatViewModel(apiClient: api)
        vm.onConversationGone = { [weak vm] cid in
            NotificationCenter.default.post(name: .conversationRemoved, object: nil, userInfo: ["id": cid])
            // 当前会话被删 → 回到空白新对话，不再落到默认会话（那个概念正在淡出）
            Task { @MainActor in
                vm?.resetForNewChat()
            }
        }
        // iOS 没有右栏：产物走 ChatView 的 sheet（macOS 由 ChatTabManager 接线）
        vm.onOpenArtifact = { id, title in
            ArtifactOpenCoordinator.shared.open(id: id, title: title)
        }
        _chatViewModel = StateObject(wrappedValue: vm)
    }
    #endif

    var body: some View {
        ZStack {
            Group {
#if os(iOS)
                TabView(selection: $selectedTab) {
                    NavigationStack {
                        ConversationListContentView(deviceManager: deviceManager, isInChat: $isInChat, chatViewModel: chatViewModel, loadError: conversationLoadError)
                    }
                    .toolbarBackground(Color("chatBackground"), for: .tabBar)
                    .toolbarBackground(.visible, for: .tabBar)
                    .tabItem {
                        Label("聊天", systemImage: "bubble.left.and.bubble.right")
                    }
                    .tag("chat")

                    NavigationStack {
                        SettingsView(deviceManager: deviceManager)
                    }
                    .toolbarBackground(Color("chatBackground"), for: .tabBar)
                    .toolbarBackground(.visible, for: .tabBar)
                    .tabItem {
                        Label("设置", systemImage: "gear")
                    }
                    .tag("settings")
                }
#else
                VStack(spacing: 0) {
                    TopBarView(
                        availableWidth: topBarAvailableWidth,
                        trailingLimit: topBarTrailingLimit,
                        rightSidebar: rightSidebar,
                        chatTabManager: chatTabManager,
                        conversationListVM: conversationListVM
                    )
                    .zIndex(1)
                    .onPreferenceChange(TopBarMinWidthKey.self) { width in
                        guard width > 0, abs(width - topBarMinWidth) > 0.5 else { return }
                        topBarMinWidth = width
                        updateWindowMinWidth()
                    }

                    GeometryReader { geo in
                        // 分隔条上限 = 侧栏可用宽（与 clamp 同源，见 rightSidebarMaxAvailable）。
                        let rightMax: Double = rightSidebarMaxAvailable
                        HStack(alignment: .top, spacing: 0) {
                            HSpace(Layout.cardGap)

                            if !rightSidebar.isPipChat {
                                macOSContentArea
                            }
                            if rightSidebar.isOpen {
                                // PiP 时中间区消失、两 seam 重合，只保留左 seam（避免缝隙翻倍）
                                if !rightSidebar.isPipChat {
                                    HSpace(Layout.seamHalfGap)
                                    SeamMarker(x: $rightSeamX)
                                    HSpace(Layout.seamHalfGap)
                                }

                                RightSidebarView(
                                    manager: rightSidebar,
                                    billVM: billVM,
                                    memoryVM: memoryVM,
                                    artifactVM: artifactVM,
                                    documentVM: documentVM,
                                    eventVM: eventVM,
                                    agentVM: agentVM,
                                    imageVM: imageVM,
                                    baseURL: deviceManager.baseURL,
                                    token: deviceManager.token ?? ""
                                )
                                .frame(width: rightSidebar.isPipChat ? nil : rightSidebar.width)
                                .transition(.move(edge: .trailing))
                            }

                            HSpace(Layout.cardGap)
                        }
                        .coordinateSpace(name: ResizeDividerSpace.name)
                        .overlay(alignment: .topLeading) {
                            ZStack(alignment: .topLeading) {
                                // 右手柄：浮在右分隔线（seam）上方（PiP 时中间区消失，右 seam 隐藏）
                                if rightSidebar.isOpen && !rightSidebar.isPipChat {
                                    ResizableDivider(
                                        id: "right",
                                        width: $rightSidebar.width,
                                        minWidth: Double(Layout.rightSidebarMinWidth),
                                        maxWidth: rightMax,
                                        reversed: true,
                                        ghost: resizeGhost,
                                        seamX: $rightSeamX
                                    )
                                }
                            }
                        }
                        .overlay(alignment: .topLeading) {
                            // 聊天画中画面板：悬浮在右栏浏览器之上，可拖动/吸附/收起
                            if rightSidebar.isPipChat, let vm = chatTabManager.currentTab?.viewModel {
                                FloatingChatPanel(
                                    viewModel: vm,
                                    manager: rightSidebar,
                                    loadError: conversationLoadError,
                                    baseURL: deviceManager.baseURL,
                                    token: deviceManager.token ?? "",
                                    containerSize: geo.size,
                                    onExit: { autoExitPip(reason: "userExit") }
                                )
                            }
                        }
                        .animation(.easeInOut(duration: 0.2), value: rightSidebar.isOpen)
                        .onChange(of: geo.size.width, initial: true) { _, new in
                            windowWidth = new
                            clampRightSidebarWidth()
                        }
                        .onChange(of: rightSidebar.isOpen) { _, isOpen in
                            if !isOpen {
                                autoExitPip(reason: "sidebarClosed")
                            }
                            // 浏览器按钮组随侧栏开/关注册/注销，内容底线变化
                            if isOpen {
                                updateWindowForRightSidebarOpen()
                                clampRightSidebarWidth()
                            } else {
                                restoreWindowMinWidth()
                            }
                            // 浏览器按钮组随侧栏可见性注册/注销（修复：侧栏收起时左槽组残留）
                            BrowserToolProvider.shared.refreshBrowserGroup()
                        }
                        .onChange(of: rightSidebar.isPipChat) { _, isPip in
                            if isPip {
                                enterPipEffects()
                            } else {
                                exitPipEffects(reason: pipExitReason)
                            }
                            // 顶栏按钮组随 PiP 注册/注销，内容底线变化
                            clampRightSidebarWidth()
                        }
                        .onChange(of: rightSidebar.activeTabId) { _, _ in
                            BrowserToolProvider.shared.refreshBrowserGroup()
                        }
                    }
                    VSpace(Layout.cardBottomGap)
                }
                .overlay(alignment: .top) {
                    VStack(spacing: 0) {
                        Color(nsColor: .clear)
                            .frame(height: 26)
                    }
                    .allowsHitTesting(false)
                }
                .ignoresSafeArea(edges: .top)
                // 内部跳链：文档正文/聊天里由模型写出的 [标题](xhanhelper://document/<id>)。
                // 装在最外层，聊天气泡与右栏文档正文两处 Markdown 都能被拦到；
                // 非本 scheme 的 URL 原样交还系统（外部链接行为不变）。
                .environment(\.openURL, OpenURLAction { url in
                    guard url.scheme == "xhanhelper" else { return .systemAction(url) }
                    if let id = DocumentSelectionCoordinator.documentID(from: url) {
                        DocumentSelectionCoordinator.shared.openDocument(id: id)
                    }
                    return .handled // 本 scheme 的未知路径：吞掉，不交给系统
                })
                .onAppear {
                    // 启动即按当前状态确立窗口最小宽（此前只在状态变化时才更新，冷启动停在默认值）。
                    updateWindowMinWidth()
                    BrowserToolProvider.shared.rightSidebar = rightSidebar
                    DocumentSelectionCoordinator.shared.rightSidebar = rightSidebar
                    DocumentSelectionCoordinator.shared.documentVM = documentVM
                    DocumentSelectionCoordinator.shared.configure(baseURL: deviceManager.baseURL, token: deviceManager.token ?? "")
                    DocumentSelectionCoordinator.shared.ensureRegisteredGroup()
                    ArtifactOpenCoordinator.shared.rightSidebar = rightSidebar
                    ArtifactOpenCoordinator.shared.artifactVM = artifactVM
                    ArtifactOpenCoordinator.shared.configure(baseURL: deviceManager.baseURL, token: deviceManager.token ?? "")
                    ArtifactOpenCoordinator.shared.ensureRegisteredGroup()
                    registerAppShortcutBridge()
                }
                .onDisappear {
                    // 离开主界面（登出 / 未配置页 / 解锁门）时复位最小宽，别让上个界面
                    // 算出的底线卡住那些页面的窗口。
                    resetWindowMinWidth()
                }
                .onReceive(NotificationCenter.default.publisher(for: NSWindow.didResizeNotification)) { note in
                    // 窗口帧层始终触发：geo 在内容比窗口宽时会冻结在「溢出后的内容宽」不再变化，
                    // 侧栏 clamp 不能只挂在 geo 上（docs/bugs/014 的布点教训）。这里保证窗口真变宽/窄时
                    // 尺子一定重算 —— 也覆盖"系统平铺绕过 contentMinSize"这类残余路径（减损，见 docs/bugs/033 §5）。
                    guard let window = note.object as? NSWindow,
                          window === (NSApp.mainWindow ?? NSApp.keyWindow) else { return }
                    clampRightSidebarWidth()
                }
#endif
            }
            .onAppear {
                // 会话失效时（任意 401 路径）先把当前未发内容截下来
                deviceManager.onSessionExpiring = { captureUnsentBeforeLogout() }
            }
            .onChange(of: scenePhase) { _, newPhase in                #if os(macOS)
                // 前台周期刷新会话列表；离开激活态停止，避免后台空转
                if newPhase == .active { conversationListVM.startAutoRefresh() }
                else { conversationListVM.stopAutoRefresh() }
                #endif
                guard newPhase == .active else { return }
                onReturnedToForeground()
            }
#if os(macOS)
            .onReceive(NotificationCenter.default.publisher(for: NSApplication.didBecomeActiveNotification)) { _ in
                conversationListVM.startAutoRefresh()
                onReturnedToForeground()
            }
#endif
#if os(macOS)
            // 启动开新对话（空白 tab，发首条消息才落库），不再自动打开默认会话
            .task {
                chatTabManager.newChat()
                await restoreUnsentDraftIfAny()
            }
#else
            .task(id: deviceManager.defaultConversation?.id) {
                // 恢复草稿时已打开原会话：默认会话随后到达时别把它顶掉（会清空刚恢复的队列）。
                // 这个 task 可能因 defaultConversation 变化被取消，所以这里要补一次加载，
                // 不能只 return，否则会停在「加载消息失败」的空会话。
                if let override = restoredConversationOverride {
                    let title = restoredConversationTitle ?? "会话"
                    restoredConversationOverride = nil
                    restoredConversationTitle = nil
                    if deviceManager.defaultConversation?.id != override {
                        await chatViewModel.loadConversation(id: override, title: title)
                        return
                    }
                }
                if let conv = deviceManager.defaultConversation {
                    LogBuffer.shared.debug("loading conversation \(conv.id)")
                    await chatViewModel.loadConversation(id: conv.id, title: conv.title, mode: conv.mode)
                } else {
                    chatViewModel.isInitialLoading = false
                }
                await restoreUnsentDraftIfAny()
            }
#endif
            .onReceive(NotificationCenter.default.publisher(for: .reminderTapped)) { notification in
                guard let reminderId = notification.userInfo?["reminder_id"] as? String, !reminderId.isEmpty else { return }
                LogBuffer.shared.info("[Reminder] tap: processing reminder_id=\(reminderId)")
                Task { await handleReminderTap(reminderId: reminderId) }
            }
            .onReceive(NotificationCenter.default.publisher(for: .remindersSyncTriggered)) { _ in
                DispatchQueue.main.async {
                    Task {
                        try? await Task.sleep(nanoseconds: 15_000_000_000)
                        await syncReminders()
                    }
                }
            }

            if showAckLoading {
                Color.black.opacity(0.4).ignoresSafeArea()
                VStack(spacing: 16) {
                    ProgressView()
                        .scaleEffect(1.5)
                    Text("处理中...")
                        .foregroundColor(.white)
                        .font(.headline)
                }
                .frame(minWidth: 120, minHeight: 120)
                .background(.ultraThinMaterial, in: RoundedRectangle(cornerRadius: 16))
            }
        }
        .alert("提醒通知", isPresented: $showAckError) {
            Button("好", role: .cancel) {}
        } message: {
            Text(ackErrorMessage ?? "未知错误")
        }
    }

#if os(macOS)
    @ViewBuilder
    private var macOSContentArea: some View {
        Group {
            if let vm = chatTabManager.currentTab?.viewModel {
                ChatPage(
                    chatViewModel: vm,
                    loadError: conversationLoadError,
                    baseURL: deviceManager.baseURL,
                    token: deviceManager.token ?? "",
                    isCurrentTab: true
                )
            } else {
                ProgressView()
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            }
        }
        .padding(.horizontal, 12)
        // 中栏卡片：上边停在窗口内 `cardGap` 处（与左/右/下对称），向上溢出 `cardTopRise`
        // 把红绿灯 + 会话下拉包进卡片 —— 四条边都看得见（完整卡片，不是压在窗顶）。
        // 右栏关着时中栏铺满整宽，于是顶栏**所有**按钮都落在卡片里。
        // 见 docs/design/mac-card-top-wrap/design.md。
        .background(alignment: .top) {
            RoundedRectangle(cornerRadius: 12)
                .fill(Color.white.opacity(Layout.cardFillOpacity))
                .padding(.top, -Layout.cardTopRise)
                // 与描边同理：溢出到顶栏那一截不参与命中（今天顶栏 zIndex 更高本就轮不到它，防将来被提层级/按包围盒命中）
                .allowsHitTesting(false)
        }
        // 卡片投影：只在卡片外投（挖掉卡片内部），浅色黑 / 深色白 —— 见 `CardElevationShadow`
        .modifier(CardElevationShadow(topExtension: Layout.cardTopRise))
        .overlay(
            RoundedRectangle(cornerRadius: 12)
                .strokeBorder(Color("assistantText").opacity(0.12), lineWidth: 0.5)
                .padding(.top, -Layout.cardTopRise)
                // 非可命中：溢出到顶栏那一截不许截走顶栏的手势/空白双击缩放
                .allowsHitTesting(false))
        .frame(minWidth: Layout.middleMinWidth, idealWidth: Layout.middleMinWidth,
               maxWidth: .infinity,
               alignment: .leading)
        .layoutPriority(1)
    }
#endif

    // MARK: - Reminder sync

    private func syncReminders() async {
        guard let token = deviceManager.token else { return }
        let api = APIClient(baseURL: deviceManager.baseURL, token: token)
        do {
            // 翻页拉全量：一页 50 条不够时，第 51 条起既不镜像也不排通知
            var items: [Reminder] = []
            var offset = 0
            var serverTotal = 0
            for _ in 0..<4 {
                let page = try await api.listReminders(status: "pending", limit: 50, offset: offset)
                serverTotal = page.total
                items.append(contentsOf: page.items)
                offset += page.items.count
                if page.items.isEmpty || items.count >= page.total { break }
            }
            // 超过 4 页（200 条）才会剩下没拉到的，此时 total 大于实到条数 → 下游进入「截断保护」
            await notificationManager.syncWithServer(pending: items, total: max(serverTotal, items.count))
        } catch {
            LogBuffer.shared.error("[Reminder] sync error: \(error)")
        }
    }

    // MARK: - Notification tap → ack → navigate

    private func handleReminderTap(reminderId: String) async {
        showAckLoading = true
        defer { showAckLoading = false }

        guard let token = deviceManager.token else {
            ackErrorMessage = "未登录"
            showAckError = true
            return
        }

        let api = APIClient(baseURL: deviceManager.baseURL, token: token)

        var lastError: Error?
        for attempt in 1...3 {
            do {
                let response = try await api.ackReminder(id: reminderId)
                LogBuffer.shared.info("[Reminder] ack: success id=\(response.id) trigger=\(response.triggerMessage.prefix(40))")
                let convId = response.conversationId ?? ""

                guard response.status == "acknowledged" else {
                    if response.status == "cancelled" {
                        ackErrorMessage = "此提醒已取消"
                        showAckError = true
                    }
                    return
                }

                // 跳回**创建这条提醒的那段对话**（服务端 ack 把触发消息写在那里）。
                // 没有来源会话（秘书/反思自动生成的提醒）或已删 → 停在原地，只弹一句文案，
                // 不再落默认会话（那个概念正在淡出）。文案是 LLM 生成的，截断防顶满屏。
                guard !convId.isEmpty else {
                    let text = response.triggerMessage
                    ToastCenter.shared.show(text.count > 120 ? String(text.prefix(120)) + "…" : text)
                    return
                }
                DispatchQueue.main.async {
#if !os(macOS)
                    selectedTab = "chat"
#endif
                }
#if os(macOS)
                await chatTabManager.openConversation(id: convId, title: response.conversationTitle ?? "")
#else
                await chatViewModel.loadConversation(id: convId, title: response.conversationTitle ?? "", mode: nil)
#endif
                return
            } catch {
                lastError = error
                let nsErr = error as NSError
                if nsErr.code >= 400 && nsErr.code < 500 {
                    ackErrorMessage = nsErr.localizedDescription
                    showAckError = true
                    return
                }
                if attempt < 3 {
                    try? await Task.sleep(nanoseconds: UInt64(attempt) * 1_000_000_000)
                }
            }
        }

        LogBuffer.shared.error("[Reminder] ack: all retries failed, last=\(String(describing: lastError))")
        ackErrorMessage = "网络错误，请稍后重试"
        showAckError = true
    }
}

struct ConversationListContentView: View {
    @ObservedObject var deviceManager: DeviceManager
    @Binding var isInChat: Bool
    @ObservedObject var chatViewModel: ChatViewModel
    let loadError: String?

    var body: some View {
        ZStack {
            if let msg = loadError, chatViewModel.messages.isEmpty {
                VStack(spacing: 12) {
                    Text(msg).foregroundColor(.secondary)
                }
            } else {
                ChatView(viewModel: chatViewModel, agentId: nil, baseURL: deviceManager.baseURL, token: deviceManager.token ?? "")
                    .onAppear { isInChat = true }
                    .onDisappear { isInChat = false }
            }
        }
    }
}

#if os(macOS)
struct OpenSettingsButton: View {
    @Environment(\.openSettings) private var openSettings
    var body: some View {
        Button {
            openSettings()
        } label: {
            Image(systemName: "gear")
        }
    }
}
#endif
