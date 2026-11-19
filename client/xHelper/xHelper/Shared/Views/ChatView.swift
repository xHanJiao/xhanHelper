import SwiftUI
import UniformTypeIdentifiers
#if os(iOS)
import UIKit
#else
import AppKit
#endif

#if os(macOS)
/// 取本视图所在窗口：空格监听只接管发给自己的窗口，避免 PiP/设置窗抢键
private struct HostWindowReader: NSViewRepresentable {
    let onChange: (NSWindow?) -> Void

    func makeNSView(context: Context) -> NSView {
        let view = NSView()
        DispatchQueue.main.async { onChange(view.window) }
        return view
    }

    func updateNSView(_ nsView: NSView, context: Context) {
        DispatchQueue.main.async { onChange(nsView.window) }
    }
}
#endif

struct ChatView: View {
    @ObservedObject var viewModel: ChatViewModel
    /// 语音服务挂在 VM 上（不是本视图的 @StateObject）：进出 PiP 会拆掉并重建 ChatView，
    /// 服务留在视图里会连带结束语音会话、掐断朗读（见 docs/bugs/062）。VM 会把它的
    /// objectWillChange 转发过来，视图照常跟着刷新。
    private var voice: VoiceChatService { viewModel.voice }
    /// 本视图是否对 `voice` 计过一次在屏占用（见 `releaseVoiceIfLast`）
    @State private var voiceHolding = false
    /// 上一次接过语音链路的 viewModel：换会话时要先把它的朗读回调摘掉（见 `wireVoice`）
    @State private var wiredViewModel: ChatViewModel?
    @State private var isEditing = false
    @State private var dismissKeyboardToken: UUID?
    @State private var scrollPosition = ScrollPosition(edge: .top)
    @State private var showScrollToBottom = false
    let agentId: String?

    /// 输入栏智能体选择器状态：当前选中 / 分页列表 / 加载进度
    @State private var selectedAgentID: String?
    @State private var pickerAgents: [InputBarAgent] = []
    @State private var pickerCursor = 0
    @State private var pickerHasMore = false
    @State private var isLoadingPicker = false
    /// 首次列表加载是否完成（完成前不展示按钮，避免空列表闪烁；用户无智能体时保持隐藏）
    @State private var hasLoadedAgents = false

    // MARK: 输入框附件（选图 → 上传 → 发送）
    @State private var showFileImporter = false
    /// 本视图输入框是否聚焦（⌘V 粘贴定位收件 ChatView 用，见 viewModel.pastedImages）
    @State private var inputFocused = false

    // MARK: 输入栏折叠的更多操作
    /// 展开态：在按钮行上方多出一行（同一胶囊内）。12s 无操作自动收起。
    @State private var inputBarExtrasExpanded = false
    @State private var extrasCollapseTask: Task<Void, Never>?
    /// 每个按钮常驻/收起的配置（右键菜单切换，持久化，主/PiP 实例共享）。
    @ObservedObject private var inputBarLayout = InputBarLayoutState.shared
    /// 完全信任开关（全局持久化，主/PiP 共享）+ 开启前的二次确认。
    @ObservedObject private var trustStore = ToolTrustStore.shared
    @State private var showTrustEnableConfirm = false

    /// 底部浮层栈（AskUserBar/计划便签/子任务面板/L3 确认条/输入栏）的实测高度。
    /// 用于把「回到最新」按钮上推到浮层之上（基座消息区现在铺满整高）。
    @State private var composerHeight: CGFloat = 0

    /// 中栏整高（含被底部浮层覆盖的部分），供子任务面板封顶。
    @State private var contentHeight: CGFloat = 0

    let baseURL: String
    let token: String
    /// 是否当前会话 tab：空格按住只让当前 tab 响应（主窗口 true，PiP 副本 false）
    var isCurrentTab: Bool = true
    /// 是否正在「退出语音」触发的 flush：这一轮按普通消息提交，不朗读
    @State private var exitingVoice = false
    /// macOS 空格监听 / 失焦兜底
    #if os(macOS)
    @State private var spaceKeyMonitor: Any?
    @State private var resignActiveObserver: NSObjectProtocol?
    /// 本视图所在窗口（空格监听只接管发给它的按键）
    @State private var hostWindow: NSWindow?
    #endif
    @ObservedObject private var artifactOpener = ArtifactOpenCoordinator.shared

    var body: some View {
#if os(iOS)
        ZStack(alignment: .top) {
            GlassBackground()
            content
            topBarCover
        }
        // iOS 没有右栏：产物用整屏 sheet 展示（同一个 HTMLArtifactView）
        .sheet(item: $artifactOpener.presented) { page in
            NavigationStack {
                HTMLArtifactView(html: page.html)
                    .ignoresSafeArea()
                    .navigationTitle(page.title)
                    .navigationBarTitleDisplayMode(.inline)
                    .toolbar {
                        ToolbarItem(placement: .cancellationAction) {
                            Button("完成") { artifactOpener.presented = nil }
                        }
                    }
            }
        }
        .onAppear {
            artifactOpener.configure(baseURL: baseURL, token: token)
        }
#else
        content
            .background(HostWindowReader { window in
                if window !== hostWindow { hostWindow = window }
            })
            .confirmationDialog("开启「完全信任」？", isPresented: $showTrustEnableConfirm) {
                Button("开启完全信任", role: .destructive) {
                    trustStore.fullTrust = true
                    ToastCenter.shared.show("已进入完全信任：fs 写 / shell 写等高危工具也不再确认")
                }
                Button("取消", role: .cancel) {}
            } message: {
                Text("本地工具不再弹确认：fs 写文件、shell 写/执行命令（含高危）都直接执行。\n只对已授权目录生效——范围外目录不会自动授权，会直接报错让你去「设置 → 文件访问授权」添加。\n硬拦截（白名单外命令、高危路径、敏感文件名）与防注入确认仍然生效。\n开关对本机该 macOS 用户下的所有 app 账号生效。")
            }
#endif
    }

#if os(macOS)
    /// 音波条状态：说 > 想 > 听。「想」包含 TTS 合成期（`isPreparingSpeech`），
    /// 否则回复流刚结束、音频还没到时会先闪回「听」。
    private var voiceWavePhase: VoiceWaveLine.Phase {
        if voice.isSpeaking { return .speaking }
        if viewModel.isStreaming || voice.isPreparingSpeech || voice.isHoldTranscribing { return .thinking }
        return .listening
    }

    /// 句级朗读进度：只给「正在念的那条」消息（流式气泡或最后一条 assistant）
    private var voiceSpeechProgress: SpeechProgress? {
        guard voice.isSpeaking || voice.isPreparingSpeech, !voice.speechSentences.isEmpty else { return nil }
        return SpeechProgress(sentences: voice.speechSentences, index: voice.speechIndex)
    }

    /// 语音会话期间顶替输入栏文本行的音波条（两档共用；iOS 没有连续对话入口，恒为 nil）。
    private var voiceStripSlot: AnyView? {
        guard voice.voiceBarActive else { return nil }
        return AnyView(VoiceWaveLine(phase: voiceWavePhase, level: { voice.waveLevel },
                                     onInterrupt: { voice.stopSpeaking() }))
    }
#else
    private var voiceStripSlot: AnyView? { nil }
    /// iOS 没有连续对话 / 句级朗读进度（TTS 只在 macOS 侧接入）
    private var voiceSpeechProgress: SpeechProgress? { nil }
#endif

