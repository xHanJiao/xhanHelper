import SwiftUI
import UniformTypeIdentifiers
#if os(iOS)
import UIKit
#else
import AppKit
#endif

struct ChatView: View {
    @ObservedObject var viewModel: ChatViewModel
    @State private var isEditing = false
    @State private var dismissKeyboardToken: UUID?
    @State private var scrollPosition = ScrollPosition(edge: .top)
    @State private var showScrollToBottom = false
    /// 进入「完全信任」的二次确认（防连点误入最危险档）
    @State private var showFullTrustConfirm = false
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
    @State private var draftAttachments: [DraftAttachment] = []
    @State private var isUploadingAttachments = false
    @State private var showFileImporter = false
    /// 本视图输入框是否聚焦（⌘V 粘贴定位收件 ChatView 用，见 viewModel.pastedImages）
    @State private var inputFocused = false

    // MARK: 输入栏折叠的更多操作
    /// 展开态：在按钮行上方多出一行（同一胶囊内）。12s 无操作自动收起。
    @State private var inputBarExtrasExpanded = false
    @State private var extrasCollapseTask: Task<Void, Never>?
    /// 每个按钮常驻/收起的配置（右键菜单切换，持久化，主/PiP 实例共享）。
    @ObservedObject private var inputBarLayout = InputBarLayoutState.shared

    let baseURL: String
    let token: String

    var body: some View {
#if os(iOS)
        ZStack(alignment: .top) {
            GlassBackground()
            content
            topBarCover
        }
#else
        content
#endif
    }

