#if os(macOS)
import SwiftUI
import AppKit

/// 全屏输入面板（macOS）：盖住聊天中栏整高，供写长文本（手账等）。
/// 键位与普通输入栏**完全一致**：Enter 发送、Shift+Enter 换行、Esc 收起（输入法组词中只取消拼音）。
/// 草稿由外部绑定（与普通输入栏共用同一份 `draftText`），发送/停止也走外部回调。
struct ExpandedComposer: View {
    @Binding var text: String
    let isStreaming: Bool
    let canSend: Bool
    let onSend: () -> Void
    let onStop: () -> Void
    let onClose: () -> Void
    /// ⌃C：清空草稿（与普通输入栏一致）。
    let onClearAll: () -> Void
    /// Tab：循环计划模式（与普通输入栏一致；流式中不响应）。
    let onToggleMode: () -> Void
    /// 焦点变化要往上报：⌘V 贴图靠它认「哪个 ChatView 在输入」。
    let onFocusChange: (Bool) -> Void

    @EnvironmentObject private var fontSizeManager: FontSizeManager
    @FocusState private var isFocused: Bool

    /// 输入法组词中：Enter 是确认拼音、Esc 是取消拼音，都该放行给输入法。
    private var isIMEComposing: Bool {
        (NSApp.keyWindow?.firstResponder?.value(forKey: "hasMarkedText") as? Bool) == true
    }

    var body: some View {
        VStack(spacing: 0) {
            HStack {
                Text("全屏输入")
                    .font(.caption)
                    .foregroundColor(.secondary)
                Spacer()
                Button(action: onClose) {
                    Image(systemName: "chevron.down")
                        .font(.system(size: 15, weight: .medium))
                        .frame(width: 30, height: 30)
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .foregroundColor(.secondary)
                .hoverTooltip("收起（Esc）", direction: .down, align: .trailing, delay: 0.3)
            }
            .padding(.horizontal, 16)
            .padding(.top, 12)

            TextEditor(text: $text)
                .focused($isFocused)
                .font(.system(size: fontSizeManager.scaledSize))
                .scrollContentBackground(.hidden)
                .padding(.horizontal, 12)
                .padding(.top, 4)
                // Return：单独按发送；Shift / Option+Return 换行（放行给 TextEditor）。
                .onKeyPress(.return, phases: .down) { keyPress in
                    let modifiers = keyPress.modifiers
                    guard !modifiers.contains(.shift), !modifiers.contains(.option) else { return .ignored }
                    // IME 组合态下交给输入法确认。
                    if isIMEComposing {
                        return .ignored
                    }
                    guard canSend else { return .handled }
                    DispatchQueue.main.async {
                        onSend()
                    }
                    return .handled
                }
                .onKeyPress(.escape, phases: .down) { _ in
                    // 组词中 Esc 只取消拼音（放行给输入法），不收起面板。
                    if isIMEComposing { return .ignored }
                    onClose()
                    return .handled
                }
                // Tab：循环计划模式（与普通输入栏一致；流式中不响应）。
                .onKeyPress(.tab, phases: .down) { _ in
                    guard !isStreaming else { return .handled }
                    onToggleMode()
                    return .handled
                }
                // ⌃C：清空草稿（文字 + 待发图片），与普通输入栏一致。
                .onKeyPress(KeyEquivalent("c"), phases: .down) { keyPress in
                    guard keyPress.modifiers.contains(.control) else { return .ignored }
                    onClearAll()
                    return .handled
                }
                .onChange(of: isFocused) { _, focused in
                    onFocusChange(focused)
                }

            Rectangle()
                .fill(Color.primary.opacity(0.10))
                .frame(height: 0.5)
                .padding(.horizontal, 16)

            HStack {
                Spacer(minLength: 0)
                SendStopButton(
                    context: InputBarContext(isStreaming: isStreaming, canSend: canSend),
                    isStreaming: isStreaming,
                    canSend: canSend,
                    sendTooltip: "发送（Enter）",
                    onSend: onSend,
                    onStop: onStop
                )
            }
            .padding(.horizontal, 16)
            .padding(.vertical, 10)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(
            RoundedRectangle(cornerRadius: 20, style: .continuous)
                .fill(.regularMaterial)
        )
        .overlay(
            RoundedRectangle(cornerRadius: 20, style: .continuous)
                .strokeBorder(Color.primary.opacity(0.08), lineWidth: 0.5)
        )
        .shadow(color: .black.opacity(0.10), radius: 16, y: 4)
        .padding(12)
        .task {
            // 等第一次布局再抢焦点；onAppear 里直接设会被覆盖层的重建吃掉。
            try? await Task.sleep(for: .milliseconds(60))
            guard !Task.isCancelled else { return }
            isFocused = true
        }
        .onAppear {
            // 全屏输入期间 App 级 Esc/Tab（L3 拒绝/允许）要让路给面板。
            ChatInputFocus.shared.expandedComposerActive = true
        }
        .onDisappear {
            ChatInputFocus.shared.expandedComposerActive = false
            // 视图消失时焦点不会自己回报，留着会让 ⌘V 误判收件人。
            onFocusChange(false)
        }
    }
}
#endif
