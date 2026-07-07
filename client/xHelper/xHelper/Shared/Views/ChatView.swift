import SwiftUI

struct ChatView: View {
    @ObservedObject var viewModel: ChatViewModel
    @State private var inputText = ""
    @FocusState private var isInputFocused: Bool
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


            // 输入栏
            InputBar(
                text: $inputText,
                isStreaming: viewModel.isStreaming,
                isFocused: $isInputFocused,
                onSend: {
                    guard !inputText.trimmingCharacters(in: .whitespaces).isEmpty else { return }
                    isInputFocused = false
                    viewModel.sendMessage(inputText, baseURL: baseURL, token: token, agentId: agentId)
                    inputText = ""
                },
                onStop: {
                    viewModel.stopStreaming()
                }
            )
            .layoutPriority(1)
        }
        }
    }
}
