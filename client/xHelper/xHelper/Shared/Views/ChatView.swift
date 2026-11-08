import SwiftUI
import UniformTypeIdentifiers
#if os(iOS)
import UIKit
#else
import AppKit
#endif

struct ChatView: View {
    @ObservedObject var viewModel: ChatViewModel
    @StateObject private var voice = VoiceChatService()
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
            .overlay {
                if voice.continuousActive {
                    VoiceOrbView(
                        phase: voiceOrbPhase,
                        level: voice.level,
                        onClose: {
                            voice.stopContinuous()
                            viewModel.stopStreaming()
                            voice.stopSpeaking()
                        }
                    )
                }
            }
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
    /// 全屏语音环状态：说 > 想 > 听。
    private var voiceOrbPhase: VoiceOrbView.Phase {
        if voice.isSpeaking { return .speaking }
        if viewModel.isStreaming { return .thinking }
        return .listening
    }
#endif

    private var content: some View {
        // 消息列表铺满中栏整高；底部浮层栈经 safeAreaInset 悬浮其上（见 bottomComposer）。
        ZStack(alignment: .bottom) {
            MessageListView(
                messages: viewModel.messages,
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
#endif
            voice.onFinalTranscript = { text in
                viewModel.draftText = text
                viewModel.pendingVoiceReply = true
                inputFocused = true
            }
            // C3：播报期间用户开口 → 立刻停生成（朗读与门控已在 service 里处理）
            voice.onBargeIn = {
                viewModel.stopStreaming()
            }
            // C 模式：每句判停就直接提交一轮；上一轮还在跑就先停掉，避免两个 loop 交叉写库
            voice.onSentence = { text in
                if viewModel.isStreaming {
                    voice.stopSpeaking()
                    // 不发 /api/chat/stop：服务端 Register 会取消旧 loop，迟到的 stop 会误杀新轮
                    viewModel.stopStreaming(notifyServer: false)
                }
                viewModel.sendMessage(text, baseURL: baseURL, token: token,
                                      agentId: selectedAgentID ?? agentId, speakReply: true)
            }
            // 空闲/会话计时器要知道「正在回答」，否则会在 AI 说话时把会话收掉
            voice.isChatBusy = { [weak viewModel] in viewModel?.isStreaming ?? false }
            viewModel.onAssistantReplyDone = { text in
                Task { await voice.speak(text: text, baseURL: baseURL, token: token) }
            }
            Task { await voice.refresh(baseURL: baseURL, token: token) }
        }
        .onDisappear {
            voice.cancel()
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
        guard !viewModel.isUploadingAttachments else { return }
        let text = viewModel.draftText
        let attachments = viewModel.draftAttachments
        let hasText = !text.trimmingCharacters(in: .whitespaces).isEmpty
        guard hasText || !attachments.isEmpty else { return }
        let speakReply = viewModel.pendingVoiceReply
        viewModel.pendingVoiceReply = false

        dismissKeyboardToken = UUID()
        viewModel.draftText = ""
        viewModel.draftAttachments = []
        withAnimation(.easeOut(duration: 0.3)) {
            scrollPosition.scrollTo(edge: .top)
        }

        guard !attachments.isEmpty else {
            viewModel.sendMessage(text, baseURL: baseURL, token: token, agentId: selectedAgentID ?? agentId, speakReply: speakReply)
            return
        }

        // 带图：先上传拿 image_id，再发送
        viewModel.isUploadingAttachments = true
        Task { @MainActor in
            defer { viewModel.isUploadingAttachments = false }
            let api = viewModel.apiClient
            var ids: [String] = []
            for item in attachments {
                do {
                    let uploaded = try await api.uploadImage(item.data, filename: item.filename)
                    ids.append(uploaded.imageId)
                    // 手上就有解码好的图，直接进共享缓存：消息重载后不用再拉一次
                    AttachmentImageLoader.shared.seed(id: uploaded.imageId, image: item.image)
                } catch {
                    LogBuffer.shared.error("ChatView.uploadImage failed: \(error)")
                    ToastCenter.shared.show("图片上传失败：\((error as NSError).localizedDescription)")
                }
            }
            guard !ids.isEmpty else {
                // 全失败：文字和待发图一起还给用户，别让打好的字白丢
                viewModel.draftText = text
                viewModel.draftAttachments = attachments
                return
            }
            viewModel.sendMessage(
                text,
                baseURL: baseURL,
                token: token,
                agentId: selectedAgentID ?? agentId,
                attachmentIds: ids,
                localImages: attachments.map(\.image),
                speakReply: speakReply
            )
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

    /// InputBar 状态行：常驻按钮 + 更多操作 chevron。哪些按钮常驻由折叠配置决定。
    /// 独立成方法而不是内联闭包：整个 InputBar 调用表达式已顶到类型检查预算上限。
    @ViewBuilder
    private func inputBarStatusRow(_ ctx: InputBarContext) -> some View {
        #if os(macOS)
        HStack(spacing: 4) {
            ForEach(InputBarButtonID.allCases.filter { !inputBarLayout.isCollapsed($0) || isPinnedInStatusRow($0) }, id: \.self) { id in
                inputBarButton(id, ctx: ctx)
            }
            // 更多操作：有按钮被收起时才提供 chevron（否则全常驻，无展开行）。
            if !collapsedInExtras.isEmpty {
                ExpandPill(context: ctx, isExpanded: inputBarExtrasExpanded) {
                    toggleInputBarExtras()
                }
            }
        }
        #endif
    }

    /// InputBar 折叠的更多操作行：展开时出现在按钮行上方，同胶囊内右对齐——
    /// 参与布局，宽度受胶囊约束，窄窗不会戳出框。
    @ViewBuilder
    private func inputBarExtrasRow(_ ctx: InputBarContext) -> some View {
        #if os(macOS)
        if inputBarExtrasExpanded && !collapsedInExtras.isEmpty {
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
                isEnabled: !viewModel.isStreaming && !viewModel.isUploadingAttachments,
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
        // 上下文占用圆环：底部左侧，hover 出自定义浮层（token + 金额）。
        ContextRingBadge(
            contextTokens: viewModel.contextTokens,
            contextWindow: viewModel.contextWindow,
            usage: viewModel.usage
        )
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
                onPreview: { item in
                    LightboxCenter.shared.present {
                        AttachmentPreviewView(image: item.image) { LightboxCenter.shared.dismiss() }
                    }
                }
            )
        }
    }

    /// InputBar 尾部动作。独立成方法而不是内联闭包：整个 InputBar 调用表达式已经
    /// 顶到类型检查预算上限（再加一个参数就报 "unable to type-check in reasonable time"）。
    @ViewBuilder
    private func inputBarTrailingActions(_ ctx: InputBarContext) -> some View {
        #if os(macOS)
        // 仅发送/停止常驻；附件已收进「更多操作」展开行。
        if voice.isAvailable {
            IconButton(
                visibility: .always,
                context: ctx,
                symbol: voice.continuousActive ? "waveform.circle.fill" : "waveform.circle",
                tint: voice.continuousActive ? .red : Color("appAccent"),
                isEnabled: !voice.isBusy,
                tooltip: voice.continuousActive ? "结束连续对话" : "连续对话",
                pulsing: voice.state == .finishing,
                action: {
                    if voice.continuousActive {
                        voice.stopContinuous()
                    } else {
                        Task { await voice.startContinuous(baseURL: baseURL, token: token) }
                    }
                }
            )
            IconButton(
                visibility: .always,
                context: ctx,
                symbol: voice.isRecording ? "mic.fill" : "mic",
                tint: voice.isRecording ? .red : Color("appAccent"),
                isEnabled: !voice.isBusy && !voice.continuousActive,
                tooltip: voice.isRecording ? "停止录音" : "语音输入",
                pulsing: voice.state == .finishing,
                action: {
                    if voice.isRecording {
                        voice.stop()
                    } else {
                        Task { await voice.start(baseURL: baseURL, token: token) }
                    }
                }
            )
        }
        SendStopButton(
            context: ctx,
            isStreaming: viewModel.isStreaming,
            canSend: ctx.canSend && !viewModel.isUploadingAttachments,
            onSend: sendDraft,
            onStop: {
                voice.stopSpeaking()
                viewModel.stopStreaming()
            }
        )
        #else
        if ctx.isStreaming {
            IconButton(
                visibility: .always,
                context: ctx,
                symbol: "stop.circle.fill",
                tint: .red,
                tooltip: "停止生成",
                action: { viewModel.stopStreaming() }
            )
        } else {
            IconButton(
                visibility: .always,
                context: ctx,
                symbol: "arrow.up.circle.fill",
                tint: ctx.canSend ? Color("appAccent") : .gray,
                isEnabled: ctx.canSend && !viewModel.isUploadingAttachments,
                tooltip: "发送",
                action: sendDraft
            )
        }
        #endif
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
