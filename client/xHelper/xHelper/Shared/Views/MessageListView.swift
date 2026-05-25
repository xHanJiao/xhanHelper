import SwiftUI

struct MessageListView: View {
    @ObservedObject var viewModel: ChatViewModel
    @State private var scrollTarget: String?

    var body: some View {
        ScrollView {
            VStack(spacing: 12) {
                if viewModel.hasMoreBefore {
                    Button {
                        Task { await viewModel.loadMore(direction: "before") }
                    } label: {
                        HStack {
                            if viewModel.isLoadingMore {
                                ProgressView().scaleEffect(0.7)
                            } else {
                                Image(systemName: "arrow.up")
                            }
                            Text("加载更早的消息")
                                .font(.caption)
                        }
                        .foregroundColor(.blue)
                    }
                    .disabled(viewModel.isLoadingMore)
                }

                ForEach(viewModel.messages) { msg in
                    if msg.id == "__streaming__", let thought = viewModel.thoughtMessage {
                        ThinkingBubble(text: thought, isStreaming: !viewModel.thoughtDone)
                            .frame(maxWidth: .infinity, alignment: .leading)
                            .padding(.horizontal)
                    }
                    if msg.role == "tool" {
                        ChatBubble(text: msg.content, isUser: false, isTool: true, messageId: msg.id, onDelete: { Task { await viewModel.deleteMessage(msg) } })
                    } else if msg.role == "user" {
                        ChatBubble(text: msg.content, isUser: true, messageId: msg.id, onDelete: { Task { await viewModel.deleteMessage(msg) } })
                    } else {
                        VStack(alignment: .leading, spacing: 6) {
                            if let tcs = msg.parsedToolCalls {
                                ForEach(tcs, id: \.id) { tc in
                                    ToolCallHistoryPill(toolCall: tc)
                                }
                            }
                            ChatBubble(text: msg.content, isUser: false, messageId: msg.id, onDelete: { Task { await viewModel.deleteMessage(msg) } })
                        }
                    }
                }

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
                }

                Color.clear.frame(height: 1).id("bottom")
            }
            .padding(.vertical)
        }
        .defaultScrollAnchor(.bottom)
        .scrollPosition(id: $scrollTarget, anchor: .bottom)
        .dismissKeyboardOnTap()
        .onChange(of: viewModel.messages.count) { oldCount, newCount in
            guard newCount > oldCount, let anchor = viewModel.anchorMessageID else { return }
            scrollTarget = anchor
            viewModel.anchorMessageID = nil
        }
    }
}
