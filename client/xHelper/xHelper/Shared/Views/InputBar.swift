import SwiftUI
#if os(macOS)
import AppKit
#endif

struct InputBar: View {
    @Binding var text: String
    @Binding var isEditing: Bool
    let isStreaming: Bool
    var dismissKeyboardToken: UUID?
    let onSend: () -> Void
    let onStop: () -> Void
    /// 切换计划模式（work ↔ plan，胶囊点击切换）
    let onToggleMode: () -> Void
    /// 切换信任模式（跳过 L3 工具确认，胶囊点击切换）
    let onToggleTrustedMode: () -> Void
    @EnvironmentObject var fontSizeManager: FontSizeManager
    @State private var shakeOffset: CGFloat = 0
#if os(macOS)
    @FocusState private var isFocused: Bool
    @State private var idleTask: Task<Void, Never>?
#endif

    var body: some View {
        HStack(alignment: .center, spacing: 8) {
#if os(iOS)
            CustomTextView(
                text: $text,
                dismissKeyboardToken: dismissKeyboardToken,
                isDisabled: isStreaming,
                placeholder: "输入消息...",
                fontSize: fontSizeManager.scaledSize,
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
            let scaledSize = fontSizeManager.scaledSize
            let font = NSFont.systemFont(ofSize: scaledSize)
            let lineHeight = font.ascender + abs(font.descender) + font.leading
            TextEditor(text: $text)
                .offset(x: shakeOffset)
                .focused($isFocused)
                .onChange(of: text) {
                    idleTask?.cancel()
                    if !isEditing {
                        print("[InputBar] typing started")
                        isEditing = true
                    }
                    idleTask = Task {
                        try? await Task.sleep(for: .seconds(5))
                        guard !Task.isCancelled else { return }
                        print("[InputBar] typing idle 5s")
                        isEditing = false
                    }
                }
                .onChange(of: isFocused) { _, new in
                    if !new {
                        idleTask?.cancel()
                        print("[InputBar] focus lost")
                        isEditing = false
                    }
                }
                .onKeyPress(.return, phases: .down) { keyPress in
                    if keyPress.modifiers.contains(.shift) {
                        return .ignored
                    }
                    if (NSApp.keyWindow?.firstResponder?.value(forKey: "hasMarkedText") as? Bool) == true {
                        return .ignored
                    }
                    if isStreaming {
                        Task {
                            for _ in 0..<3 {
                                withAnimation(.easeInOut(duration: 0.06)) { shakeOffset = 6 }
                                try? await Task.sleep(nanoseconds: 60_000_000)
                                withAnimation(.easeInOut(duration: 0.06)) { shakeOffset = -6 }
                                try? await Task.sleep(nanoseconds: 60_000_000)
                            }
                            withAnimation(.easeInOut(duration: 0.06)) { shakeOffset = 0 }
                            ToastCenter.shared.show("请等待上一条消息的回复接收完毕")
                        }
                        return .handled
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
                .font(.system(size: scaledSize))
                .frame(minHeight: lineHeight * 4, maxHeight: lineHeight * 8)
                .fixedSize(horizontal: false, vertical: true)
                .padding(.leading, 12)
                .padding(.trailing, 12)
                .padding(.vertical, 14)
                .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 16))
                .disabled(isStreaming)

            // macOS 停止按钮：流式时显示，点击显式终止服务端 ReAct 循环
            if isStreaming {
                Button(action: onStop) {
                    Image(systemName: "stop.circle.fill")
                        .font(.title2)
                        .foregroundColor(.red)
                }
                .buttonStyle(.plain)
                .help("停止生成")
            }
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
        @Previewable @State var isEditing = false
        @Previewable @State var dismissKeyboardToken: UUID? = nil

        VStack {
            Spacer()
            InputBar(
                text: $text,
                isEditing: $isEditing,
                isStreaming: false,
                dismissKeyboardToken: dismissKeyboardToken,
                onSend: {},
                onStop: {},
                onToggleMode: {},
                onToggleTrustedMode: {}
            )
        }
        .frame(height: 200)
    }

    #Preview("Streaming") {
        @Previewable @State var text = "正在思考..."
        @Previewable @State var isEditing = false
        @Previewable @State var dismissKeyboardToken: UUID? = nil

        VStack {
            Spacer()
            InputBar(
                text: $text,
                isEditing: $isEditing,
            isStreaming: true,
            dismissKeyboardToken: dismissKeyboardToken,
            onSend: {},
            onStop: {},
            onToggleMode: {},
            onToggleTrustedMode: {}
        )
    }
    .frame(height: 200)
}