    private var content: some View {
        // 消息列表铺满中栏整高；底部浮层栈经 safeAreaInset 悬浮其上（见 bottomComposer）。
        ZStack(alignment: .bottom) {
            MessageListView(
                messages: viewModel.messages,
                pendingSends: viewModel.pendingSends,
                pendingPaused: viewModel.pendingPaused,
                onDeletePending: { viewModel.removePendingSend(id: $0) },
                onResumePending: { viewModel.resumePendingQueue() },
                isStreaming: viewModel.isStreaming,
                isInitialLoading: viewModel.isInitialLoading,
                errorMessage: viewModel.errorMessage,
                conversationId: viewModel.conversationId,
                statusMessage: viewModel.statusMessage,
                thoughtMessage: viewModel.thoughtMessage,
                thoughtDone: viewModel.thoughtDone,
                toolQueue: viewModel.toolQueue,
                verifyStatus: viewModel.verifyStatus,
                onOpenDocument: { viewModel.onOpenDocument?($0) },
                onOpenArtifact: { id, title in viewModel.onOpenArtifact?(id, title) },
                hasMoreBefore: viewModel.hasMoreBefore,
                isLoadingMore: viewModel.isLoadingMore,
                loadMoreError: viewModel.loadMoreError,
                onLoadMore: { direction in Task { await viewModel.loadMore(direction: direction) } },
                onRetry: {
                    guard let cid = viewModel.conversationId else { return }
                    Task { await viewModel.loadConversation(id: cid, title: viewModel.conversationTitle ?? "", mode: viewModel.mode, preserveTransientState: true) } // 重试保持当前模式/临时态
                },
                baseURL: baseURL,
                token: token,
                speech: voiceSpeechProgress,
                scrollPosition: $scrollPosition,
                showScrollToBottom: $showScrollToBottom
            )
            if showScrollToBottom && !viewModel.messages.isEmpty {
                Button {
                    withAnimation(.easeOut(duration: 0.3)) {
                        scrollPosition.scrollTo(edge: .top)
                    }
                } label: {
                    Image(systemName: "chevron.down")
                        .font(.title3)
                        .foregroundColor(.primary)
                        .frame(width: 36, height: 36)
                        .background(.ultraThinMaterial, in: Circle())
#if os(iOS)
                        .shadow(color: .black.opacity(0.15), radius: 6, y: 2)
#endif
                }
                .buttonStyle(.plain)
                .animation(.easeInOut(duration: 0.2), value: showScrollToBottom)
                .transition(.scale.combined(with: .opacity))
                .padding(.bottom, composerHeight + 12)
            }
        }
        // 底部浮层栈（AskUserBar / 计划便签 / 子任务面板 / L3 确认条 / 输入栏）改为 safeAreaInset：
        // 浮在消息内容之上、不占消息区布局，滚动时内容从浮层下方穿过；safeAreaInset 同时给
        // 滚动内容留出与栈等高的安全区，滚到底时最后一条完整停在输入栏上方。
        .safeAreaInset(edge: .bottom, spacing: 0) {
            bottomComposer
        }
        // 量中栏整高，供子任务面板封顶（展开态内联在底部栈里，太高会把消息区挤没）。
        .onGeometryChange(for: CGFloat.self) { $0.size.height } action: { contentHeight = $0 }
        .onChange(of: viewModel.scrollToBottomRequested) { _, requested in
            guard requested else { return }
            withAnimation(.easeOut(duration: 0.3)) {
                scrollPosition.scrollTo(edge: .top)
            }
            viewModel.scrollToBottomRequested = false
        }
        .onChange(of: isEditing) { _, new in
            viewModel.isEditing = new
        }
        // ⌘V 粘贴的图（剪贴板在 AppDelegate 读，经 VM 转手）：只由"正在输入的那个"
        // ChatView 收下（PiP 与主视图共用同一个 VM，会同时收到 onChange）
        .onChange(of: viewModel.pastedImages) { _, datas in
            guard inputFocused, !datas.isEmpty else { return }
            viewModel.pastedImages = []
            Task {
                await appendPickedItems(datas.map { (filename: "粘贴的图片", data: $0) })
            }
        }
        // 智能体选择器：页面出现/回到会话（如从智能体页切回，此时可能刚创建第一个
        // 智能体）时拉取列表；会话切换（含空 tab 转正）时重新拉取激活项。
        .onAppear {
#if os(macOS)
            Task { await reloadAgentPicker() }
            installSpaceHoldMonitor()
#endif
            holdVoice()
            wireVoice()
        }
        // 换会话：macOS 换 tab 只是换 viewModel（ChatView 的身份不变、onAppear 不会再跑），
        // 不重接的话语音还绑在上一个会话的 VM 上，分句会提交进旧会话（docs/bugs/056）。
        // 语音服务随 tab 走（在 VM 上）：要先把旧 tab 的服务收干净，再登记新服务。
        .onChange(of: ObjectIdentifier(viewModel)) { _, _ in
            let previous = wiredViewModel?.voice
            unwireVoice()
            if voiceHolding {
                voiceHolding = false
                previous?.detachView()
            }
            previous?.stopSpeaking()
            previous?.cancel()
            holdVoice()
            wireVoice()
        }
        .onDisappear {
#if os(macOS)
            removeSpaceHoldMonitor()
#endif
            releaseVoiceIfLast()
        }
        .onReceive(NotificationCenter.default.publisher(for: .voicePreviewWillStart)) { _ in
            // 设置页试听前先停朗读，别让两路音频叠在一起。
            voice.stopSpeaking()
        }
        .onChange(of: viewModel.conversationId) { _, _ in
#if os(macOS)
            Task { await reloadAgentPicker() }
#endif
        }
#if os(macOS)
        // 选图：PNG/JPEG/WebP/GIF（服务端还会做一次魔数校验）
        .fileImporter(
            isPresented: $showFileImporter,
            allowedContentTypes: [.png, .jpeg, .webP, .gif],
            allowsMultipleSelection: true
        ) { result in
            switch result {
            case .success(let urls): handlePickedFiles(urls)
            case .failure(let error): LogBuffer.shared.warning("ChatView.fileImporter: \(error)")
            }
        }
#endif
    }

    /// 本视图出现：登记一个语音服务使用者
    private func holdVoice() {
        guard !voiceHolding else { return }
        voiceHolding = true
        voice.attachView()
    }

    /// 本视图离开：只有没有别的 ChatView（PiP ⇄ 中栏）接着用同一个语音服务时才收尾。
    /// 延后 200ms 复查一次 —— 进出 PiP 时新旧视图的 onAppear / onDisappear 先后 SwiftUI 不保证，
    /// 当场就停会把正在朗读的回复掐断（见 docs/bugs/062）。
    /// shortcut: 200ms 是固定超时（新视图若晚于它 onAppear 仍会误停），真机出现误停再改成
    /// 「等一次 attach / 200ms 兜底」。
    private func releaseVoiceIfLast() {
        guard voiceHolding else { return }
        voiceHolding = false
        guard voice.detachView() else { return }
        let service = voice
        Task { @MainActor in
            try? await Task.sleep(for: .milliseconds(200))
            guard service.viewHolders == 0 else { return }
            service.cancel()
            service.stopSpeaking()
            unwireVoice()
        }
    }

