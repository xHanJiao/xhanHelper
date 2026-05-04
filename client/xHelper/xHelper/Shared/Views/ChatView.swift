import SwiftUI

struct ChatView: View {
    @ObservedObject var viewModel: ChatViewModel
    @State private var inputText = ""
    let agentId: String?

    let baseURL: String
    let token: String

    var body: some View {
        ZStack {
            VStack(spacing: 0) {
            // 消息列表
            MessageListView(viewModel: viewModel)

            // 错误提示
            if let err = viewModel.errorMessage {
                Text(err).font(.caption).foregroundColor(.red).padding(.horizontal)
            }

            // 记忆引用按钮
            if let mids = viewModel.lastMemoryIDs, !mids.isEmpty {
                Button {
                    viewModel.showMemoryPopup = true
                } label: {
                    HStack {
                        Image(systemName: "brain")
                        Text("基于 \(mids.count) 条回忆")
                            .font(.caption)
                    }
                    .foregroundColor(.secondary)
                    .padding(.horizontal, 12)
                    .padding(.vertical, 6)
                    .background(Color.gray.opacity(0.1))
                    .cornerRadius(12)
                }
                .padding(.horizontal)
                .sheet(isPresented: $viewModel.showMemoryPopup) {
                    MemoryPopupView(memoryIDs: mids, apiClient: APIClient(baseURL: baseURL, token: token))
                }
            }

            // 输入栏
            InputBar(
                text: $inputText,
                isStreaming: viewModel.isStreaming,
                onSend: {
                    guard !inputText.trimmingCharacters(in: .whitespaces).isEmpty else { return }
                    viewModel.sendMessage(inputText, baseURL: baseURL, token: token, agentId: agentId)
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
        .navigationTitle(viewModel.conversationTitle ?? "新对话")
        .navigationBarTitleDisplayMode(.inline)

            // 动态图弹窗
            PlanModalView(viewModel: viewModel)

            // 最小化浮动按钮
            if viewModel.isPlanMinimized, !viewModel.planNodes.isEmpty {
                VStack {
                    Spacer()
                    HStack {
                        Spacer()
                        Button {
                            withAnimation { viewModel.isPlanMinimized = false }
                        } label: {
                            Image(systemName: "square.grid.2x2")
                                .font(.title3)
                                .padding(12)
                                .background(Color(.systemBackground))
                                .cornerRadius(12)
                                .shadow(radius: 4)
                        }
                        .padding()
                    }
                }
            }
        }
    }
}
