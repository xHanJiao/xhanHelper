import SwiftUI
#if os(macOS)
import AppKit
#endif

struct InputBar: View {
    @Binding var text: String
    let isStreaming: Bool
    var dismissKeyboardToken: UUID?
    let onSend: () -> Void
    let onStop: () -> Void

    var body: some View {
        HStack(alignment: .center, spacing: 8) {
#if os(iOS)
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
#else
            let font = NSFont.preferredFont(forTextStyle: .body)
            let lineHeight = font.ascender + abs(font.descender) + font.leading
            TextEditor(text: $text)
                .onKeyPress(.return, phases: .down) { keyPress in
                    if keyPress.modifiers.contains(.shift) {
                        return .ignored
                    }
                    if (NSApp.keyWindow?.firstResponder?.value(forKey: "hasMarkedText") as? Bool) == true {
                        return .ignored
                    }
                    if text.trimmingCharacters(in: .whitespaces).isEmpty {
                        return .ignored
                    }
                    DispatchQueue.main.async {
                        onSend()
                    }
                    return .handled
                }
                .scrollContentBackground(.hidden)
                .font(.body)
                .frame(minHeight: lineHeight * 4, maxHeight: lineHeight * 8)
                .fixedSize(horizontal: false, vertical: true)
                .padding(.leading, 12)
                .padding(.trailing, 12)
                .padding(.vertical, 14)
                .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 16))
                .disabled(isStreaming)
#endif

#if os(iOS)
            if isStreaming {
                Button(action: onStop) {
                    Image(systemName: "stop.circle.fill")
                        .font(.title2)
                        .foregroundColor(.red)
                }
                .buttonStyle(.plain)
            }
            if !isStreaming {
                Button(action: onSend) {
                    Image(systemName: "arrow.up.circle.fill")
                        .font(.title2)
                        .foregroundColor(text.trimmingCharacters(in: .whitespaces).isEmpty ? .gray : Color("appAccent"))
                }
                .buttonStyle(.plain)
                .disabled(text.trimmingCharacters(in: .whitespaces).isEmpty)
            }
#endif
        }
        .padding(.horizontal, Layout.chatHorizontalPadding)
        .padding(.vertical, 12)
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