    /// 把语音链路接到「当前」会话的 viewModel。
    /// 必须在 `onAppear` 和换 `viewModel` 时都跑一遍：macOS 只渲染 currentTab 的那一个 ChatView，
    /// 换 tab 只是换参数、视图身份不变，这里装的闭包会跨会话存活，闭包捕获的是当时的 viewModel
    /// —— 不重接就会把语音提交进上一个会话（docs/bugs/056）。
    /// 语音服务本身随 tab 挂在 VM 上（`viewModel.voice`），换 tab 时要连带换服务（见 onChange）。
    private func wireVoice() {
        // 旧会话的朗读回调先摘掉：切走时它可能还在流式，留着会把旧会话的正文念出来
        unwireVoice()
        wiredViewModel = viewModel

        voice.onFinalTranscript = { text in
            viewModel.draftText = text
            viewModel.pendingVoiceReply = true
            inputFocused = true
        }
        // C3：播报期间用户开口 → 立刻停生成（朗读与门控已在 service 里处理）
        voice.onBargeIn = {
            // 打断的主场景是「本轮已流完、TTS 正在念」：这时发 stop 会误杀下一轮刚注册的
            // stop channel（还会 killAll 本地 shell），交给 stopSpeaking 停朗读就够
            LogBuffer.shared.diag("[voice] barge-in: stop streaming=\(viewModel.isStreaming)")
            if viewModel.isStreaming {
                viewModel.stopStreaming()
            }
        }
        // C 模式：每句判停就直接提交一轮；上一轮还在跑就先停掉，避免两个 loop 交叉写库
        voice.onSentence = { text in
            LogBuffer.shared.diag("[voice] submit sentence: chars=\(text.count) " +
                                  "streaming=\(viewModel.isStreaming) cid=\(viewModel.conversationId ?? "-")")
            // 无条件停朗读：上一轮已流完但还在念时，门还关着，不停就会把这句一起吞掉
            voice.stopSpeaking()
            if viewModel.isStreaming {
                // 不发 /api/chat/stop：服务端 Register 会取消旧 loop，迟到的 stop 会误杀新轮
                viewModel.stopStreaming(notifyServer: false, clearPending: false)
            }
            // 退出语音时 flush 的话按普通消息提交（exitingVoice），不朗读
            viewModel.sendMessage(text, baseURL: baseURL, token: token,
                                  agentId: selectedAgentID ?? agentId,
                                  speakReply: !exitingVoice)
        }
        // 长按说话：一次松手 = 一轮。语音条里按的不朗读；全局热键按的朗读回复
        voice.onHoldSentence = { text, speakReply in
            LogBuffer.shared.diag("[voice] submit hold sentence: chars=\(text.count) " +
                                  "streaming=\(viewModel.isStreaming)")
            // 切档瞬间上一轮连续档的朗读可能还在播：先停掉，别叠音
            voice.stopSpeaking()
            if viewModel.isStreaming {
                viewModel.stopStreaming(notifyServer: false, clearPending: false)
            }
            viewModel.sendMessage(text, baseURL: baseURL, token: token,
                                  agentId: selectedAgentID ?? agentId,
                                  speakReply: speakReply, voiceMode: true)
        }
        // 空闲/会话计时器要知道「正在回答」，否则会在 AI 说话时把会话收掉
        voice.isChatBusy = { [weak viewModel] in viewModel?.isStreaming ?? false }
        // 正文边到边切句合成：首句一到就出声，不再等整段回复 + 整段 mp3
        viewModel.onAssistantReplyDelta = { text in
            voice.appendReplyDelta(text, baseURL: baseURL, token: token)
        }
        viewModel.onAssistantReplyDone = { text in
            LogBuffer.shared.diag("[voice] assistant reply done, tts chars=\(text.count)")
            voice.endReplyStream(text, baseURL: baseURL, token: token)
        }
        viewModel.onAssistantReplyAborted = {
            voice.stopSpeaking()
        }
        viewModel.replyOwner = ObjectIdentifier(voice)
        Task { await voice.refresh(baseURL: baseURL, token: token) }
#if os(macOS)
        wireVoiceHotKey()
#endif
    }

#if os(macOS)
    /// 全局「按住左右 ⌘」的**视图侧**接线：热键本体在 `VoiceHotKeyController`（app 级常驻，
    /// 关窗/ PiP 收起也活着）。这里只交出三件事：
    /// - 语音条开着时把热键让给视图那套（连续档按住暂存、松手整段提交，行为与以前一致）
    /// - 浮层「说」态要知道视图那套是不是在朗读
    /// - 开录前先停掉视图那套的朗读，否则录进去的是 TTS
    private func wireVoiceHotKey() {
        AppShortcutBridge.shared.onVoicePress = { [weak voice] in
            guard let voice else { return }
            Task {
                switch await voice.hotKeyPress(baseURL: baseURL, token: token) {
                case .started: NSSound(named: "Tink")?.play()
                case .failed: NSSound.beep()
                case .ignored: break
                }
            }
        }
        AppShortcutBridge.shared.onVoiceRelease = { [weak voice] in
            guard let voice else { return }
            Task {
                if !(await voice.hotKeyRelease()) { NSSound.beep() }
            }
        }
        AppShortcutBridge.shared.voiceBarActive = { [weak voice] in voice?.voiceBarActive ?? false }
        AppShortcutBridge.shared.voiceIsSpeaking = { [weak voice] in voice?.isSpeaking ?? false }
        AppShortcutBridge.shared.stopVoicePlayback = { [weak voice] in voice?.stopSpeaking() }
        AppShortcutBridge.shared.voiceHotKeyOwner = ObjectIdentifier(voice)
    }
#endif

    /// 摘掉上一轮接线：视图消失后同一 VM 的流式尾巴不能继续把正文念出来
    /// （`voice` 已被 `cancel`，但闭包还挂在 VM 上会重新开一轮朗读）。
    /// 只摘自己占着的槽：同一个 VM 只有一份 `voice`（随 tab 挂在 VM 上），中栏与 PiP 共用它；
    /// 换 tab 时 `wiredViewModel` 才是旧的那份，所以身份从它取。
    private func unwireVoice() {
#if os(macOS)
        // 热键槽只摘自己占的（同一时刻只有一份 voice 接线）
        if let vm = wiredViewModel, AppShortcutBridge.shared.voiceHotKeyOwner == ObjectIdentifier(vm.voice) {
            AppShortcutBridge.shared.onVoicePress = nil
            AppShortcutBridge.shared.onVoiceRelease = nil
            AppShortcutBridge.shared.voiceBarActive = nil
            AppShortcutBridge.shared.voiceIsSpeaking = nil
            AppShortcutBridge.shared.stopVoicePlayback = nil
            AppShortcutBridge.shared.voiceHotKeyOwner = nil
        }
#endif
        // 身份从 `wiredViewModel` 取而不是从 `viewModel`：换 tab 时 `viewModel` 已经是新的了
        guard let vm = wiredViewModel, vm.replyOwner == ObjectIdentifier(vm.voice) else {
            wiredViewModel = nil
            return
        }
        vm.onAssistantReplyDelta = nil
        vm.onAssistantReplyDone = nil
        vm.onAssistantReplyAborted = nil
        vm.replyOwner = nil
        wiredViewModel = nil
    }

    /// 底部浮层栈：AskUserBar / 子任务状态条 / L3 确认条 / 输入栏。
    /// 作为消息区的 bottom safeAreaInset —— 浮在消息内容之上、不占消息区布局，
    /// 同时给滚动内容留出与栈等高的安全区（滚到底时最后一条完整停在输入栏上方）。
    /// 高度经 onGeometryChange 回传 composerHeight，供「回到最新」按钮 / 子任务浮层上推。
    private var bottomComposer: some View {
        VStack(spacing: 0) {
            // 子任务用户介入条（ask_user）：输入栏上方，独立于 toolQueue 不被文本流清理。
            // 多个请求堆叠折叠，逐条回答/已处理/跳过；早于 L3 确认条展示（它是待执行工具的确认，ask_user 是待办介入）。
            // 宽度约束与 InputBar 完全一致（maxWidth 720 + trailing + chatHorizontalPadding），保证同宽。
            if !viewModel.pendingUserRequests.isEmpty {
                AskUserBar(requests: viewModel.pendingUserRequests) { callID, reply in
                    viewModel.resolveUserRequest(callID: callID, reply: reply)
                }
                .frame(maxWidth: 720, alignment: .trailing)
                .padding(.horizontal, Layout.chatHorizontalPadding)
                .frame(maxWidth: .infinity, alignment: .center)
                .padding(.bottom, 8)
            }

            // 子任务条上方一行：L3 待确认条（居中偏左）+ 执行计划便签（靠右）并排；
            // 窄窗由 ComposerTopRow 内的 ViewThatFits 自动回退成竖排（计划在上、待确认在下）。
            composerTopRow

            // 子任务活动面板：内联在底部栈里（计划便签与输入栏之间）。
            // 折叠态=一行状态条；展开态=详情面板（按内容自适应，太高则封顶内部滚动）。
            // 宽度约束与 InputBar 完全一致（maxWidth 720 + chatHorizontalPadding），保证同宽。
            if !viewModel.subagentSessions.isEmpty {
                if viewModel.subagentPanelExpanded {
                    SubagentDetailPanel(
                        sessions: viewModel.subagentSessions,
                        onToggleSession: { viewModel.toggleSubagentExpand($0) },
                        onCollapse: { collapseSubagentPanel() }
                    )
                    .frame(maxWidth: 720, alignment: .center)
                    .frame(maxHeight: subagentPanelMaxHeight)
                    .padding(.horizontal, Layout.chatHorizontalPadding)
                    .frame(maxWidth: .infinity, alignment: .center)
                    .padding(.bottom, 6)
                    .transition(.move(edge: .bottom).combined(with: .opacity))
                } else {
                    SubagentStatusBar(
                        sessions: viewModel.subagentSessions,
                        expanded: viewModel.subagentPanelExpanded,
                        onToggle: {
                            withAnimation(.easeOut(duration: 0.25)) { viewModel.toggleSubagentPanel() }
                        }
                    )
                    .frame(maxWidth: 720, alignment: .trailing)
                    .padding(.horizontal, Layout.chatHorizontalPadding)
                    .frame(maxWidth: .infinity, alignment: .center)
                    .padding(.bottom, 6)
                    .transition(.move(edge: .bottom).combined(with: .opacity))
                }
            }

            // L3 待确认条与执行计划便签：见上方 composerTopRow（已合并为子任务条上方的一行）。

            // 输入栏（驾驶舱）：按钮通过 builder 注入，收起/展开双态
            inputBar
        }
        .onGeometryChange(for: CGFloat.self) { $0.size.height } action: { composerHeight = $0 }
    }

