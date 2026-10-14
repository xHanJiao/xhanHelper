import SwiftUI
#if os(macOS)
import AppKit
#endif

/// 双态「驾驶舱」输入栏（macOS）。
///
/// InputBar 只负责：区域摆放（状态行/展开行/编辑器/尾部动作）、收起↔展开动画、
/// 玻璃容器样式、聚焦/Return/打字空闲等编辑逻辑。具体按钮通过多个
/// @ViewBuilder 闭包从外部（ChatView）注入，各自带 ButtonVisibility 决定
/// 在收起态/展开态是否出现。增删按钮不改本文件。
struct InputBar<StatusRow: View, StatusRowExtras: View, TrailingActions: View, BottomLeading: View, AttachmentTraySlot: View>: View {
    @Binding var text: String
    @Binding var isEditing: Bool
    let isStreaming: Bool
    var dismissKeyboardToken: UUID?
    /// 容器描边/光晕色（随会话模式由外部计算传入）。
    var containerTint: Color
    /// 已挂附件（草稿区缩略图）。仅用于计算 canSend——只发图不写字也要能发。
    var hasAttachments: Bool = false
    /// 输入框聚焦变化回调：⌘V 粘贴要在共享同一 VM 的主视图/PiP 两个 ChatView 之间
    /// 认出"是谁在输入"，所以焦点态除了全局标记还要往上传一份。
    var onFocusChange: ((Bool) -> Void)? = nil
    /// ⌃C（输入框聚焦时）：清空草稿（文字由 text 绑定自理，图片由外部一并清）。
    var onClearAll: (() -> Void)? = nil
    let onSend: () -> Void
    let onStop: () -> Void
    /// Tab（仅输入框聚焦时）：循环计划模式（关/计划）。
    let onToggleMode: () -> Void
    /// 状态行胶囊（收起态与展开态都渲染，显示与否由各按钮自声明）。
    let statusRow: (InputBarContext) -> StatusRow
    /// 状态行上方多出的一行（折叠的更多操作；不注入时为空视图，不占空间）。
    let statusRowExtras: (InputBarContext) -> StatusRowExtras
    /// 尾部动作按钮（🎤/发送/停止等，位置随态变化）。
    let trailingActions: (InputBarContext) -> TrailingActions
    /// 底部左侧槽（上下文圆环等；不注入时为空视图）。
    let bottomLeading: (InputBarContext) -> BottomLeading
    /// 编辑器下方的附件缩略图托盘（不注入时为空视图）。
    let attachmentTray: (InputBarContext) -> AttachmentTraySlot

    @EnvironmentObject var fontSizeManager: FontSizeManager
    @State private var shakeOffset: CGFloat = 0
#if os(macOS)
    @FocusState private var isFocused: Bool
    @State private var idleTask: Task<Void, Never>?
    /// 流式呼吸光晕相位（0…1），用 CGFloat 使阴影平滑呼吸。
    @State private var streamingGlow: CGFloat = 0
#endif

    /// 便捷 init（无展开行 / 底部左侧槽 / 附件托盘）。
    init(
        text: Binding<String>,
        isEditing: Binding<Bool>,
        isStreaming: Bool,
        dismissKeyboardToken: UUID? = nil,
        containerTint: Color = Color("appAccent"),
        onSend: @escaping () -> Void,
        onStop: @escaping () -> Void,
        onToggleMode: @escaping () -> Void = {},
        @ViewBuilder         statusRow: @escaping (InputBarContext) -> StatusRow,
        @ViewBuilder trailingActions: @escaping (InputBarContext) -> TrailingActions
    ) where BottomLeading == EmptyView, AttachmentTraySlot == EmptyView, StatusRowExtras == EmptyView {
        self.init(
            text: text,
            isEditing: isEditing,
            isStreaming: isStreaming,
            dismissKeyboardToken: dismissKeyboardToken,
            containerTint: containerTint,
            onSend: onSend,
            onStop: onStop,
            onToggleMode: onToggleMode,
            statusRow: statusRow,
            statusRowExtras: { _ in EmptyView() },
            trailingActions: trailingActions,
            bottomLeading: { _ in EmptyView() },
            attachmentTray: { _ in EmptyView() }
        )
    }

