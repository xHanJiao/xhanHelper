import SwiftUI

struct MessageListView: View {
    @ObservedObject var viewModel: ChatViewModel
    @State private var pendingScroll: DispatchWorkItem?

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
                        scheduleScroll(proxy: proxy, id: anchor, anchor: .top)
                        viewModel.anchorMessageID = nil
                    } else if let last = viewModel.messages.last, last.id != "__streaming__" {
                        scheduleScroll(proxy: proxy, id: last.id, anchor: .bottom)
                    }
                }
            }
            .onChange(of: viewModel.messages.last?.content) {
                if let last = viewModel.messages.last {
                    scheduleScroll(proxy: proxy, id: last.id, anchor: .top)
                }
            }
        }
    }

    private func scheduleScroll(proxy: ScrollViewProxy, id: String, anchor: UnitPoint, delay: TimeInterval = 0.05) {
        pendingScroll?.cancel()
        let work = DispatchWorkItem {
            withAnimation { proxy.scrollTo(id, anchor: anchor) }
        }
        pendingScroll = work
        DispatchQueue.main.asyncAfter(deadline: .now() + delay, execute: work)
    }
}