    /// 子任务面板展开态的高度上限：中栏 60%，留出消息区；窗口小时也保底 240。
    private var subagentPanelMaxHeight: CGFloat {
        max(contentHeight * 0.6, 240)
    }

    private func collapseSubagentPanel() {
        withAnimation(.easeOut(duration: 0.25)) { viewModel.subagentPanelExpanded = false }
    }

    /// 输入栏容器描边/光晕色：计划模式橙，其余默认强调色。
    private var inputBarTint: Color {
        switch viewModel.chatMode {
        case .normal: return .orange
        case .off: return Color("appAccent")
        }
    }

    /// 发送草稿：校验非空 → 文字与待发图一起清空 → 带图先上传 → 发消息 → 滚到底部。
    /// 文字和图片同一帧一起清掉（图片只在上传期间不可能留下、避免「文字先走、图片后走」的时差）；
    /// 上传失败时两者一起还原。
    private func sendDraft() {
        let hasText = !viewModel.draftText.trimmingCharacters(in: .whitespaces).isEmpty
        guard hasText || !viewModel.draftAttachments.isEmpty else { return }

        dismissKeyboardToken = UUID()
        viewModel.sendDraft(baseURL: baseURL, token: token, agentId: selectedAgentID ?? agentId)
        withAnimation(.easeOut(duration: 0.3)) {
            scrollPosition.scrollTo(edge: .top)
        }
    }

    /// 输入框焦点变化（InputBar 上报）：用于判断 ⌘V 粘贴该由哪个 ChatView 收下
    private func setInputFocused(_ focused: Bool) {
        inputFocused = focused
    }

    /// 输入栏（驾驶舱）：按钮通过 builder 注入，收起/展开双态。
    /// 独立成属性：整个调用表达式很大，内联进 content 会让类型检查超时
    /// （"unable to type-check this expression in reasonable time"）。
    private var inputBar: some View {
        InputBar(
            text: $viewModel.draftText,
            isEditing: $isEditing,
            isStreaming: viewModel.isStreaming,
            dismissKeyboardToken: dismissKeyboardToken,
            containerTint: inputBarTint,
            hasAttachments: !viewModel.draftAttachments.isEmpty,
            onFocusChange: setInputFocused,
            onClearAll: {
                viewModel.draftText = ""
                viewModel.draftAttachments.removeAll()
                // 语音标记跟着那句识别文本走：清空输入后手打的第一条不能还按语音轮处理
                viewModel.pendingVoiceReply = false
            },
            onSend: sendDraft,
            onStop: {
                voice.stopSpeaking()
                viewModel.stopStreaming()
            },
            onToggleMode: {
                Task { await viewModel.cycleMode() }
            },
            voiceStrip: voiceStripSlot,
            statusRow: inputBarStatusRow,
            statusRowExtras: inputBarExtrasRow,
            trailingActions: inputBarTrailingActions,
            bottomLeading: inputBarBottomLeading,
            attachmentTray: inputBarAttachmentTray
        )
        .layoutPriority(1)
        .animation(.easeOut(duration: 0.2), value: viewModel.planExpanded)
        .animation(.easeOut(duration: 0.18), value: inputBarExtrasExpanded)
    }

    /// 空格：连续档按住/松开暂存，录音档按一下开始 / 再按一下发送；Esc 取消本次录音。
    /// 只在当前 tab、语音条开着、且焦点不在文本输入时接管；接管时吞掉事件，避免空格滚动页面。
    #if os(macOS)
    private func installSpaceHoldMonitor() {
        guard spaceKeyMonitor == nil else { return }
        spaceKeyMonitor = NSEvent.addLocalMonitorForEvents(matching: [.keyDown, .keyUp]) { event in
            var swallow = false
            MainActor.assumeIsolated {
                guard event.window === hostWindow,
                      isCurrentTab, voice.voiceBarActive else { return }
                // 切档过渡期还没有会话：空格既不暂存也不吞，交给响应链
                if voice.isSwitchingMode { return }
                let holdMode = voice.mode == .hold

                // Esc：录音中丢弃本次（移动端有上滑，桌面端靠它）
                if event.type == .keyDown, event.keyCode == 53 {
                    if voice.isHoldRecording {
                        swallow = true
                        Task { await voice.finishHoldTalk(cancelled: true) }
                    }
                    return
                }

                guard event.keyCode == 49 else { return }

                if holdMode {
                    // 焦点在文本框时完全放行（长按空格要能连续输入）
                    if let responder = NSApp.keyWindow?.firstResponder,
                       responder is NSText || responder is NSTextView {
                        return
                    }
                    // 两态按钮：空格按一下开始、再按一下发送。抬手与自动重复一律吞掉，别漏给响应链。
                    if event.isARepeat || event.type == .keyUp {
                        swallow = true
                        return
                    }
                    swallow = true
                    if voice.isHoldRecording {
                        Task { await voice.finishHoldTalk(cancelled: false) }
                    } else {
                        Task { await voice.startHoldTalk(baseURL: baseURL, token: token) }
                    }
                    return
                }

                // 连续档：按住暂存、松手提交
                if event.isARepeat {
                    if voice.isHolding { swallow = true }
                    return
                }
                if event.type == .keyUp {
                    if voice.isHolding {
                        swallow = true
                        voice.endHold()
                    }
                    return
                }
                if let responder = NSApp.keyWindow?.firstResponder,
                   responder is NSText || responder is NSTextView {
                    return
                }
                voice.startHold()
                swallow = true
            }
            return swallow ? nil : event
        }
        // 切走窗口会丢 keyUp：强制收 hold，避免回来还卡在暂存态
        // （录音档是两态按钮，切窗口不取消录音——录到一半切个窗口不该丢）
        resignActiveObserver = NotificationCenter.default.addObserver(
            forName: NSApplication.didResignActiveNotification, object: nil, queue: .main
        ) { _ in
            MainActor.assumeIsolated {
                if voice.isHolding { voice.endHold() }
            }
        }
    }

    private func removeSpaceHoldMonitor() {
        if let spaceKeyMonitor { NSEvent.removeMonitor(spaceKeyMonitor) }
        spaceKeyMonitor = nil
        if let resignActiveObserver { NotificationCenter.default.removeObserver(resignActiveObserver) }
        resignActiveObserver = nil
    }
    #endif

