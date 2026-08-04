import SwiftUI
#if os(macOS)
import AppKit
#endif

/// 双态「驾驶舱」输入栏（macOS）。
///
/// InputBar 只负责：区域摆放（状态行/编辑器/尾部动作）、收起↔展开动画、
/// 玻璃容器样式、聚焦/Return/打字空闲等编辑逻辑。具体按钮通过两个
/// @ViewBuilder 闭包从外部（ChatView）注入，各自带 ButtonVisibility 决定
/// 在收起态/展开态是否出现；上缘便签面板通过第三个闭包注入（锚定胶囊右上缘，
/// 不注入时为空视图）。增删按钮不改本文件。
struct InputBar<StatusRow: View, TrailingActions: View, TopNote: View>: View {
    @Binding var text: String
    @Binding var isEditing: Bool
    let isStreaming: Bool
    var dismissKeyboardToken: UUID?
    /// 容器描边/光晕色（随会话模式由外部计算传入）。
    var containerTint: Color
    let onSend: () -> Void
    let onStop: () -> Void
    /// 状态行胶囊（收起态与展开态都渲染，显示与否由各按钮自声明）。
    let statusRow: (InputBarContext) -> StatusRow
    /// 尾部动作按钮（🎤/发送/停止等，位置随态变化）。
    let trailingActions: (InputBarContext) -> TrailingActions
    /// 上缘便签面板（可选注入，锚定 720 胶囊右上缘；不注入时为空视图）。
    let topTrailingNote: (InputBarContext) -> TopNote

    @EnvironmentObject var fontSizeManager: FontSizeManager
    @State private var shakeOffset: CGFloat = 0
#if os(macOS)
    @FocusState private var isFocused: Bool
    @State private var idleTask: Task<Void, Never>?
    /// 流式呼吸光晕相位（0…1），用 CGFloat 使阴影平滑呼吸。
    @State private var streamingGlow: CGFloat = 0
#endif

    /// 便捷 init（无便签面板，TopNote 为 EmptyView）。
    init(
        text: Binding<String>,
        isEditing: Binding<Bool>,
        isStreaming: Bool,
        dismissKeyboardToken: UUID? = nil,
        containerTint: Color = Color("appAccent"),
        onSend: @escaping () -> Void,
        onStop: @escaping () -> Void,
        @ViewBuilder statusRow: @escaping (InputBarContext) -> StatusRow,
        @ViewBuilder trailingActions: @escaping (InputBarContext) -> TrailingActions
    ) where TopNote == EmptyView {
        self.init(
            text: text,
            isEditing: isEditing,
            isStreaming: isStreaming,
            dismissKeyboardToken: dismissKeyboardToken,
            containerTint: containerTint,
            onSend: onSend,
            onStop: onStop,
            statusRow: statusRow,
            trailingActions: trailingActions,
            topTrailingNote: { _ in EmptyView() }
        )
    }

    /// 完整 init（可注入上缘便签面板）。
    init(
        text: Binding<String>,
        isEditing: Binding<Bool>,
        isStreaming: Bool,
        dismissKeyboardToken: UUID? = nil,
        containerTint: Color = Color("appAccent"),
        onSend: @escaping () -> Void,
        onStop: @escaping () -> Void,
        @ViewBuilder statusRow: @escaping (InputBarContext) -> StatusRow,
        @ViewBuilder trailingActions: @escaping (InputBarContext) -> TrailingActions,
        @ViewBuilder topTrailingNote: @escaping (InputBarContext) -> TopNote
    ) {
        self._text = text
        self._isEditing = isEditing
        self.isStreaming = isStreaming
        self.dismissKeyboardToken = dismissKeyboardToken
        self.containerTint = containerTint
        self.onSend = onSend
        self.onStop = onStop
        self.statusRow = statusRow
        self.trailingActions = trailingActions
        self.topTrailingNote = topTrailingNote
    }

    var body: some View {
#if os(macOS)
        macOSBody
#else
        iOSBody
#endif
    }

    // MARK: - 状态上下文

    private var hasText: Bool {
        !text.trimmingCharacters(in: .whitespaces).isEmpty
    }

    private var context: InputBarContext {
        InputBarContext(
            isStreaming: isStreaming,
            canSend: hasText
        )
    }

    // MARK: - macOS 输入栏

#if os(macOS)
    @State private var barWidth: CGFloat = Layout.inputBarTwoRowThreshold

    private var macOSBody: some View {
        VStack(alignment: .trailing, spacing: 0) {
            // 上缘便签：内容自适应，最宽不超过输入框一半；右缘距胶囊右缘 10pt。
            // 无便签时为空视图，不占空间。
            topTrailingNote(context)
                .frame(maxWidth: barWidth / 2, alignment: .trailing)
                .padding(.trailing, 10)
            Group {
                if barWidth < Layout.inputBarTwoRowThreshold {
                    twoRowLayout
                } else {
                    oneRowLayout
                }
            }
            .background(
                GeometryReader { proxy in
                    Color.clear
                        .onAppear { barWidth = proxy.size.width }
                        .onChange(of: proxy.size.width) { _, w in barWidth = w }
                }
            )
            .padding(.horizontal, 14)
            .padding(.vertical, 6)
            .background(
                RoundedRectangle(cornerRadius: 20, style: .continuous)
                    .fill(.regularMaterial)
            )
            .shadow(
                color: containerTint.opacity(0.18 + streamingGlow * 0.14),
                radius: 10 + streamingGlow * 7
            )
        }
        .frame(maxWidth: 720, alignment: .trailing)
        .padding(.horizontal, Layout.chatHorizontalPadding)
        .padding(.bottom, 24)
        .frame(maxWidth: .infinity, alignment: .center)
        .animation(.easeInOut(duration: 0.4), value: containerTint)
        .onChange(of: isStreaming) { _, streaming in
            if streaming {
                withAnimation(.easeInOut(duration: 1.6).repeatForever(autoreverses: true)) {
                    streamingGlow = 1
                }
            } else {
                withAnimation(.easeOut(duration: 0.3)) {
                    streamingGlow = 0
                }
            }
        }
    }

