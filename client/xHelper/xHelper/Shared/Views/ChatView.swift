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

            // 执行计划（todo）：进行中显示步骤列表（可 × 隐藏）；完成/取消后消失（扩散动画）
            if viewModel.planState == .active && !viewModel.planDismissed {
                planTodoList
                    .transition(Self.planDismissTransition)
                    .animation(.easeOut(duration: 0.25), value: viewModel.planState)
                    .animation(.easeOut(duration: 0.25), value: viewModel.planDismissed)
            }
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
            }
            .layoutPriority(1)
        }
        .onChange(of: viewModel.scrollToBottomRequested) { _, requested in
            guard requested else { return }
            withAnimation(.easeOut(duration: 0.3)) {
                scrollPosition.scrollTo(edge: .top)
            }
            viewModel.scrollToBottomRequested = false
        }
        .onChange(of: isEditing) { _, new in
            print("[ChatView] isEditing: \(new)")
            viewModel.isEditing = new
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
        viewModel.sendMessage(viewModel.draftText, baseURL: baseURL, token: token, agentId: agentId)
        viewModel.draftText = ""
        withAnimation(.easeOut(duration: 0.3)) {
            scrollPosition.scrollTo(edge: .top)
        }
    }

    /// L3 工具执行确认行（无背景，橙色图标/按钮）
    /// 执行计划（todo）列表：goal + 步骤状态 + × 隐藏按钮（隐藏跨会话保持，新计划自动恢复）
    private var planTodoList: some View {
        VStack(alignment: .leading, spacing: 3) {
            HStack {
                if !viewModel.planGoal.isEmpty {
                    Text(viewModel.planGoal)
                        .font(.caption).bold()
                        .foregroundColor(.secondary)
                }
                Spacer()
                Button {
                    viewModel.dismissPlanTodo()
                } label: {
                    Image(systemName: "xmark.circle.fill")
                        .font(.caption2)
                        .foregroundColor(.secondary)
                }
                .buttonStyle(.plain)
                .help("隐藏计划列表")
            }
            ForEach(viewModel.planTodo) { step in
                HStack(spacing: 5) {
                    Image(systemName: planStepIcon(step.status))
                        .font(.caption2)
                        .foregroundColor(planStepColor(step.status))
                    Text(step.content)
                        .font(.caption)
                        .strikethrough(step.status == "completed" || step.status == "cancelled")
                        .foregroundColor(step.status == "cancelled" ? .secondary : .primary)
                }
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(.horizontal, 12)
        .padding(.vertical, 6)
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 8))
        .padding(.horizontal, 4)
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

    private func planStepIcon(_ status: String) -> String {
        switch status {
        case "completed": return "checkmark.circle.fill"
        case "in_progress": return "circle.inset.filled"
        case "cancelled": return "xmark.circle"
        default: return "circle"
        }
    }

    private func planStepColor(_ status: String) -> Color {
        switch status {
        case "completed": return .green
        case "in_progress": return .orange
        case "cancelled": return .secondary
        default: return .gray
        }
    }

    private func toolConfirmationBar(_ conf: ToolConfirmation) -> some View {
        HStack(spacing: 8) {
            Image(systemName: "shield.lefthalf.filled")
                .font(.caption)
                .foregroundColor(.orange)
            Text("请求执行工具：「\(conf.displayName)」：\(conf.argsSummary)")
                .font(.caption)
                .foregroundColor(.secondary)
                .lineLimit(2)
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