    /// InputBar 状态行：常驻按钮 + 更多操作 chevron。哪些按钮常驻由折叠配置决定。
    /// 独立成方法而不是内联闭包：整个 InputBar 调用表达式已顶到类型检查预算上限。
    @ViewBuilder
    private func inputBarStatusRow(_ ctx: InputBarContext) -> some View {
        #if os(macOS)
        if voice.voiceBarActive {
            // 胶囊恒图标（62pt），整行约 214pt，最窄内容区 304pt 富余，不需要降级分支。
            voiceStatusRow(ctx)
        } else {
            HStack(spacing: 4) {
                ForEach(InputBarButtonID.allCases.filter { !inputBarLayout.isCollapsed($0) || isPinnedInStatusRow($0) }, id: \.self) { id in
                    inputBarButton(id, ctx: ctx)
                }
                // 连续对话紧挨「更多操作」左侧（原「语音输入」按钮已按用户要求删除）
                if voice.isAvailable {
                    voiceContinuousButton(ctx)
                }
                // 更多操作：有按钮被收起时才提供 chevron（否则全常驻，无展开行）。
                if !collapsedInExtras.isEmpty {
                    ExpandPill(context: ctx, isExpanded: inputBarExtrasExpanded) {
                        toggleInputBarExtras()
                    }
                }
            }
        }
        #endif
    }

    /// 语音模式的状态行（两档共用）：录音档多一个取消键与麦克风（两态：开始/发送），
    /// 胶囊只切档，退出语音恒在胶囊右侧（生成中也不收起），最右留一个 34pt 停止键槽位。
    /// 两个常驻空槽（取消、停止）让这一行在任何状态下宽度恒定：这一行是居中的，
    /// 增删按钮会让整行横跳，录音中点发送可能落空。连续档左侧两个键位也按同宽空槽占住，
    /// 否则切档时胶囊会左右挪位。
    /// 红色只表示停止 / 取消 / 挂断；发送键用主输入栏同款绿色 arrow.up。
    @ViewBuilder
    private func voiceStatusRow(_ ctx: InputBarContext) -> some View {
        HStack(spacing: 4) {
            if voice.mode == .hold {
                // 取消键不占布局会飘到胶囊外（窄窗越界），占位又会让麦克风从鼠标底下挪走，
                // 所以给一个常驻空槽：只有里面的图标随录音状态出现/消失。
                if voice.isHoldRecording {
                    IconButton(
                        visibility: .always,
                        context: ctx,
                        symbol: "xmark",
                        tint: .red,
                        tooltip: "取消本次发送（Esc）",
                        hoverBackground: Color.red.opacity(0.12)
                    ) {
                        Task { await voice.finishHoldTalk(cancelled: true) }
                    }
                } else {
                    Color.clear.frame(width: 34, height: 34)
                }
                IconButton(
                    visibility: .always,
                    context: ctx,
                    symbol: voice.isHoldRecording ? "arrow.up" : "mic",
                    tint: Color("appAccent"),
                    isEnabled: !voice.isHoldTranscribing,
                    tooltip: voice.isHoldRecording
                        ? "点击发送（Esc 取消）"
                        : "点击开始录音（再点发送，Esc 取消）",
                    pulsing: voice.isHoldRecording,
                    hoverBackground: Color("appAccent").opacity(0.12)
                ) {
                    if voice.isHoldRecording {
                        Task { await voice.finishHoldTalk(cancelled: false) }
                    } else {
                        Task { await voice.startHoldTalk(baseURL: baseURL, token: token) }
                    }
                }
            } else {
                // 连续档没有左侧两键，但留同宽空槽：这一行居中，少了它会整行横跳，胶囊跟着挪位。
                Color.clear.frame(width: 34, height: 34)
                Color.clear.frame(width: 34, height: 34)
            }
            VoiceModeCapsule(mode: voice.mode) { next in
                voice.switchMode(next, baseURL: baseURL, token: token)
            }
            voiceContinuousButton(ctx)
            if viewModel.isStreaming {
                IconButton(
                    visibility: .always,
                    context: ctx,
                    symbol: "stop.fill",
                    tint: .red,
                    tooltip: "停止生成",
                    hoverBackground: .red.opacity(0.12),
                    action: {
                        voice.stopSpeaking()
                        viewModel.stopStreaming()
                    }
                )
            } else {
                Color.clear.frame(width: 34, height: 34)
            }
        }
    }

    /// InputBar 折叠的更多操作行：展开时出现在按钮行上方，同胶囊内右对齐——
    /// 参与布局，宽度受胶囊约束，窄窗不会戳出框。
    @ViewBuilder
    private func inputBarExtrasRow(_ ctx: InputBarContext) -> some View {
        #if os(macOS)
        if !voice.voiceBarActive && inputBarExtrasExpanded && !collapsedInExtras.isEmpty {
            HStack(spacing: 4) {
                Spacer(minLength: 0)
                ForEach(collapsedInExtras, id: \.self) { id in
                    inputBarButton(id, ctx: ctx)
                }
            }
            .transition(.move(edge: .bottom).combined(with: .opacity))
        }
        #endif
    }

    /// 完全信任开着时强制常驻：它是安全状态指示，不能因为按钮被收起就看不见。
    private func isPinnedInStatusRow(_ id: InputBarButtonID) -> Bool {
        id == .trust && trustStore.fullTrust
    }

    /// 真正收进「更多操作」行的按钮（被强制常驻的不算，否则会出现两处入口/空展开行）。
    private var collapsedInExtras: [InputBarButtonID] {
        InputBarButtonID.allCases.filter { inputBarLayout.isCollapsed($0) && !isPinnedInStatusRow($0) }
    }

    /// 单个输入栏按钮：按 id 构造对应控件，并挂右键菜单切换常驻/收起。
    /// 被钉在常驻行的按钮（完全信任开着）不挂菜单——「收起」对它无效，是个点了没反应的哑操作。
    @ViewBuilder
    private func inputBarButton(_ id: InputBarButtonID, ctx: InputBarContext) -> some View {
        if isPinnedInStatusRow(id) {
            inputBarButtonContent(id, ctx: ctx)
        } else {
            inputBarButtonContent(id, ctx: ctx)
                .contextMenu {
                    Button(inputBarLayout.isCollapsed(id) ? "常驻" : "收起") {
                        withAnimation(.easeOut(duration: 0.18)) { inputBarLayout.toggle(id) }
                    }
                }
        }
    }

