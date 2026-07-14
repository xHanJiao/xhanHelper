import SwiftUI

struct MessageListView: View {
    @ObservedObject var viewModel: ChatViewModel
    @State private var initialLoadDone = false
    @State private var activeTimeBubbleId: String? = nil
    @State private var autoDismissTask: Task<Void, Never>? = nil
    @State private var menuMessageId: String? = nil
    @State private var menuPoint: CGPoint = .zero
    @State private var menuFrames: [String: CGRect] = [:]
    @State private var menuDismissTask: Task<Void, Never>? = nil
    @State private var viewHeight: CGFloat = 0
    @Binding var scrollPosition: ScrollPosition
    @Binding var showScrollToBottom: Bool

    var body: some View {
        Group {
            if !viewModel.messages.isEmpty {
                messageScrollView
            } else if viewModel.isInitialLoading {
                ProgressView()
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else if viewModel.errorMessage != nil {
                Color.clear
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                Text("暂无消息")
                    .font(.callout)
                    .foregroundColor(.secondary)
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            }
        }
        .coordinateSpace(name: "menuSpace")
        .background(
            GeometryReader { geo in
                Color.clear.onAppear {
                    viewHeight = geo.size.height
                }
                .onChange(of: geo.size.height) { _, h in
                    viewHeight = h
                }
            }
        )
        .onAppear {
            initialLoadDone = false
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.5) {
                initialLoadDone = true
            }
        }
        .onChange(of: viewModel.conversationId) { _, _ in
            initialLoadDone = false
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.5) {
                initialLoadDone = true
            }
        }
        .onChange(of: viewModel.messages.count) { _, _ in
            dismissMenu()
        }
        .overlay {
            if menuMessageId != nil {
                Button {
                    if let msg = viewModel.messages.first(where: { $0.id == menuMessageId }) {
                        copyToPasteboard(msg.content)
                    }
                    dismissMenu()
                } label: {
                    Label("拷贝", systemImage: "doc.on.doc")
                        .font(.subheadline)
                }
                .padding(.horizontal, 16).padding(.vertical, 10)
                .background(.regularMaterial)
                .clipShape(Capsule())
                .shadow(color: .black.opacity(0.15), radius: 8, y: 4)
                .position(menuPoint)
            }
        }
    }

    private var messageScrollView: some View {
        ScrollView {
            VStack(spacing: 18) {
                // status（思考提示）→ 逻辑最前 → 视觉最底（最新消息下方）
                if let status = viewModel.statusMessage {
                    HStack {
                        Image(systemName: "brain.head.profile")
                            .font(.caption)
                            .foregroundColor(.secondary)
                        Text(status)
                            .font(.caption)
                            .foregroundColor(.secondary)
                        Spacer()
                    }
                    .padding(.horizontal)
                    .scaleEffect(y: -1)
                }

                // 消息：倒序，最新在逻辑前=视觉底
                ForEach(viewModel.messages.reversed()) { msg in
                    messageRow(msg)
                        .background(
                            GeometryReader { geo in
                                Color.clear.onAppear {
                                    menuFrames[msg.id] = geo.frame(in: .named("menuSpace"))
                                }
                                .onChange(of: geo.frame(in: .named("menuSpace"))) { _, newFrame in
                                    menuFrames[msg.id] = newFrame
                                }
                            }
                        )
                        .scaleEffect(y: -1)
                        .simultaneousGesture(
                            LongPressGesture(minimumDuration: 0.5)
                                .onEnded { _ in
                                    if !msg.content.isEmpty {
                                        if menuMessageId == msg.id {
                                            dismissMenu()
                                            return
                                        }
                                        menuMessageId = msg.id
                                        if let frame = menuFrames[msg.id], viewHeight > 0 {
                                            menuPoint = CGPoint(x: frame.midX, y: frame.midY)
                                        }
                                        startMenuDismissTimer()
                                    }
                                }
                        )
                }

                // header（加载更早/已无更早）→ 逻辑最后 → 视觉最顶
                loadMoreHeader
                    .scaleEffect(y: -1)
            }
            .padding(.vertical)
        }
        .scaleEffect(y: -1)
        .defaultScrollAnchor(.top)
        .scrollPosition($scrollPosition)
        .dismissKeyboardOnTap()
        .onScrollPhaseChange { _, newPhase in
            if newPhase != .idle { dismissMenu() }
        }
        .simultaneousGesture(TapGesture().onEnded { dismissMenu() })
        .onScrollGeometryChange(for: CGFloat.self, of: { $0.contentOffset.y }, action: { _, newY in
            showScrollToBottom = newY > viewHeight * 0.75
        })

    }

    // 单条消息行。流式那条：ThinkingBubble 在前、ChatBubble 在后，
    // 思考在上、回复在下。
    @ViewBuilder
    private func messageRow(_ msg: Message) -> some View {
        VStack(spacing: 12) {
            if msg.id == "__streaming__", let thought = viewModel.thoughtMessage {
                ThinkingBubble(text: thought, isStreaming: !viewModel.thoughtDone)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(.horizontal)
            }

            if msg.role == "user" {
                ChatBubble(text: msg.content, isUser: true, messageId: msg.id, createdAt: msg.createdAt,
                           showTime: activeTimeBubbleId == msg.id,
                           onTapToShowTime: { toggleTimeBubble(msg.id) })
            } else {
                ChatBubble(text: msg.content, isUser: false, messageId: msg.id, createdAt: msg.createdAt,
                           showTime: activeTimeBubbleId == msg.id,
                           onTapToShowTime: { toggleTimeBubble(msg.id) })
            }
        }
    }

    private func toggleTimeBubble(_ msgId: String) {
        if menuMessageId != nil {
            dismissMenu()
            return
        }
        autoDismissTask?.cancel()
        autoDismissTask = nil

        if activeTimeBubbleId == msgId {
            withAnimation(.easeInOut(duration: 0.2)) {
                activeTimeBubbleId = nil
            }
            return
        }

        withAnimation(.easeInOut(duration: 0.2)) {
            activeTimeBubbleId = msgId
        }

        autoDismissTask = Task {
            try? await Task.sleep(nanoseconds: 10_000_000_000)
            guard !Task.isCancelled else { return }
            await MainActor.run {
                withAnimation(.easeInOut(duration: 0.2)) {
                    activeTimeBubbleId = nil
                }
            }
        }
    }

    private func copyToPasteboard(_ text: String) {
        #if os(iOS)
        UIPasteboard.general.string = text
        #else
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(text, forType: .string)
        #endif
    }

    private func dismissMenu() {
        menuDismissTask?.cancel()
        menuDismissTask = nil
        menuMessageId = nil
    }

    private func startMenuDismissTimer() {
        menuDismissTask?.cancel()
        menuDismissTask = Task {
            try? await Task.sleep(nanoseconds: 20_000_000_000)
            guard !Task.isCancelled else { return }
            await MainActor.run {
                menuMessageId = nil
            }
        }
    }

    @ViewBuilder
    private var loadMoreHeader: some View {
        if viewModel.hasMoreBefore {
            HStack {
                if viewModel.isLoadingMore {
                    ProgressView().scaleEffect(0.7)
                } else {
                    Image(systemName: "arrow.up")
                }
                Text(viewModel.isLoadingMore ? "正在加载..." : "加载更早的消息")
                    .font(.caption)
            }
            .foregroundColor(Color("accentSecondary"))
            .onTapGesture {
                guard !viewModel.isLoadingMore else { return }
                Task { await viewModel.loadMore(direction: "before") }
            }
            .onScrollVisibilityChange(threshold: 0.1) { isVisible in
                guard initialLoadDone, isVisible, !viewModel.isLoadingMore else { return }
                Task { await viewModel.loadMore(direction: "before") }
            }
        } else {
            Text("已无更早消息")
                .font(.caption)
                .foregroundColor(.secondary)
        }
    }
}
