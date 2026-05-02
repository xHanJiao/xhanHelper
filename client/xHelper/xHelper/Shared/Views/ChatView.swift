import SwiftUI

struct ChatView: View {
    @ObservedObject var viewModel: ChatViewModel
    @State private var inputText = ""

    let baseURL: String
    let token: String

    var body: some View {
        VStack(spacing: 0) {
            if !viewModel.planNodes.isEmpty {
                PlanGraphView(nodes: viewModel.planNodes, edges: viewModel.planEdges)
                    .id("plan")
            }

            // 消息列表
            ScrollViewReader { proxy in
                ScrollView {
                    LazyVStack(spacing: 12) {
                        ForEach(viewModel.messages) { msg in
                            if msg.role == "tool" {
                                ChatBubble(text: msg.content, isUser: false, isTool: true)
                            } else if msg.role == "user" {
                                ChatBubble(text: msg.content, isUser: true)
                            } else {
                                ChatBubble(text: msg.content, isUser: false)
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
                            .transition(.opacity)
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
                            .transition(.opacity)
                        }

                        if let tc = viewModel.currentToolCall {
                            ToolCallCard(toolCall: tc)
                                .id("tool_call")
                        }

                        if viewModel.isStreaming {
                            HStack {
                                ProgressView().scaleEffect(0.6)
                                Text("思考中...").font(.caption).foregroundColor(.secondary)
                                Spacer()
                            }
                            .padding(.horizontal)
                            .id("typing")
                        }
                    }
                    .padding(.vertical)
                }
                .onChange(of: viewModel.messages.count) {
                    if let last = viewModel.messages.last {
                        withAnimation { proxy.scrollTo(last.id, anchor: .bottom) }
                    }
                }
                .onChange(of: viewModel.currentToolCall?.function) {
                    withAnimation { proxy.scrollTo("tool_call", anchor: .bottom) }
                }
            }

            // 错误提示
            if let err = viewModel.errorMessage {
                Text(err).font(.caption).foregroundColor(.red).padding(.horizontal)
            }

            // 输入栏
            InputBar(
                text: $inputText,
                isStreaming: viewModel.isStreaming,
                onSend: {
                    guard !inputText.trimmingCharacters(in: .whitespaces).isEmpty else { return }
                    viewModel.sendMessage(inputText, baseURL: baseURL, token: token)
                    inputText = ""
                },
                onStop: {
                    viewModel.stopStreaming()
                }
            )
        }
        .toolbar {
                ToolbarItem(placement: .navigationBarTrailing) {
                    Button(action: { viewModel.deepMode.toggle() }) {
                        Image(systemName: viewModel.deepMode ? "brain.head.profile.fill" : "brain.head.profile")
                            .foregroundColor(viewModel.deepMode ? .orange : .secondary)
                    }
                    .help(viewModel.deepMode ? "深度思考模式 (Pro)" : "标准模式 (Flash)")
                }
            }
        .navigationTitle(viewModel.conversationId ?? "新对话")
        .navigationBarTitleDisplayMode(.inline)
    }
}
