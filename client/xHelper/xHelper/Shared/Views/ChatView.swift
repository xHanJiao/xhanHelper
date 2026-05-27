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

            // 记忆 & 笔记引用按钮
            let hasMemory = viewModel.lastMemoryIDs?.isEmpty == false
            let hasNote = viewModel.lastNoteIDs?.isEmpty == false
            if hasMemory || hasNote {
                HStack(spacing: 8) {
                    if hasMemory {
                        Button {
                            viewModel.showMemoryPopup = true
                        } label: {
                            HStack {
                                Image(systemName: "brain")
                                Text("\(viewModel.lastMemoryIDs!.count) 条回忆")
                                    .font(.caption)
                            }
                            .frame(maxWidth: .infinity)
                            .foregroundColor(.secondary)
                            .padding(.vertical, 6)
                            .background(Color.gray.opacity(0.1))
                            .cornerRadius(12)
                        }
                        .sheet(isPresented: $viewModel.showMemoryPopup) {
                            MemoryPopupView(memoryIDs: viewModel.lastMemoryIDs!, apiClient: APIClient(baseURL: baseURL, token: token))
                        }
                    }
                    if hasNote {
                        Button {
                            viewModel.showNotePopup = true
                        } label: {
                            HStack {
                                Image(systemName: "note.text")
                                Text("\(viewModel.lastNoteIDs!.count) 条笔记")
                                    .font(.caption)
                            }
                            .frame(maxWidth: .infinity)
                            .foregroundColor(.secondary)
                            .padding(.vertical, 6)
                            .background(Color.gray.opacity(0.1))
                            .cornerRadius(12)
                        }
                        .sheet(isPresented: $viewModel.showNotePopup) {
                            NotePopupView(noteIDs: viewModel.lastNoteIDs!, apiClient: APIClient(baseURL: baseURL, token: token))
                        }
                    }
                }
                .padding(.horizontal)
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
        .toolbar {
                ToolbarItem(placement: .navigationBarTrailing) {
                    Button { viewModel.showChatSettings = true } label: {
                        Image(systemName: "slider.horizontal.3")
                            .foregroundColor(.secondary)
                    }
                }
            }
            .sheet(isPresented: $viewModel.showChatSettings) {
                NavigationStack {
                    Form {
                        Section("模型") {
                            Toggle("Deep Mode (Pro)", isOn: $viewModel.deepMode)
                            Toggle("Thinking", isOn: $viewModel.thinkingMode)
                        }
                        Section("参数") {
                            VStack(alignment: .leading, spacing: 8) {
                                Text("Temperature: \(viewModel.temperature.map { String(format: "%.1f", $0) } ?? "默认")")
                                    .font(.subheadline)
                                Slider(value: Binding(get: { viewModel.temperature ?? 1.0 }, set: { viewModel.temperature = $0 }), in: 0...2, step: 0.1)
                                Button("恢复默认") { viewModel.temperature = nil }
                                    .font(.caption)
                            }
                            VStack(alignment: .leading, spacing: 8) {
                                Text("Max Tokens: \(viewModel.maxTokens.map { String($0) } ?? "默认")")
                                    .font(.subheadline)
                                Slider(value: Binding(get: { Double(viewModel.maxTokens ?? 4096) }, set: { viewModel.maxTokens = Int($0) }), in: 100...32000, step: 100)
                                Button("恢复默认") { viewModel.maxTokens = nil }
                                    .font(.caption)
                            }
                        }
                    }
                    .navigationTitle("对话参数")
                    .navigationBarTitleDisplayMode(.inline)
                    .toolbar {
                        ToolbarItem(placement: .confirmationAction) {
                            Button("完成") { viewModel.showChatSettings = false }
                        }
                    }
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
