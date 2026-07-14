import SwiftUI

struct InputBar: View {
    @Binding var text: String
    let isStreaming: Bool
    var dismissKeyboardToken: UUID?
    let onSend: () -> Void
    let onStop: () -> Void

    var body: some View {
        HStack(alignment: .center, spacing: 8) {
            CustomTextView(
                text: $text,
                dismissKeyboardToken: dismissKeyboardToken,
                isDisabled: isStreaming,
                placeholder: "输入消息...",
                onSubmit: {
                    if !text.trimmingCharacters(in: .whitespaces).isEmpty {
                        onSend()
                    }
                }
            )
            .padding(.leading, 12)
            .padding(.trailing, 12)
            .padding(.vertical, 8)
            .background(Color("surfaceInput"))
            .cornerRadius(16)
            .disabled(isStreaming)

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
    }
}

#Preview {
    @Previewable @State var text = ""
    @Previewable @State var dismissKeyboardToken: UUID? = nil

    VStack {
        Spacer()
        InputBar(
            text: $text,
            isStreaming: false,
            dismissKeyboardToken: dismissKeyboardToken,
            onSend: {},
            onStop: {}
        )
    }
    .frame(height: 200)
}

#Preview("Streaming") {
    @Previewable @State var text = "正在思考..."
    @Previewable @State var dismissKeyboardToken: UUID? = nil

    VStack {
        Spacer()
        InputBar(
            text: $text,
            isStreaming: true,
            dismissKeyboardToken: dismissKeyboardToken,
            onSend: {},
            onStop: {}
        )
    }
    .frame(height: 200)
}
