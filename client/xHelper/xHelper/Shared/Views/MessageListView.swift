import SwiftUI

struct MessageListView: View {
    @ObservedObject var viewModel: ChatViewModel

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
                        if msg.role == "tool" {
                            ChatBubble(text: msg.content, isUser: false, isTool: true, messageId: msg.id, onDelete: { Task { await viewModel.deleteMessage(msg) } })
                        } else if msg.role == "user" {
                            ChatBubble(text: msg.content, isUser: true, messageId: msg.id, onDelete: { Task { await viewModel.deleteMessage(msg) } })
                        } else {
                            ChatBubble(text: msg.content, isUser: false, messageId: msg.id, onDelete: { Task { await viewModel.deleteMessage(msg) } })
                        }
                    }

                    if let thought = viewModel.thoughtMessage {
                        HStack {
                            Image(systemName: "lightbulb")
                                .font(.caption)
                                .foregroundColor(.yellow)
                            Text(thought)
                                .font(.caption)
                                .italic()
                                .foregroundColor(.gray)
                            Spacer()
                        }
                        .padding(.horizontal)
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
                }
                .padding(.vertical)
            }
            .dismissKeyboardOnTap()
            .onChange(of: viewModel.messages.count) { oldCount, newCount in
                if newCount > oldCount {
                    if let anchor = viewModel.anchorMessageID {
                        withAnimation { proxy.scrollTo(anchor, anchor: .top) }
                        viewModel.anchorMessageID = nil
                    } else if let last = viewModel.messages.last, last.id != "__streaming__" {
                        withAnimation { proxy.scrollTo(last.id, anchor: .bottom) }
                    }
                }
            }
            .onChange(of: viewModel.messages.last?.content) {
                if let last = viewModel.messages.last {
                    withAnimation { proxy.scrollTo(last.id, anchor: .top) }
                }
            }
        }
    }
}