    /// 按钮本体（不含右键菜单）。
    /// 2026-10 下架：`.model` / `.brief` / `.handoff` 三个分支已不可达（对应 pill 改 `visibility: .hidden`，
    /// id 也从 `InputBarButtonID.allCases` 移除）——分支保留，恢复只需把 id 加回 allCases + visibility 改回 `.always`。
    @ViewBuilder
    private func inputBarButtonContent(_ id: InputBarButtonID, ctx: InputBarContext) -> some View {
        switch id {
        case .mode:
            // 计划模式：高频使用。两档循环（关 → 计划）。
            ModePill(context: ctx, mode: viewModel.chatMode) {
                Task { await viewModel.cycleMode() }
            }
        case .agent:
            // 智能体选择器：cpu 图标与侧边栏一致。
            // 仅当已确认用户存在智能体时才展示（无智能体时隐藏，避免无意义按钮）。
            if hasLoadedAgents && !pickerAgents.isEmpty {
                AgentPickerButton(
                    context: ctx,
                    activeAgentID: selectedAgentID,
                    activeAgentName: selectedAgentID.flatMap { selID in
                        pickerAgents.first(where: { $0.id == selID })?.name
                    },
                    agents: displayAgents,
                    hasMore: pickerHasMore,
                    isLoadingMore: isLoadingPicker,
                    onSelect: { agent in
                        Task { await switchAgent(agent) }
                    },
                    onLoadMore: {
                        Task { await loadAgentPickerPage(cursor: pickerCursor) }
                    }
                )
            }
        case .trust:
            // 完全信任（全局持久化）：开→点即关；关→先二次确认再开。
            TrustPill(context: ctx, isOn: trustStore.fullTrust) {
                if trustStore.fullTrust {
                    trustStore.fullTrust = false
                    ToastCenter.shared.show("已关闭完全信任：fs 写 / shell 写仍会确认")
                } else {
                    showTrustEnableConfirm = true
                }
                resetExtrasAutoCollapse()
            }
        case .model:
            ModelPill(context: ctx, isDeep: viewModel.deepMode) {
                viewModel.toggleDeepMode()
                resetExtrasAutoCollapse()
            }
        case .brief:
            BriefPill(context: ctx, isBrief: viewModel.briefAnswer) {
                viewModel.toggleBriefAnswer()
                resetExtrasAutoCollapse()
            }
        case .ephemeral:
            // 无痕：空白 tab 只改本地意图（首条消息落库）；已有会话 PATCH 落库。
            // 默认会话禁止无痕（服务端 400 兜底 + toast），此处不预判。
            EphemeralPill(
                context: ctx,
                isOn: viewModel.isEphemeral,
                isBlank: (viewModel.conversationId ?? "").isEmpty,
                isEnabled: !viewModel.isTogglingEphemeral
            ) {
                viewModel.toggleEphemeral()
                resetExtrasAutoCollapse()
            }
        case .handoff:
            // 会话接力：整理成文档并开新会话续接。计划模式不能写文档，
            // 故计划档下置灰（tooltip 说明原因），非流式、非空会话才可用。
            HandoffPill(
                context: ctx,
                isEnabled: !viewModel.isStreaming
                    && !(viewModel.conversationId ?? "").isEmpty
                    && !viewModel.chatMode.isPlanLike,
                isPlanLike: viewModel.chatMode.isPlanLike
            ) {
                viewModel.beginHandoff(baseURL: baseURL, token: token, agentId: selectedAgentID ?? agentId)
            }
        case .attach:
            // 附件：选本地图片（发送时随消息一起上传）
            IconButton(
                visibility: .always,
                context: ctx,
                symbol: "paperclip",
                tint: Color("appAccent"),
                isEnabled: !viewModel.isUploadingAttachments,
                tooltip: "添加图片",
                action: { showFileImporter = true }
            )
        }
    }

    /// 展开/收起折叠的更多操作；展开时启动 12s 无操作自动收起。
    private func toggleInputBarExtras() {
        inputBarExtrasExpanded.toggle()
        if inputBarExtrasExpanded {
            resetExtrasAutoCollapse()
        } else {
            extrasCollapseTask?.cancel()
        }
    }

    /// 重置自动收起计时（点胶囊后重新计时）。
    private func resetExtrasAutoCollapse() {
        extrasCollapseTask?.cancel()
        guard inputBarExtrasExpanded else { return }
        extrasCollapseTask = Task {
            try? await Task.sleep(for: .seconds(12))
            guard !Task.isCancelled else { return }
            withAnimation(.easeOut(duration: 0.18)) { inputBarExtrasExpanded = false }
        }
    }

    /// 子任务条上方一行：把 L3 待确认条（前）与执行计划便签（后）并排；放不下自动竖排。
    /// 独立成属性：InputBar 调用表达式已在类型检查预算边缘。
    @ViewBuilder
    private var composerTopRow: some View {
        let hasPlan = viewModel.planState == .active && !viewModel.planDismissed
        if let conf = viewModel.pendingToolConfirmations.first {
            ComposerTopRow(leading: { pendingConfirmationBlock(conf) }, trailing: { planNote })
        } else if hasPlan {
            ComposerTopRow(leading: { EmptyView() }, trailing: { planNote })
        }
    }

    /// L3 待确认块：队列计数提示 + 待确认条本体。
    @ViewBuilder
    private func pendingConfirmationBlock(_ conf: ToolConfirmation) -> some View {
        VStack(spacing: 4) {
            if viewModel.pendingToolConfirmations.count > 1 {
                Text("另有 \(viewModel.pendingToolConfirmations.count - 1) 条待确认")
                    .font(.caption2)
                    .foregroundColor(.secondary)
            }
            toolConfirmationBar(conf)
        }
    }

    /// 执行计划便签本体（右对齐、宽度封顶）。外层摆放交给 ComposerTopRow。
    @ViewBuilder
    private var planNote: some View {
        if viewModel.planState == .active && !viewModel.planDismissed {
            PlanNoteView(
                goal: viewModel.planGoal,
                steps: viewModel.planTodo,
                expanded: viewModel.planExpanded,
                onToggle: { viewModel.togglePlanExpand() },
                onDismiss: { viewModel.dismissPlanTodo() }
            )
            .frame(maxWidth: 300, alignment: .trailing)
            .transition(Self.planDismissTransition)
            .animation(.easeOut(duration: 0.25), value: viewModel.planState)
            .animation(.easeOut(duration: 0.25), value: viewModel.planDismissed)
        }
    }

    /// InputBar 底部左侧槽（上下文占用圆环）
    @ViewBuilder
    private func inputBarBottomLeading(_ ctx: InputBarContext) -> some View {
        #if os(macOS)
        if !voice.voiceBarActive {
            // 上下文占用圆环：底部左侧，hover 出自定义浮层（token + 金额）。
            ContextRingBadge(
                contextTokens: viewModel.contextTokens,
                contextWindow: viewModel.contextWindow,
                usage: viewModel.usage
            )
        }
        #endif
    }

    /// InputBar 附件托盘（已选待发缩略图）
    @ViewBuilder
    private func inputBarAttachmentTray(_ ctx: InputBarContext) -> some View {
        if !viewModel.draftAttachments.isEmpty {
            AttachmentTray(
                attachments: viewModel.draftAttachments,
                onRemove: { item in
                    viewModel.draftAttachments.removeAll { $0.id == item.id }
                },
                onAdd: { showFileImporter = true },
                onClearAll: { viewModel.draftAttachments.removeAll() },
                onPreview: { index in
                    LightboxCenter.shared.present {
                        ImageGalleryPreviewView(
                            source: .local(viewModel.draftAttachments.map(\.image)),
                            startIndex: index
                        ) { LightboxCenter.shared.dismiss() }
                    }
                }
            )
        }
    }

    /// InputBar 尾部动作。独立成方法而不是内联闭包：整个 InputBar 调用表达式已经
    /// 顶到类型检查预算上限（再加一个参数就报 "unable to type-check in reasonable time"）。
    @ViewBuilder
    private func inputBarTrailingActions(_ ctx: InputBarContext) -> some View {
        if voice.voiceBarActive {
            // 语音模式：停止键放进居中的按钮组（状态行），这里留空
            EmptyView()
        } else {
            // 流式中且草稿非空时同时给「停止 + 发送（排队）」
            SendStopButton(
                context: ctx,
                isStreaming: viewModel.isStreaming,
                canSend: ctx.canSend,
                onSend: sendDraft,
                onStop: {
                    voice.stopSpeaking()
                    viewModel.stopStreaming()
                }
            )
        }
    }

    /// 语音条开关：正常模式是绿色波形圈；语音模式变成红色「挂断」（`phone.down.fill`）——
    /// 同一行里红色只表示「取消 / 结束」，挂断用通话条惯例的字形，别再用波形圈冒充退出键。
    /// 进入按上次的档位（连续对话 / 长按说话）。
    @ViewBuilder
    private func voiceContinuousButton(_ ctx: InputBarContext) -> some View {
        let inVoice = voice.voiceBarActive
        IconButton(
            visibility: .always,
            context: ctx,
            symbol: inVoice ? "phone.down.fill" : "waveform.circle",
            tint: inVoice ? .red : Color("appAccent"),
            // 切档过渡期（state 已到 .requestingPermission）也要能退出；其余沿用原来的忙禁
            isEnabled: voice.isSwitchingMode || !voice.isBusy,
            tooltip: inVoice ? "退出语音对话" : (voice.mode == .hold ? "点击说话" : "连续对话"),
            pulsing: !inVoice && voice.state == .finishing,
            hoverBackground: inVoice ? .red.opacity(0.12) : nil,
            action: {
                if voice.voiceBarActive {
                    endVoiceSession()
                } else if voice.mode == .hold {
                    voice.enterHoldMode()
                } else {
                    Task { await voice.startContinuous(baseURL: baseURL, token: token) }
                }
            }
        )
    }

