import SwiftUI

struct InputBar: View {
    @Binding var text: String
    let isStreaming: Bool
    @FocusState.Binding var isFocused: Bool
    let onSend: () -> Void
    let onStop: () -> Void

    var body: some View {
        HStack(alignment: .center, spacing: 8) {
            TextField("输入消息...", text: $text, axis: .vertical)
                .textFieldStyle(.plain)
                .focused($isFocused)
                .submitLabel(.send)
                .onSubmit { onSend() }
                .lineLimit(1...15)
                .padding(.leading, 12)
                .padding(.trailing, 12)
                .padding(.vertical, 8)
                .background(Color("surfaceInput"))
                .cornerRadius(16)
                .disabled(isStreaming)
                .onChange(of: text) { oldValue, newValue in
                    let addedCount = newValue.count - oldValue.count
                    let addedNewlines = newValue.filter { $0 == "\n" }.count
                                      - oldValue.filter { $0 == "\n" }.count
                    // 仅"净增 1 个字符且是换行" = 回车键 → 发送；粘贴(多字符)不触发
                    if addedCount == 1, addedNewlines == 1 {
                        text = oldValue
                        if !text.trimmingCharacters(in: .whitespaces).isEmpty {
                            onSend()
                        }
                    }
                }

            if isStreaming {
                Button(action: onStop) {
                    Image(systemName: "stop.circle.fill")
                        .font(.title2)
                        .foregroundColor(.red)
                }
                .buttonStyle(.plain)
            } else {
                Button(action: onSend) {
                    Image(systemName: "arrow.up.circle.fill")
                        .font(.title2)
                        .foregroundColor(text.trimmingCharacters(in: .whitespaces).isEmpty ? .gray : Color("appAccent"))
                }
                .buttonStyle(.plain)
                .disabled(text.trimmingCharacters(in: .whitespaces).isEmpty)
            }
        }
        .padding(.horizontal)
        .padding(.vertical, 8)
        .background(.regularMaterial)
    }
}

#Preview {
    @Previewable @State var text = ""
    @Previewable @FocusState var isFocused: Bool

    VStack {
        Spacer()
        InputBar(
            text: $text,
            isStreaming: false,
            isFocused: $isFocused,
            onSend: {},
            onStop: {}
        )
    }
    .frame(height: 200)
}

#Preview("Streaming") {
    @Previewable @State var text = "正在思考..."
    @Previewable @FocusState var isFocused: Bool

    VStack {
        Spacer()
        InputBar(
            text: $text,
            isStreaming: true,
            isFocused: $isFocused,
            onSend: {},
            onStop: {}
        )
    }
    .frame(height: 200)
}
