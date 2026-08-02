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

            // 模式条（macOS）：计划模式（work/plan）+ 信任模式，点击切换
            #if os(macOS)
            HStack(spacing: 8) {
                modeBar
                trustedModeBar
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            #endif

            // 执行计划（todo）丑版列表：当前会话已保存的计划步骤（UI 后续优化）
            if !viewModel.planTodo.isEmpty {
                planTodoList
            }

            // 输入栏
            InputBar(
                text: $viewModel.draftText,
                isEditing: $isEditing,
                isStreaming: viewModel.isStreaming,
                dismissKeyboardToken: dismissKeyboardToken,
                onSend: {
                    guard !viewModel.draftText.trimmingCharacters(in: .whitespaces).isEmpty else { return }
                    dismissKeyboardToken = UUID()
                    viewModel.sendMessage(viewModel.draftText, baseURL: baseURL, token: token, agentId: agentId)
                    viewModel.draftText = ""
                    withAnimation(.easeOut(duration: 0.3)) {
                        scrollPosition.scrollTo(edge: .top)
                    }
                },
                onStop: {
                    viewModel.stopStreaming()
                },
                onToggleMode: {
                    Task { await viewModel.toggleMode() }
                },
                onToggleTrustedMode: {
                    viewModel.toggleTrustedMode()
                }
            )
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

    /// 会话模式条：work（正常，蓝）/ plan（只读讨论，橙）。点击切换。
    private var modeBar: some View {
        Button {
            Task { await viewModel.toggleMode() }
        } label: {
            HStack(spacing: 5) {
                Image(systemName: viewModel.isPlanMode ? "eye.slash" : "square.and.pencil")
                    .font(.caption2)
                Text(viewModel.isPlanMode ? "计划模式 · 只读" : "工作模式")
                    .font(.caption)
            }
            .foregroundColor(viewModel.isPlanMode ? .orange : .blue)
            .padding(.horizontal, 10)
            .padding(.vertical, 3)
            .background(
                Capsule().fill(
                    (viewModel.isPlanMode ? Color.orange : Color.blue).opacity(0.12)
                )
            )
        }
        .buttonStyle(.plain)
        .help("点击切换会话模式：工作（可执行全部工具）/ 计划（只读讨论，写操作被拦截）")
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(.horizontal, 4)
    }

    /// 信任模式状态条：默认"正常模式"（蓝），开启"信任模式 · 免确认"（橙）。点击切换。
    /// 仅 macOS 显示（iOS 纯聊天无 L3 工具，不需要）。
    private var trustedModeBar: some View {
        Button {
            viewModel.toggleTrustedMode()
        } label: {
            HStack(spacing: 5) {
                Image(systemName: viewModel.trustedMode ? "shield.checkered" : "shield")
                    .font(.caption2)
                Text(viewModel.trustedMode ? "信任模式 · 免确认" : "正常模式")
                    .font(.caption)
            }
            .foregroundColor(viewModel.trustedMode ? .orange : .blue)
            .padding(.horizontal, 10)
            .padding(.vertical, 3)
            .background(
                Capsule().fill(
                    (viewModel.trustedMode ? Color.orange : Color.blue).opacity(0.12)
                )
            )
        }
        .buttonStyle(.plain)
        .help("点击切换信任模式：开启后高风险工具（L3）不再弹确认，直接执行")
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(.horizontal, 4)
    }

    /// L3 工具执行确认行（无背景，橙色图标/按钮）
    /// 执行计划（todo）丑版列表：goal + 步骤状态
    private var planTodoList: some View {
        VStack(alignment: .leading, spacing: 3) {
            if !viewModel.planGoal.isEmpty {
                Text(viewModel.planGoal)
                    .font(.caption).bold()
                    .foregroundColor(.secondary)
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