    /// 结束语音条（两档共用）：停采集、停生成、停朗读。
    /// 输入栏按钮和浮层「结束」必须走同一条路径——只调 stopContinuous 会让正在播的回复继续念完。
    private func endVoiceSession() {
        // 先停退出前那轮，再 flush 暂存；否则刚提交的新轮会被随后的 stopStreaming 一起停掉。
        // flush 会产出一轮时不发 /api/chat/stop（迟到的 stop 会误杀新轮），交给服务端 Register 顶掉旧 loop。
        // flush 期间 exitingVoice = true，onSentence 用 speakReply: false 提交，不朗读。
        let willFlush = voice.hasHeldText
        if viewModel.isStreaming {
            // flush 要接新轮：保留排队消息；不 flush（真正结束）按停止语义清空
            viewModel.stopStreaming(notifyServer: !willFlush, clearPending: !willFlush)
        }
        exitingVoice = true
        voice.exitVoiceMode()
        exitingVoice = false
        voice.stopSpeaking()
    }

    /// 读取用户选中的图片文件（macOS 原生文件选择器）
    private func handlePickedFiles(_ urls: [URL]) {
        Task {
            var items: [(filename: String, data: Data)] = []
            for url in urls {
                guard let raw = try? Data(contentsOf: url) else {
                    LogBuffer.shared.warning("ChatView: 无法读取图片 \(url.lastPathComponent)")
                    continue
                }
                items.append((url.lastPathComponent, raw))
            }
            await appendPickedItems(items)
        }
    }

    /// 追加待发图片：文件选择器与 ⌘V 粘贴共用（压缩 → 去重 → 上限 → 追加）
    private func appendPickedItems(_ items: [(filename: String, data: Data)]) async {
        var duplicated = 0
        var overCap = 0
        for item in items {
            // 缩放/重编码放后台：十几兆的照片在主线程解码 + 编码要几百毫秒，
            // 一次二十张就是十几秒卡顿。字节是 Sendable，回主线程再解码显示
            let upload = await Task.detached { ImageCompressor.shrink(item.data) ?? item.data }.value
            guard let image = PlatformImage(data: upload) else {
                LogBuffer.shared.warning("ChatView: 无法解码图片 \(item.filename)")
                continue
            }
            // 同一张图重复选只留一份：服务端会按内容去重返回同一个 image_id，
            // 重复 id 会让模型把同一张图看两遍
            guard !viewModel.draftAttachments.contains(where: { $0.data == upload }) else {
                duplicated += 1
                continue
            }
            guard viewModel.draftAttachments.count < Layout.maxDraftImages else {
                overCap += 1
                continue
            }
            viewModel.draftAttachments.append(
                DraftAttachment(data: upload, image: image, filename: item.filename)
            )
        }
        // 跳过必须说出来：静默忽略会让人以为选图没生效，反复重选
        var reasons: [String] = []
        if duplicated > 0 { reasons.append("\(duplicated) 张重复") }
        if overCap > 0 { reasons.append("\(overCap) 张超出 \(Layout.maxDraftImages) 张上限") }
        if !reasons.isEmpty {
            ToastCenter.shared.show("已忽略 " + reasons.joined(separator: "、"))
        }
    }

    // MARK: - 智能体选择器

    /// 菜单展示列表：选中/激活项恒置首位，其余保持服务端创建时间倒序。
    private var displayAgents: [InputBarAgent] {
        guard let selID = selectedAgentID,
              let sel = pickerAgents.first(where: { $0.id == selID }) else {
            return pickerAgents
        }
        return [sel] + pickerAgents.filter { $0.id != selID }
    }

    /// 会话切换/页面出现后重新拉取第一页。
    private func reloadAgentPicker() async {
        pickerAgents = []
        pickerCursor = 0
        pickerHasMore = false
        hasLoadedAgents = false
        selectedAgentID = nil
        await loadAgentPickerPage(cursor: 0)
        hasLoadedAgents = true
    }

    /// 分页拉取（cursor 为 offset，分页大小由服务端 page_size 下发）。
    private func loadAgentPickerPage(cursor: Int) async {
        guard !isLoadingPicker else { return }
        isLoadingPicker = true
        defer { isLoadingPicker = false }
        let convID = viewModel.conversationId
        let effective = (convID == nil || convID!.isEmpty) ? nil : convID
        do {
            let resp = try await viewModel.apiClient.listAgentsForInputBar(convID: effective, cursor: cursor)
            let pageSize = resp.pageSize
            let existingIDs = Set(pickerAgents.map(\.id))
            pickerAgents += resp.agents.filter { !existingIDs.contains($0.id) }
            pickerCursor += pageSize
            // 服务端每页最多 pageSize 条非激活项：满页则有下一页
            pickerHasMore = resp.agents.filter { !$0.active }.count == pageSize
            // 已有绑定会话时，首页首位即激活智能体 → 同步选中
            if cursor == 0, let active = resp.agents.first(where: { $0.active }) {
                selectedAgentID = active.id
            }
        } catch {
            LogBuffer.shared.error("ChatView.listAgentsForInputBar: \(error)")
        }
    }

    /// 点击切换智能体：
    /// - 已有会话 → 立即 PATCH 绑定；
    /// - 空白 tab → 先创建会话并绑定智能体（空 tab 转正），后续首条消息直接携带 agent_id 防重复绑定。
    private func switchAgent(_ agent: InputBarAgent) async {
        selectedAgentID = agent.id
        if let cid = viewModel.conversationId, !cid.isEmpty {
            do {
                _ = try await viewModel.apiClient.setConversationAgent(id: cid, agentID: agent.id)
                ToastCenter.shared.show("已切换智能体：\(agent.name)")
            } catch {
                LogBuffer.shared.error("ChatView.setConversationAgent: \(error)")
                ToastCenter.shared.show("切换智能体失败：\(error.localizedDescription)")
            }
            return
        }
        do {
            let conv = try await viewModel.apiClient.createConversation(agentID: agent.id)
            viewModel.conversationId = conv.id
            viewModel.conversationTitle = conv.title
            viewModel.onConversationCreated?(conv.id, conv.title)
            ToastCenter.shared.show("已切换智能体：\(agent.name)")
        } catch {
            LogBuffer.shared.error("ChatView.createConversation: \(error)")
            ToastCenter.shared.show("切换智能体失败：\(error.localizedDescription)")
        }
    }

    /// 计划栏消失的扩散态（放大 + 淡出 + 模糊）
    private struct PlanDismissModifier: ViewModifier {
        let active: Bool
        func body(content: Content) -> some View {
            content
                .scaleEffect(active ? 1.15 : 1)
                .opacity(active ? 0 : 1)
                .blur(radius: active ? 6 : 0)
        }
    }

    /// 计划栏过渡动画：出现轻微缩放，消失时扩散（scale 放大 + 淡出 + 模糊）
    private static let planDismissTransition = AnyTransition
        .asymmetric(
            insertion: .scale(scale: 0.95).combined(with: .opacity),
            removal: .modifier(
                active: PlanDismissModifier(active: true),
                identity: PlanDismissModifier(active: false)
            )
        )