    /// 单行布局：编辑器 + 模式按钮 + 发送（宽度充足时）。
    private var oneRowLayout: some View {
        HStack(alignment: .center, spacing: 4) {
            editor
            statusRow(context)
            trailingActions(context)
        }
    }

    /// 两行布局：编辑器全宽在上，模式/发送按钮右对齐在下（宽度窄时）。
    private var twoRowLayout: some View {
        VStack(spacing: 6) {
            editor
            HStack(spacing: 4) {
                Spacer(minLength: 0)
                statusRow(context)
                trailingActions(context)
            }
        }
    }

    private var editor: some View {
        let scaledSize = fontSizeManager.scaledSize
        let font = NSFont.systemFont(ofSize: scaledSize)
        let lineHeight = font.ascender + abs(font.descender) + font.leading
        return TextEditor(text: $text)
            .offset(x: shakeOffset)
            .focused($isFocused)
            .onChange(of: text) {
                idleTask?.cancel()
                if !isEditing {
                    isEditing = true
                }
                idleTask = Task {
                    try? await Task.sleep(for: .seconds(5))
                    guard !Task.isCancelled else { return }
                    isEditing = false
                }
            }
            .onChange(of: isFocused) { _, new in
                if !new {
                    idleTask?.cancel()
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
            .frame(minHeight: lineHeight, maxHeight: lineHeight * 8)
            .fixedSize(horizontal: false, vertical: true)
            .background(InputBarScrollHider(reapplyToken: barWidth))
            .padding(.leading, 6)
            .padding(.trailing, 6)
            .padding(.vertical, 6)
            .disabled(isStreaming)
    }
#endif

    // MARK: - iOS 分支（保持原交互，按钮由外部注入）

#if os(iOS)
    private var iOSBody: some View {
        VStack(alignment: .trailing, spacing: 0) {
            topTrailingNote(context)
                .frame(maxWidth: 320, alignment: .trailing)
                .padding(.trailing, 10)
            HStack(alignment: .center, spacing: 8) {
                CustomTextView(
                    text: $text,
                    dismissKeyboardToken: dismissKeyboardToken,
                    isDisabled: isStreaming,
                    placeholder: "输入消息...",
                    fontSize: fontSizeManager.scaledSize,
                    onSubmit: {
                        if hasText {
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

                trailingActions(context)
            }
            .padding(.horizontal, Layout.chatHorizontalPadding)
            .padding(.vertical, 12)
        }
        .frame(maxWidth: .infinity)
    }
#endif
}

#if os(macOS)
/// 隐藏 TextEditor 内部 NSScrollView 的垂直滚动条。
/// 保留 maxHeight 高度上限（输入栏不无限长高），内容超出上限后仍可
/// 内部滚动（触控板/滚轮），但看不到可见滚动条。
/// 布局切换/宽度变化会重建 TextEditor 的 NSScrollView（恢复默认滚动条），
/// 因此传入 reapplyToken：token 变化时 updateNSView 重新应用隐藏。
struct InputBarScrollHider: NSViewRepresentable {
    /// 变化即触发重新隐藏（传 barWidth 或"是否两行"）。
    var reapplyToken: CGFloat

    func makeNSView(context: Context) -> NSView {
        NSView()
    }

    func updateNSView(_ nsView: NSView, context: Context) {
        hideScroller(from: nsView)
    }

    private func hideScroller(from nsView: NSView) {
        DispatchQueue.main.async {
            guard let root = nsView.superview else { return }
            var queue: [NSView] = [root]
            while !queue.isEmpty {
                let view = queue.removeFirst()
                if let scrollView = view as? NSScrollView {
                    scrollView.hasVerticalScroller = false
                    scrollView.scrollerStyle = .overlay
                }
                queue.append(contentsOf: view.subviews)
            }
        }
    }
}
#endif

// MARK: - Previews

#Preview("Idle") {
    @Previewable @State var text = ""
    @Previewable @State var isEditing = false

    VStack {
        Spacer()
        InputBar(
            text: $text,
            isEditing: $isEditing,
            isStreaming: false,
            onSend: {},
            onStop: {}
        ) { ctx in
            CollapsibleModeControls(
                context: ctx,
                isPlanMode: false,
                trustedMode: false,
                onToggleMode: {},
                onToggleTrusted: {}
            )
        } trailingActions: { ctx in
            SendStopButton(context: ctx, isStreaming: false, canSend: ctx.canSend, onSend: {}, onStop: {})
        }
    }
    .frame(height: 260)
    .environmentObject(FontSizeManager.shared)
}

#Preview("Streaming") {
    @Previewable @State var text = "正在思考..."
    @Previewable @State var isEditing = false

    VStack {
        Spacer()
        InputBar(
            text: $text,
            isEditing: $isEditing,
            isStreaming: true,
            onSend: {},
            onStop: {}
        ) { ctx in
            CollapsibleModeControls(
                context: ctx,
                isPlanMode: true,
                trustedMode: true,
                onToggleMode: {},
                onToggleTrusted: {}
            )
        } trailingActions: { ctx in
            SendStopButton(context: ctx, isStreaming: true, canSend: true, onSend: {}, onStop: {})
        }
    }
    .frame(height: 260)
    .environmentObject(FontSizeManager.shared)
}