    /// 完整 init（可注入展开行、底部左侧槽、附件托盘）。
    init(
        text: Binding<String>,
        isEditing: Binding<Bool>,
        isStreaming: Bool,
        dismissKeyboardToken: UUID? = nil,
        containerTint: Color = Color("appAccent"),
        hasAttachments: Bool = false,
        onFocusChange: ((Bool) -> Void)? = nil,
        onClearAll: (() -> Void)? = nil,
        onSend: @escaping () -> Void,
        onStop: @escaping () -> Void,
        onToggleMode: @escaping () -> Void = {},
        @ViewBuilder statusRow: @escaping (InputBarContext) -> StatusRow,
        @ViewBuilder statusRowExtras: @escaping (InputBarContext) -> StatusRowExtras,
        @ViewBuilder trailingActions: @escaping (InputBarContext) -> TrailingActions,
        @ViewBuilder bottomLeading: @escaping (InputBarContext) -> BottomLeading,
        @ViewBuilder attachmentTray: @escaping (InputBarContext) -> AttachmentTraySlot
    ) {
        self._text = text
        self._isEditing = isEditing
        self.isStreaming = isStreaming
        self.dismissKeyboardToken = dismissKeyboardToken
        self.containerTint = containerTint
        self.hasAttachments = hasAttachments
        self.onFocusChange = onFocusChange
        self.onClearAll = onClearAll
        self.onSend = onSend
        self.onStop = onStop
        self.onToggleMode = onToggleMode
        self.statusRow = statusRow
        self.statusRowExtras = statusRowExtras
        self.trailingActions = trailingActions
        self.bottomLeading = bottomLeading
        self.attachmentTray = attachmentTray
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

    private var canSendNow: Bool {
        hasText || hasAttachments
    }

    private var context: InputBarContext {
        InputBarContext(
            isStreaming: isStreaming,
            canSend: canSendNow
        )
    }

    // MARK: - macOS 输入栏

#if os(macOS)
    @State private var barWidth: CGFloat = 720

    private var macOSBody: some View {
        VStack(alignment: .trailing, spacing: 0) {
            twoRowLayout
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
            // 整颗胶囊（含 14/6 留白）都可点聚焦：留白区点击走此手势把焦点移入编辑器，
            // 按钮/编辑器本体由子视图优先消费（SwiftUI 子视图手势优先），不受影响。
            .contentShape(RoundedRectangle(cornerRadius: 20, style: .continuous))
            .onTapGesture {
                guard !isStreaming else { return }
                isFocused = true
            }
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

    /// 两行布局：编辑器全宽在上，附件托盘居中，模式/发送按钮右对齐在下。
    /// 折叠的更多操作（statusRowExtras）展开时插在按钮行上方，同一胶囊内，
    /// 宽度受胶囊约束——窄窗也不会戳出框。
    private var twoRowLayout: some View {
        VStack(spacing: 6) {
            editor
            attachmentTray(context)
            statusRowExtras(context)
            HStack(spacing: 4) {
                bottomLeading(context)
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
        let textWidth = max(barWidth - 12, 1)
        let contentHeight = measuredHeight(text, width: textWidth, font: font)
        let editorHeight = min(max(contentHeight, lineHeight), lineHeight * 4)
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
                // 供 ⌘V 粘贴判定（macOS 的 NSEvent monitor 拿不到 SwiftUI 的焦点态）
                ChatInputFocus.shared.isFocused = new
                onFocusChange?(new)
                if !new {
                    idleTask?.cancel()
                    isEditing = false
                }
            }
            .onDisappear {
                // 输入框被移除（切页/收起）时焦点态不会自己变，留着会误判 ⌘V 该接管
                ChatInputFocus.shared.isFocused = false
            }
            // Return：单独按换行（放行给 TextEditor）；Shift+Return 发送。
            .onKeyPress(.return, phases: .down) { keyPress in
                guard keyPress.modifiers.contains(.shift) else { return .ignored }
                // IME 组合态下交给输入法确认。
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
                    return .handled
                }
                DispatchQueue.main.async {
                    onSend()
                }
                return .handled
            }
            // Tab（仅输入框聚焦时）：循环计划模式（关/计划）。焦点不在聊天输入框时（PiP 收起/浏览器/地址栏）不会触发。
            // 流式进行中不响应，避免回合中途误切模式（胶囊点击不受影响）。
            .onKeyPress(.tab, phases: .down) { _ in
                guard !isStreaming else { return .handled }
                onToggleMode()
                return .handled
            }
            // ⌃C（仅输入框聚焦时）：清空草稿（文字与待发图片）。⌘C（复制）不受影响。
            .onKeyPress(KeyEquivalent("c"), phases: .down) { keyPress in
                guard keyPress.modifiers.contains(.control) else { return .ignored }
                onClearAll?()
                return .handled
            }
            .scrollContentBackground(.hidden)
            // 多行溢出、框内滚动时隐藏滚动条；用 .never 而非 .hidden——.hidden 在
            // macOS 接上带滚轮鼠标后仍会把滚动条带回来，.never 才不看指点设备一律隐藏。
            .scrollIndicators(.never, axes: .vertical)
            .font(.system(size: scaledSize))
            .frame(height: editorHeight)
            .background(InputBarScrollHider(reapplyToken: "\(barWidth)-\(scaledSize)"))
            .padding(.leading, 6)
            .padding(.trailing, 6)
            .padding(.vertical, 6)
            .disabled(isStreaming)
    }

    /// 用独立 TextKit 栈按宽度算折行高度，不依赖 TextEditor 内部视图（其 ideal
    /// height 会被 SwiftUI 缓存，宽度变化后不重算）。
    private func measuredHeight(_ text: String, width: CGFloat, font: NSFont) -> CGFloat {
        let storage = NSTextStorage(attributedString: NSAttributedString(
            string: text.isEmpty ? " " : text,
            attributes: [.font: font]
        ))
        let container = NSTextContainer(size: CGSize(width: width, height: .greatestFiniteMagnitude))
        container.lineFragmentPadding = 0
        let manager = NSLayoutManager()
        manager.addTextContainer(container)
        storage.addLayoutManager(manager)
        manager.ensureLayout(for: container)
        return ceil(manager.usedRect(for: container).height)
    }
#endif

    // MARK: - iOS 分支（保持原交互，按钮由外部注入）

#if os(iOS)
    private var iOSBody: some View {
        VStack(alignment: .trailing, spacing: 0) {
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
/// 调整 TextEditor 内部的 AppKit 视图：把 NSTextView 的 `lineFragmentPadding` /
/// `textContainerInset` 归零，使真实文本可用宽 == `measuredHeight` 假设的宽（两者都用零内边距）。
/// 否则 AppKit 默认 lineFragmentPadding=5 会让真实折行比测量早 10pt，
/// 文字已折到第二行时测量仍算一行，输入框不加高（要多打 1~2 字才跳）。
/// 布局切换/宽度/字号变化会重建 TextEditor 的内部视图（恢复默认值），
/// 因此传入 reapplyToken：token 变化时 updateNSView 重新应用。
/// （滚动条隐藏不在此处，改由 TextEditor 上的 `.scrollIndicators(.never, axes: .vertical)` 负责。）
struct InputBarScrollHider: NSViewRepresentable {
    /// 变化即触发重新应用。
    var reapplyToken: String

    func makeNSView(context: Context) -> NSView {
        NSView()
    }

    func updateNSView(_ nsView: NSView, context: Context) {
        apply(from: nsView)
    }

    private func apply(from nsView: NSView) {
        DispatchQueue.main.async {
            guard let root = nsView.superview else { return }
            var queue: [NSView] = [root]
            while !queue.isEmpty {
                let view = queue.removeFirst()
                if let textView = view as? NSTextView {
                    textView.textContainerInset = .zero
                    textView.textContainer?.lineFragmentPadding = 0
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
            HStack(spacing: 4) {
                ModePill(context: ctx, mode: .off) {}
                TrustedPill(context: ctx, level: .off) {}
            }
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
            HStack(spacing: 4) {
                ModePill(context: ctx, mode: .normal) {}
                TrustedPill(context: ctx, level: .trusted) {}
            }
        } trailingActions: { ctx in
            SendStopButton(context: ctx, isStreaming: true, canSend: true, onSend: {}, onStop: {})
        }
    }
    .frame(height: 260)
    .environmentObject(FontSizeManager.shared)
}

#Preview("Agent Menu (expanded)") {
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
            HStack(spacing: 4) {
                ModePill(context: ctx, mode: .off) {}
                TrustedPill(context: ctx, level: .off) {}
                AgentPickerButton(
                    context: ctx,
                    activeAgentID: "agent-active",
                    activeAgentName: "生活管家",
                    agents: [
                        InputBarAgent(
                            id: "agent-active",
                            userId: "u1",
                            name: "生活管家",
                            systemPrompt: "负责生活记录的智能体",
                            summary: "记账、提醒、日程",
                            model: "deepseek-v4-flash",
                            paramsJson: nil,
                            createdAt: "2026-08-01T10:00:00Z",
                            active: true
                        ),
                        InputBarAgent(
                            id: "agent-reader",
                            userId: "u1",
                            name: "读书助手",
                            systemPrompt: "读书方法论的智能体",
                            summary: "读书、拆书、笔记",
                            model: "deepseek-v4-flash",
                            paramsJson: nil,
                            createdAt: "2026-07-20T10:00:00Z",
                            active: false
                        ),
                        InputBarAgent(
                            id: "agent-dev",
                            userId: "u1",
                            name: "代码评审官",
                            systemPrompt: "评审代码的智能体",
                            summary: "评审、重构、测试",
                            model: "deepseek-v4-pro",
                            paramsJson: nil,
                            createdAt: "2026-07-01T10:00:00Z",
                            active: false
                        ),
                        InputBarAgent(
                            id: "agent-translator",
                            userId: "u1",
                            name: "翻译官",
                            systemPrompt: "翻译校对的智能体",
                            summary: "翻译、润色、校对",
                            model: "deepseek-v4-flash",
                            paramsJson: nil,
                            createdAt: "2026-06-15T10:00:00Z",
                            active: false
                        )
                    ],
                    hasMore: true,
                    initiallyPresented: true
                )
                ModelPill(context: ctx, isDeep: false) {}
            }
        } trailingActions: { ctx in
            SendStopButton(context: ctx, isStreaming: false, canSend: ctx.canSend, onSend: {}, onStop: {})
        }
    }
    .frame(height: 460)
    .environmentObject(FontSizeManager.shared)
}
