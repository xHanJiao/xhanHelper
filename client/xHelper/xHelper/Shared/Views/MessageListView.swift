import SwiftUI

struct MessageListView: View {
    @ObservedObject var viewModel: ChatViewModel
    @State private var autoScroll = true
    @State private var contentHeight: CGFloat = 0
    @State private var containerHeight: CGFloat = 0
    @State private var contentHeightAtStreamStart: CGFloat = 0

    var body: some View {
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(spacing: 12) {
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
                                .padding(.horizontal)
                        }
                        if msg.role == "tool" {
                            ChatBubble(text: msg.content, isUser: false, isTool: true, messageId: msg.id, onDelete: { Task { await viewModel.deleteMessage(msg) } })
                        } else if msg.role == "user" {
                            ChatBubble(text: msg.content, isUser: true, messageId: msg.id, onDelete: { Task { await viewModel.deleteMessage(msg) } })
                        } else {
                            ChatBubble(text: msg.content, isUser: false, messageId: msg.id, onDelete: { Task { await viewModel.deleteMessage(msg) } })
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
            .onScrollGeometryChange(for: CGFloat.self, of: { $0.contentSize.height }, action: { _, newHeight in
                contentHeight = newHeight
            })
            .onScrollGeometryChange(for: CGFloat.self, of: { $0.containerSize.height }, action: { _, newHeight in
                containerHeight = newHeight
            })
            .dismissKeyboardOnTap()
            .onChange(of: viewModel.messages.count) { oldCount, newCount in
                guard newCount > oldCount, let anchor = viewModel.anchorMessageID else { return }
                proxy.scrollTo(anchor, anchor: .bottom)
                viewModel.anchorMessageID = nil
            }
            .onChange(of: viewModel.isStreaming) { oldValue, newValue in
                guard newValue, !oldValue else { return }
                autoScroll = true
                contentHeightAtStreamStart = contentHeight
                proxy.scrollTo("bottom", anchor: .bottom)
            }
            .onChange(of: viewModel.streamTick) { _, _ in
                guard autoScroll else { return }
                if contentHeight - contentHeightAtStreamStart > containerHeight {
                    autoScroll = false
                } else {
                    proxy.scrollTo("bottom", anchor: .bottom)
                }
            }
        }
    }
}
