import SwiftUI
#if os(macOS)
import AppKit
#endif

/// 双态「驾驶舱」输入栏（macOS）。
///
/// InputBar 只负责：区域摆放（状态行/编辑器/尾部动作）、收起↔展开动画、
/// 玻璃容器样式、聚焦/Return/打字空闲等编辑逻辑。具体按钮通过两个
/// @ViewBuilder 闭包从外部（ChatView）注入，各自带 ButtonVisibility 决定
/// 在收起态/展开态是否出现。增删按钮不改本文件。
struct InputBar<StatusRow: View, TrailingActions: View>: View {
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

    @EnvironmentObject var fontSizeManager: FontSizeManager
    @State private var shakeOffset: CGFloat = 0
#if os(macOS)
    @FocusState private var isFocused: Bool
    @State private var idleTask: Task<Void, Never>?
    /// 流式呼吸光晕相位（0…1），用 CGFloat 使阴影平滑呼吸。
    @State private var streamingGlow: CGFloat = 0
#endif

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
    }

    var body: some View {
#if os(macOS)
        macOSBody
#else
        iOSBody
#endif
    }

    // MARK: - 状态上下文

#if os(macOS)
    /// 展开态：聚焦 / 有草稿 / 流式中任一成立。否则收起。
    private var isExpanded: Bool {
        isFocused || !text.trimmingCharacters(in: .whitespaces).isEmpty || isStreaming
    }
#endif

    private var hasText: Bool {
        !text.trimmingCharacters(in: .whitespaces).isEmpty
    }

    private var context: InputBarContext {
#if os(macOS)
        InputBarContext(
            isCollapsed: !isExpanded,
            isStreaming: isStreaming,
            canSend: hasText,
            isFocused: isFocused
        )
#else
        InputBarContext(
            isCollapsed: false,
            isStreaming: isStreaming,
            canSend: hasText,
            isFocused: true
        )
#endif
    }

    // MARK: - macOS 驾驶舱

#if os(macOS)
    private var macOSBody: some View {
        VStack(spacing: 0) {
            // 展开态状态行
            if isExpanded {
                HStack(spacing: 8) {
                    statusRow(context)
                    Spacer()
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(.horizontal, 12)
                .padding(.top, 6)
                .transition(.opacity.combined(with: .move(edge: .top)))
            }

            ZStack {
                // 编辑器常驻（保证 .focused 始终有绑定目标，修复收起态点击后无法输入）；
                // 收起时压缩到 0 高度 + 裁剪，不占布局空间。
                HStack(alignment: .center, spacing: 4) {
                    editor
                    if isExpanded {
                        trailingActions(context)
                    }
                }
                .frame(height: isExpanded ? nil : 0, alignment: .bottom)
                .clipped()

                // 收起态占位条：覆盖在零高度编辑器上方，点任意空白聚焦展开
                if !isExpanded {
                    HStack(spacing: 8) {
                        statusRow(context)
                        Text("输入消息…")
                            .font(.system(size: fontSizeManager.scaledSize))
                            .foregroundColor(.secondary)
                            .lineLimit(1)
                            .truncationMode(.tail)
                        Spacer(minLength: 8)
                        trailingActions(context)
                    }
                    .padding(.leading, 12)
                    .padding(.trailing, 8)
                    .padding(.vertical, 8)
                    .frame(minHeight: 38)
                    .contentShape(Rectangle())
                    .onTapGesture {
                        print("[InputBar] collapsed bar tapped -> focus")
                        isFocused = true
                    }
                    .transition(.opacity)
                }
            }
        }
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
        .frame(maxWidth: 720)
        .padding(.bottom, 24)
        .animation(.spring(response: 0.35, dampingFraction: 0.82), value: isExpanded)
        .animation(.easeInOut(duration: 0.4), value: containerTint)
        .onChange(of: isExpanded) { _, new in
            print("[InputBar] isExpanded -> \(new) (focused=\(isFocused) hasText=\(hasText) streaming=\(isStreaming))")
        }
        .onChange(of: isStreaming) { _, streaming in
            print("[InputBar] isStreaming -> \(streaming)")
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
                print("[InputBar] isFocused -> \(new)")
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
            .frame(minHeight: lineHeight, maxHeight: lineHeight * 8)
            .fixedSize(horizontal: false, vertical: true)
            .background(InputBarScrollHider())
            .padding(.leading, 8)
            .padding(.trailing, 8)
            .padding(.vertical, 6)
            .disabled(isStreaming)
    }
#endif

    // MARK: - iOS 分支（保持原交互，按钮由外部注入）

#if os(iOS)
    private var iOSBody: some View {
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
#endif
}

#if os(macOS)
/// 隐藏 TextEditor 内部 NSScrollView 的垂直滚动条。
/// 保留 maxHeight 高度上限（输入栏不无限长高），内容超出上限后仍可
/// 内部滚动（触控板/滚轮），但看不到可见滚动条。
struct InputBarScrollHider: NSViewRepresentable {
    func makeNSView(context: Context) -> NSView {
        NSView()
    }

    func updateNSView(_ nsView: NSView, context: Context) {
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
            ModePill(context: ctx, isPlanMode: false) {}
            TrustedPill(context: ctx, trusted: false) {}
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
            ModePill(context: ctx, isPlanMode: true) {}
            TrustedPill(context: ctx, trusted: true) {}
        } trailingActions: { ctx in
            SendStopButton(context: ctx, isStreaming: true, canSend: true, onSend: {}, onStop: {})
        }
    }
    .frame(height: 260)
    .environmentObject(FontSizeManager.shared)
}