    private var content: some View {
        VStack(spacing: 0) {
            // 消息列表
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
                    hasMoreBefore: viewModel.hasMoreBefore,
                    isLoadingMore: viewModel.isLoadingMore,
                    loadMoreError: viewModel.loadMoreError,
                    onLoadMore: { direction in Task { await viewModel.loadMore(direction: direction) } },
                    onRetry: {
                        guard let cid = viewModel.conversationId else { return }
                        Task { await viewModel.loadConversation(id: cid, title: viewModel.conversationTitle ?? "", mode: viewModel.mode, preserveTrustedMode: true) } // 重试保持当前模式/信任模式
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
                    .padding(.bottom, 12)
                }

                // 子任务详情浮层：覆盖在消息列表上（不改变消息区高度），点遮罩/收起收回。
                subagentOverlay
            }

            // 错误提示改为全局 Toast（ToastCenter），此处不再内嵌红字

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

            // 子任务活动面板（折叠态）：输入栏上方一行状态条，点击展开为覆盖消息区的浮层。
            // 展开态由浮层承载，状态条隐藏；收起后重新出现。
            // 宽度约束与 InputBar 完全一致（maxWidth 720 + trailing + chatHorizontalPadding），保证同宽。
            if !viewModel.subagentSessions.isEmpty && !viewModel.subagentPanelExpanded {
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
            }

            // L3 工具确认行：输入框上方，固定可见（浏览历史消息时也不会错过许可请求）
            if let conf = viewModel.pendingToolConfirmation {
                toolConfirmationBar(conf)
            }

            // 执行计划便签：由 InputBar 的 topTrailingNote 注入，右停靠在驾驶舱上缘
            // （便签在 InputBar 内部 overlay 渲染，不占此处布局）
            // —— 完成/取消态横幅（已砍掉，保留注释供未来恢复）——
            // if let banner = viewModel.planFlashBanner {
            //     Text(banner)
            //         .font(.caption)
            //         .foregroundColor(.green)
            //         .padding(.horizontal, 12)
            //         .padding(.vertical, 6)
            //         .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 8))
            //         .padding(.horizontal, 4)
            // }

            // 文档引用胶囊（「问 AI」）：会话级状态，引用还在就在输入栏上方常驻
            if let quote = viewModel.docQuote {
                quoteChipBar(quote)
            }

            // 输入栏（驾驶舱）：按钮通过 builder 注入，收起/展开双态
            inputBar

            // 「问 AI」的回答落成批注：引用还在 + 引用之后已经出过一轮回答 → 输入栏下方一行
            if let quote = viewModel.docQuote, let answer = viewModel.quoteAnswerText {
                quoteNoteBar(quote: quote, answer: answer)
            }
        }
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
        // 进入「完全信任」的二次确认（防连点误入最危险档）。挂在稳定的 content 上，
        // 而非会随输入栏收起而消失的胶囊上，避免弹窗期间视图被回收。
        .confirmationDialog(
            "进入完全信任？",
            isPresented: $showFullTrustConfirm,
            titleVisibility: .visible
        ) {
            Button("进入完全信任", role: .destructive) { viewModel.cycleTrustMode() }
            Button("取消", role: .cancel) {}
        } message: {
            Text("将跳过包括高危 shell 命令在内的全部确认；仅敏感页面（银行/登录等）的防注入确认仍会弹出。")
        }
    }

    /// 子任务详情浮层：仅展开且存在会话时渲染，覆盖消息列表（不改变消息区高度）。
    @ViewBuilder
    private var subagentOverlay: some View {
        if viewModel.subagentPanelExpanded && !viewModel.subagentSessions.isEmpty {
            GeometryReader { geo in
                VStack(spacing: 0) {
                    // 透明点击层：点空白处收起（不渲染深色遮罩，避免列表上方出现深色盖板）
                    Color.clear
                        .contentShape(Rectangle())
                        .onTapGesture { collapseSubagentPanel() }
                    SubagentDetailPanel(
                        sessions: viewModel.subagentSessions,
                        onToggleSession: { viewModel.toggleSubagentExpand($0) },
                        onCollapse: { collapseSubagentPanel() }
                    )
                    .frame(maxHeight: geo.size.height * 0.78)
                    .padding(.horizontal, Layout.chatHorizontalPadding)
                    .padding(.bottom, 6)
                }
            }
            .transition(.move(edge: .bottom).combined(with: .opacity))
            .zIndex(1)
        }
    }

    private func collapseSubagentPanel() {
        withAnimation(.easeOut(duration: 0.25)) { viewModel.subagentPanelExpanded = false }
    }

    /// 输入栏容器描边/光晕色：计划·普通橙、计划·严格红、信任金、完全信任红、其余默认强调色。
    private var inputBarTint: Color {
        switch viewModel.chatMode {
        case .normal: return .orange
        case .strict: return .red
        case .off:
            switch viewModel.trustLevel {
            case .off: return Color("appAccent")
            case .trusted: return Color(red: 0.85, green: 0.6, blue: 0.0)
            case .full: return .red
            }
        }
    }

    /// 发送草稿：校验非空 → 清输入 → 发消息 → 滚到底部。
    /// 带图时刻意让托盘留到上传成功后再清——托盘转圈就是上传期间的占位。
    private func sendDraft() {
        let text = viewModel.draftText
        let attachments = draftAttachments
        let hasText = !text.trimmingCharacters(in: .whitespaces).isEmpty
        guard hasText || !attachments.isEmpty else { return }

        dismissKeyboardToken = UUID()
        viewModel.draftText = ""
        withAnimation(.easeOut(duration: 0.3)) {
            scrollPosition.scrollTo(edge: .top)
        }

        guard !attachments.isEmpty else {
            draftAttachments = []
            viewModel.sendMessage(text, baseURL: baseURL, token: token, agentId: selectedAgentID ?? agentId)
            return
        }

        // 带图：先上传拿 image_id，再发送
        isUploadingAttachments = true
        Task { @MainActor in
            defer { isUploadingAttachments = false }
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
                // 全失败：文字还给用户、托盘留着，别让打好的字白丢
                viewModel.draftText = text
                return
            }
            draftAttachments = []
            viewModel.sendMessage(
                text,
                baseURL: baseURL,
                token: token,
                agentId: selectedAgentID ?? agentId,
                attachmentIds: ids,
                localImages: attachments.map(\.image)
            )
        }
    }

    /// 输入框焦点变化（InputBar 上报）：用于判断 ⌘V 粘贴该由哪个 ChatView 收下
    private func setInputFocused(_ focused: Bool) {
        inputFocused = focused
    }

    /// 文档引用胶囊：告诉用户「正拿着文档里这一段在问」，点引用文字跳回正文那段，点 × 清掉。
    /// 宽度约束与 InputBar 一致（maxWidth 720 + 同一份水平内边距），两种宿主（PiP/普通聊天）同款。
    @ViewBuilder
    private func quoteChipBar(_ quote: DocQuote) -> some View {
        HStack(spacing: 6) {
            Image(systemName: "text.quote")
                .font(.caption)
                .foregroundColor(Color("appAccent"))
            if !quote.title.isEmpty {
                Text(quote.title)
                    .font(.caption).bold()
                    .lineLimit(1)
            }
            Button {
                revealInDocument(quote)
            } label: {
                Text(quote.quote)
                    .font(.caption2)
                    .foregroundColor(.secondary)
                    .lineLimit(1)
            }
            .buttonStyle(.plain)
            .help("回到文档里这一段")
            Button {
                viewModel.docQuote = nil
            } label: {
                Image(systemName: "xmark.circle.fill")
                    .font(.caption)
                    .foregroundColor(.secondary)
            }
            .buttonStyle(.plain)
            .help("去掉这段引用")
        }
        .padding(.horizontal, 10).padding(.vertical, 7)
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 10))
        .frame(maxWidth: 720, alignment: .leading)
        .padding(.horizontal, Layout.chatHorizontalPadding)
        .frame(maxWidth: .infinity, alignment: .center)
        .padding(.bottom, 6)
    }

    /// 引用之后那轮 AI 回答 → 存成这条引用上的批注（用户还能改）。
    /// 文档没开着（或开着的是另一篇）→ 直接落库，服务端自己锚定这段文字。
    @ViewBuilder
    private func quoteNoteBar(quote: DocQuote, answer: String) -> some View {
        HStack(spacing: 8) {
            Text("把这段回答存为批注？")
                .font(.caption2)
                .foregroundColor(.secondary)
            Button("存为批注") { storeQuoteAsNote(quote, answer: answer) }
                .buttonStyle(.plain)
                .font(.caption2.bold())
                .foregroundColor(Color("appAccent"))
            Button("忽略") { viewModel.docQuote = nil }
                .buttonStyle(.plain)
                .font(.caption2)
                .foregroundColor(.secondary)
            Spacer(minLength: 0)
        }
        .padding(.horizontal, 10).padding(.vertical, 6)
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 10))
        .frame(maxWidth: 720, alignment: .leading)
        .padding(.horizontal, Layout.chatHorizontalPadding)
        .frame(maxWidth: .infinity, alignment: .center)
        .padding(.top, 6)
    }

    /// 跳回文档里那段（文档没开着、或开着的是另一篇 → 提示一句，不做"自动打开那篇文档"）。
    private func revealInDocument(_ quote: DocQuote) {
        #if os(macOS)
        guard let reader = DocumentAIBridge.shared.reader, reader.documentID == quote.documentID else {
            ToastCenter.shared.show("打开这篇文档才能跳过去")
            return
        }
        reader.revealQuote(quote)
        #endif
    }

    /// 存为批注：文档开着且就是这篇 → 走文档侧预填浮层（用户能改再存）；
    /// 否则直接落库（服务端自己定位这段文字）。
    /// 两条路都清掉引用（窄条随之消失）：交给浮层后回答已经在编辑框里，留着这条只会重复存。
    private func storeQuoteAsNote(_ quote: DocQuote, answer: String) {
        #if os(macOS)
        if let reader = DocumentAIBridge.shared.reader, reader.documentID == quote.documentID {
            reader.beginNoteFromAI(quote: quote, note: answer)
            viewModel.docQuote = nil
            return
        }
        #endif
        Task { @MainActor in
            do {
                _ = try await viewModel.apiClient.createAnnotation(
                    documentId: quote.documentID, quote: quote.quote, note: answer,
                    prefix: quote.prefix, suffix: quote.suffix
                )
                viewModel.docQuote = nil
                ToastCenter.shared.show("已存为批注")
            } catch {
                LogBuffer.shared.error("ChatView.createAnnotation(AI 回答) failed: \(error)")
                ToastCenter.shared.show("存为批注失败：\((error as NSError).localizedDescription)")
            }
        }
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
            hasAttachments: !draftAttachments.isEmpty,
            onFocusChange: setInputFocused,
            onClearAll: {
                viewModel.draftText = ""
                draftAttachments.removeAll()
            },
            onSend: sendDraft,
            onStop: {
                viewModel.stopStreaming()
            },
            onToggleMode: {
                Task { await viewModel.cycleMode() }
            },
            statusRow: inputBarStatusRow,
            statusRowExtras: inputBarExtrasRow,
            trailingActions: inputBarTrailingActions,
            topTrailingNote: inputBarTopNote,
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
            ForEach(InputBarButtonID.allCases.filter { !inputBarLayout.isCollapsed($0) }, id: \.self) { id in
                inputBarButton(id, ctx: ctx)
            }
            // 更多操作：有按钮被收起时才提供 chevron（否则全常驻，无展开行）。
            if !inputBarLayout.collapsed.isEmpty {
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
        if inputBarExtrasExpanded && !inputBarLayout.collapsed.isEmpty {
            HStack(spacing: 4) {
                Spacer(minLength: 0)
                ForEach(InputBarButtonID.allCases.filter { inputBarLayout.isCollapsed($0) }, id: \.self) { id in
                    inputBarButton(id, ctx: ctx)
                }
            }
            .transition(.move(edge: .bottom).combined(with: .opacity))
        }
        #endif
    }

    /// 单个输入栏按钮：按 id 构造对应控件，并挂右键菜单切换常驻/收起。
    @ViewBuilder
    private func inputBarButton(_ id: InputBarButtonID, ctx: InputBarContext) -> some View {
        inputBarButtonContent(id, ctx: ctx)
            .contextMenu {
                Button(inputBarLayout.isCollapsed(id) ? "常驻" : "收起") {
                    withAnimation(.easeOut(duration: 0.18)) { inputBarLayout.toggle(id) }
                }
            }
    }

    /// 按钮本体（不含右键菜单）。
    @ViewBuilder
    private func inputBarButtonContent(_ id: InputBarButtonID, ctx: InputBarContext) -> some View {
        switch id {
        case .mode:
            // 计划模式：高频使用。三档循环（关 → 普通 → 严格）。
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
        case .trusted:
            // 信任模式三档循环（安全 → 信任 → 完全信任）：进入最危险档前由 content 上的
            // confirmationDialog 二次确认，这里只负责发起。
            TrustedPill(context: ctx, level: viewModel.trustLevel) {
                if viewModel.trustLevel.next == .full {
                    showFullTrustConfirm = true
                } else {
                    viewModel.cycleTrustMode()
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
        case .handoff:
            // 会话接力：整理成文档并开新会话续接。计划模式不能写文档，
            // 故 plan/严格档下置灰（tooltip 说明原因），非流式、非空会话才可用。
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
                isEnabled: !viewModel.isStreaming && !isUploadingAttachments,
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

    /// InputBar 上缘便签（执行计划）。独立成方法：同上，InputBar 调用表达式已在类型检查预算边缘。
    @ViewBuilder
    private func inputBarTopNote(_ ctx: InputBarContext) -> some View {
        // 执行计划便签：经 InputBar 注入，窄幅右停在驾驶舱上缘。
        // 完成/取消后 state 变为非 active → 扩散消失。
        if viewModel.planState == .active && !viewModel.planDismissed {
            PlanNoteView(
                goal: viewModel.planGoal,
                steps: viewModel.planTodo,
                expanded: viewModel.planExpanded,
                onToggle: { viewModel.togglePlanExpand() },
                onDismiss: { viewModel.dismissPlanTodo() }
            )
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
        if !draftAttachments.isEmpty {
            AttachmentTray(
                attachments: draftAttachments,
                isUploading: isUploadingAttachments,
                onRemove: { item in
                    draftAttachments.removeAll { $0.id == item.id }
                },
                onAdd: { showFileImporter = true },
                onClearAll: { draftAttachments.removeAll() },
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
        SendStopButton(
            context: ctx,
            isStreaming: viewModel.isStreaming,
            canSend: ctx.canSend && !isUploadingAttachments,
            onSend: sendDraft,
            onStop: {
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
                isEnabled: ctx.canSend && !isUploadingAttachments,
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
            guard !draftAttachments.contains(where: { $0.data == upload }) else {
                duplicated += 1
                continue
            }
            guard draftAttachments.count < Layout.maxDraftImages else {
                overCap += 1
                continue
            }
            draftAttachments.append(
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
                    Text("60s 未处理自动拒绝")
                        .font(.caption2)
                        .foregroundColor(.secondary)
                    Spacer()
                    Button("允许") { viewModel.resolveToolConfirmation(allow: true) }
                        .buttonStyle(.bordered)
                        .controlSize(.small)
                        .tint(Color("appAccent"))
                    Button("拒绝") { viewModel.resolveToolConfirmation(allow: false) }
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
                         ? "请求执行高危命令：「\(conf.displayName)」（信任模式不可跳过）"
                         : "请求执行工具：「\(conf.displayName)」")
                        .font(.caption)
                        .foregroundColor(.secondary)
                    Text(conf.argsSummary)
                        .font(.system(.caption, design: .monospaced))
                        .foregroundColor(conf.hardened ? .red : .primary)
                        .lineLimit(6)
                        .textSelection(.enabled)
                }
                Button("允许") { viewModel.resolveToolConfirmation(allow: true) }
                    .buttonStyle(.bordered)
                    .controlSize(.small)
                    .tint(accent)
                    .padding(.leading, 12)
                Button("拒绝") { viewModel.resolveToolConfirmation(allow: false) }
                    .buttonStyle(.bordered)
                    .controlSize(.small)
                    .tint(accent)
            }
            .padding(.horizontal, Layout.chatHorizontalPadding)
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
