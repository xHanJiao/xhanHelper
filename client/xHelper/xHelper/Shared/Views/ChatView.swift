import SwiftUI
#if os(iOS)
import UIKit
#endif

struct ChatView: View {
    @ObservedObject var viewModel: ChatViewModel
    @State private var inputText = ""
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
                        Task { await viewModel.loadConversation(id: cid, title: viewModel.conversationTitle ?? "") }
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

            // 输入栏
            InputBar(
                text: $inputText,
                isEditing: $isEditing,
                isStreaming: viewModel.isStreaming,
                dismissKeyboardToken: dismissKeyboardToken,
                onSend: {
                    guard !inputText.trimmingCharacters(in: .whitespaces).isEmpty else { return }
                    dismissKeyboardToken = UUID()
                    viewModel.sendMessage(inputText, baseURL: baseURL, token: token, agentId: agentId)
                    inputText = ""
                    withAnimation(.easeOut(duration: 0.3)) {
                        scrollPosition.scrollTo(edge: .top)
                    }
                },
                onStop: {
                    viewModel.stopStreaming()
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