    @ViewBuilder
    private func toolConfirmationBar(_ conf: ToolConfirmation) -> some View {
        if let diff = conf.diffText {
            // fs 写工具：diff 预览卡片 + 操作按钮
            VStack(alignment: .leading, spacing: 8) {
                DiffPreviewView(
                    diffText: diff,
                    additions: conf.additions,
                    deletions: conf.deletions,
                    op: conf.displayName,
                    filePath: conf.filePath ?? ""
                )
                .contentShape(Rectangle())
                // 点卡片任意处（含 diff 滚动区）→ 放大浮层；simultaneousGesture 避免被 ScrollView 吞掉
                .simultaneousGesture(TapGesture().onEnded {
                    LightboxCenter.shared.present {
                        ExpandedDiffPreviewView(viewModel: viewModel, conf: conf) {
                            LightboxCenter.shared.dismiss()
                        }
                    }
                })
                HStack(spacing: 8) {
                    Image(systemName: "shield.lefthalf.filled")
                        .font(.caption)
                        .foregroundColor(.orange)
                    Text("\(Int(ToolConfirmation.timeoutSeconds))s 未处理自动拒绝")
                        .font(.caption2)
                        .foregroundColor(.secondary)
                    Spacer()
                    Button("允许") { viewModel.resolveToolConfirmation(id: conf.id, allow: true) }
                        .buttonStyle(.bordered)
                        .controlSize(.small)
                        .tint(Color("appAccent"))
                    Button("拒绝") { viewModel.resolveToolConfirmation(id: conf.id, allow: false) }
                        .buttonStyle(.bordered)
                        .controlSize(.small)
                        .tint(.secondary)
                }
            }
            .padding(.horizontal, Layout.chatHorizontalPadding)
            .padding(.vertical, 6)
            .frame(maxWidth: 720, alignment: .center)
        } else {
            let accent: Color = conf.hardened ? .red : .orange
            HStack(spacing: 8) {
                Image(systemName: conf.hardened ? "exclamationmark.shield.fill" : "shield.lefthalf.filled")
                    .font(.caption)
                    .foregroundColor(accent)
                VStack(alignment: .leading, spacing: 2) {
                    Text(conf.hardened
                         ? "请求执行高危操作：「\(conf.displayName)」"
                         : "请求执行工具：「\(conf.displayName)」")
                        .font(.caption)
                        .foregroundColor(.secondary)
                    if let explain = conf.explain {
                        // shell：人话解释为主（用户不读原始命令）；原始命令以等宽小字附在下方
                        Text(explain)
                            .font(.callout)
                            .foregroundColor(conf.hardened ? .red : .primary)
                            .fixedSize(horizontal: false, vertical: true)
                        Text(conf.argsSummary)
                            .font(.system(.caption2, design: .monospaced))
                            .foregroundColor(.secondary)
                            .lineLimit(6)
                            .textSelection(.enabled)
                    } else {
                        Text(conf.argsSummary)
                            .font(.system(.caption, design: .monospaced))
                            .foregroundColor(conf.hardened ? .red : .primary)
                            .lineLimit(6)
                            .textSelection(.enabled)
                    }
                }
                Button("允许") { viewModel.resolveToolConfirmation(id: conf.id, allow: true) }
                    .buttonStyle(.bordered)
                    .controlSize(.small)
                    .tint(accent)
                    .padding(.leading, 12)
                Button("拒绝") { viewModel.resolveToolConfirmation(id: conf.id, allow: false) }
                    .buttonStyle(.bordered)
                    .controlSize(.small)
                    .tint(accent)
            }
            .padding(.vertical, 4)
        }
    }

#if os(iOS)
    private var topBarCover: some View {
        GeometryReader { _ in
            let top = statusBarHeight
            let fade: CGFloat = 10
            Rectangle()
                .fill(.ultraThinMaterial)
                .frame(height: top + fade)
                .mask(
                    LinearGradient(
                        stops: [
                            .init(color: .black, location: 0),
                            .init(color: .black, location: top / (top + fade)),
                            .init(color: .clear, location: 1)
                        ],
                        startPoint: .top,
                        endPoint: .bottom
                    )
                )
                .ignoresSafeArea(edges: .top)
        }
        .allowsHitTesting(false)
    }

    private var statusBarHeight: CGFloat {
        (UIApplication.shared.connectedScenes
            .compactMap { $0 as? UIWindowScene }
            .first?.statusBarManager?.statusBarFrame.height) ?? 20
    }
#endif
}

// MARK: - ComposerTopRow
/// 子任务条上方的一行：把「待确认条」（前）与「执行计划便签」（后）并排——
/// 待确认条居中偏左、计划便签靠右；窗口窄到放不下时 `ViewThatFits` 自动回退成竖排
/// （计划便签在上、待确认条在下），避免两者互相挤压。
struct ComposerTopRow<Leading: View, Trailing: View>: View {
    let leading: Leading
    let trailing: Trailing

    init(@ViewBuilder leading: () -> Leading, @ViewBuilder trailing: () -> Trailing) {
        self.leading = leading()
        self.trailing = trailing()
    }

    var body: some View {
        ViewThatFits(in: .horizontal) {
            // 宽：待确认条居中偏左 + 计划便签靠右，同一行。
            HStack(alignment: .bottom, spacing: 12) {
                Spacer(minLength: 0)
                leading
                Spacer(minLength: 12)
                trailing
            }
            // 窄：竖排回退——计划便签(右) 在上、待确认条 在下，各自完整可读。
            VStack(spacing: 6) {
                trailing.frame(maxWidth: .infinity, alignment: .trailing)
                leading
            }
        }
        .frame(maxWidth: 720)
        .padding(.horizontal, Layout.chatHorizontalPadding)
        .frame(maxWidth: .infinity, alignment: .center)
        .padding(.bottom, 6)
    }
}

#if DEBUG
#Preview("子任务条上方一行 · 宽") {
    ComposerTopRowPreview(containerWidth: 900)
}

#Preview("子任务条上方一行 · 窄") {
    ComposerTopRowPreview(containerWidth: 400)
}

/// 预览容器：ComposerTopRow + 真实 PlanNoteView + 仿真待确认条。
private struct ComposerTopRowPreview: View {
    let containerWidth: CGFloat

    private let steps: [PlanStep] = [
        PlanStep(content: "确认测试目标与范围", status: "completed"),
        PlanStep(content: "执行测试步骤并记录结果", status: "in_progress"),
        PlanStep(content: "汇总结果并输出结论", status: "pending"),
    ]

    var body: some View {
        ZStack {
            Color("chatBackground").ignoresSafeArea()
            VStack {
                Spacer()
                ComposerTopRow {
                    mockConfirmation
                } trailing: {
                    PlanNoteView(goal: "回归测试计划", steps: steps, expanded: true,
                                 onToggle: {}, onDismiss: {})
                }
                // 子任务状态条占位（示意它在下方一行）
                HStack(spacing: 6) {
                    Image(systemName: "gearshape").font(.caption).foregroundColor(.accentColor)
                    Text("2 个子任务运行中").font(.caption).bold()
                    Text("fs.read").font(.caption2).foregroundColor(.secondary)
                    Spacer(minLength: 4)
                    Text("展开").font(.caption2).foregroundColor(.secondary)
                    Image(systemName: "chevron.right").font(.caption2).foregroundColor(.secondary)
                }
                .padding(.horizontal, 10).padding(.vertical, 7)
                .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 10))
                .frame(maxWidth: 720)
                .padding(.horizontal, Layout.chatHorizontalPadding)
                .padding(.bottom, 24)
            }
        }
        .frame(width: containerWidth, height: 300)
        .environmentObject(FontSizeManager.shared)
    }

    private var mockConfirmation: some View {
        HStack(spacing: 8) {
            Image(systemName: "shield.lefthalf.filled")
                .font(.caption).foregroundColor(.orange)
            VStack(alignment: .leading, spacing: 2) {
                Text("请求执行工具：「读取文件」").font(.caption).foregroundColor(.secondary)
                Text("filePath=/Users/you").font(.system(.caption, design: .monospaced))
            }
            Button("允许") {}.buttonStyle(.bordered).controlSize(.small)
                .tint(Color("appAccent")).padding(.leading, 12)
            Button("拒绝") {}.buttonStyle(.bordered).controlSize(.small).tint(.secondary)
        }
        .padding(.vertical, 4)
    }
}
#endif
