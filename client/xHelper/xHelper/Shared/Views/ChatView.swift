import SwiftUI
#if os(iOS)
import UIKit
#endif

struct ChatView: View {
    @ObservedObject var viewModel: ChatViewModel
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
            }

            // 错误提示改为全局 Toast（ToastCenter），此处不再内嵌红字

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

            // 输入栏（驾驶舱）：按钮通过 builder 注入，收起/展开双态
            InputBar(
                text: $viewModel.draftText,
                isEditing: $isEditing,
                isStreaming: viewModel.isStreaming,
                dismissKeyboardToken: dismissKeyboardToken,
                containerTint: inputBarTint,
                onSend: sendDraft,
                onStop: {
                    viewModel.stopStreaming()
                }
            ) { ctx in
                #if os(macOS)
                HStack(spacing: 4) {
                    CollapsibleModeControls(
                        context: ctx,
                        isPlanMode: viewModel.isPlanMode,
                        trustedMode: viewModel.trustedMode,
                        onToggleMode: {
                            Task { await viewModel.toggleMode() }
                        },
                        onToggleTrusted: {
                            viewModel.toggleTrustedMode()
                        }
                    )
                    // 智能体选择器：展开按钮与模型切换之间，cpu 图标与侧边栏一致。
                    // 仅当已确认用户存在智能体时才展示（无智能体时隐藏，避免无意义按钮）。
                    if hasLoadedAgents && !pickerAgents.isEmpty {
                        AgentPickerButton(
                            context: ctx,
                            activeAgentID: selectedAgentID,
                            activeAgentName: displayAgents.first?.name,
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
                    // 常驻模型切换（chevron 与发送按钮之间）：deep（pro+thinking）↔ flash
                    ModelPill(context: ctx, isDeep: viewModel.deepMode) {
                        viewModel.toggleDeepMode()
                    }
                    // 响应校验开关（每轮多一次 LLM 检查）
                    VerifyPill(context: ctx, isOn: viewModel.verifyEnabled) {
                        viewModel.toggleVerify()
                    }
                }
                #endif
            } trailingActions: { ctx in
                #if os(macOS)
                SendStopButton(
                    context: ctx,
                    isStreaming: viewModel.isStreaming,
                    canSend: ctx.canSend,
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
                        isEnabled: ctx.canSend,
                        tooltip: "发送",
                        action: sendDraft
                    )
                }
                #endif
            } topTrailingNote: { ctx in
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
            .layoutPriority(1)
            .animation(.easeOut(duration: 0.2), value: viewModel.planExpanded)
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
    }

    /// 输入栏容器描边/光晕色：计划模式橙、信任模式金、其余默认强调色。
    private var inputBarTint: Color {
        if viewModel.isPlanMode { return .orange }
        if viewModel.trustedMode { return Color(red: 0.85, green: 0.6, blue: 0.0) }
        return Color("appAccent")
    }

    /// 发送草稿：校验非空 → 清输入 → 发消息 → 滚到底部。
    private func sendDraft() {
        guard !viewModel.draftText.trimmingCharacters(in: .whitespaces).isEmpty else { return }
        dismissKeyboardToken = UUID()
        viewModel.sendMessage(viewModel.draftText, baseURL: baseURL, token: token, agentId: selectedAgentID ?? agentId)
        viewModel.draftText = ""
        withAnimation(.easeOut(duration: 0.3)) {
            scrollPosition.scrollTo(edge: .top)
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
            } catch {
                LogBuffer.shared.error("ChatView.setConversationAgent: \(error)")
            }
            return
        }
        do {
            let conv = try await viewModel.apiClient.createConversation(agentID: agent.id)
            viewModel.conversationId = conv.id
            viewModel.conversationTitle = conv.title
            viewModel.onConversationCreated?(conv.id, conv.title)
        } catch {
            LogBuffer.shared.error("ChatView.createConversation: \(error)")
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

    private func toolConfirmationBar(_ conf: ToolConfirmation) -> some View {
        HStack(spacing: 8) {
            Image(systemName: "shield.lefthalf.filled")
                .font(.caption)
                .foregroundColor(.orange)
            Text("请求执行工具：「\(conf.displayName)」：\(conf.argsSummary)")
                .font(.caption)
                .foregroundColor(.secondary)
                .lineLimit(3)
            Button("允许") { viewModel.resolveToolConfirmation(allow: true) }
                .buttonStyle(.bordered)
                .controlSize(.small)
                .tint(.orange)
                .padding(.leading, 12)
            Button("拒绝") { viewModel.resolveToolConfirmation(allow: false) }
                .buttonStyle(.bordered)
                .controlSize(.small)
                .tint(.orange)
        }
        .padding(.horizontal, Layout.chatHorizontalPadding)
        .padding(.vertical, 4)
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
