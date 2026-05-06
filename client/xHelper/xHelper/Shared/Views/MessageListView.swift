import SwiftUI

struct MessageListView: View {
    @ObservedObject var viewModel: ChatViewModel

    var body: some View {
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(spacing: 12) {
                    ForEach(viewModel.messages) { msg in
                        if msg.role == "tool" {
                            ChatBubble(text: msg.content, isUser: false, isTool: true, messageId: msg.id)
                        } else if msg.role == "user" {
                            ChatBubble(text: msg.content, isUser: true, messageId: msg.id)
                        } else {
                            ChatBubble(text: msg.content, isUser: false, messageId: msg.id)
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
                if newCount > oldCount, let last = viewModel.messages.last, last.id != "__streaming__" {
                    withAnimation { proxy.scrollTo(last.id, anchor: .bottom) }
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
