import SwiftUI

struct MessageListView: View {
    @ObservedObject var viewModel: ChatViewModel
    @State private var initialLoadDone = false

    var body: some View {
        ScrollView {
            VStack(spacing: 12) {
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
                        .scaleEffect(y: -1)
                }

                // header（加载更早/已无更早）→ 逻辑最后 → 视觉最顶
                loadMoreHeader
                    .scaleEffect(y: -1)
            }
            .padding(.vertical)
        }
        .scaleEffect(y: -1)
        .defaultScrollAnchor(.top)
        .dismissKeyboardOnTap()
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
    }

    // 单条消息行。流式那条：ChatBubble 在前、ThinkingBubble 在后，
    // 经外层 .scaleEffect(y:-1) 翻转后恢复「思考在上、回复在下」。
    @ViewBuilder
    private func messageRow(_ msg: Message) -> some View {
        VStack(spacing: 12) {
            if msg.role == "tool" {
                ChatBubble(text: msg.content, isUser: false, isTool: true, messageId: msg.id, createdAt: msg.createdAt)
            } else if msg.role == "user" {
                ChatBubble(text: msg.content, isUser: true, messageId: msg.id, createdAt: msg.createdAt)
            } else {
                ChatBubble(text: msg.content, isUser: false, messageId: msg.id, createdAt: msg.createdAt)
            }

            if msg.id == "__streaming__", let thought = viewModel.thoughtMessage {
                ThinkingBubble(text: thought, isStreaming: !viewModel.thoughtDone)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(.horizontal)
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
            .foregroundColor(.blue)
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
