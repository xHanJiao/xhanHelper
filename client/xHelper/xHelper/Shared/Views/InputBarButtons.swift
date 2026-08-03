import SwiftUI

/// 会话模式胶囊：聊天（青绿）/ 计划（橙）。点击切换。
/// 收起态与展开态均显示（可读的会话状态）。
struct ModePill: View {
    let context: InputBarContext
    let isPlanMode: Bool
    let action: () -> Void

    var body: some View {
        TogglePill(
            visibility: .always,
            context: context,
            isActive: isPlanMode,
            icon: isPlanMode ? "eye.slash" : "square.and.pencil",
            text: isPlanMode ? "计划" : "聊天",
            color: isPlanMode ? .orange : Color("appAccent"),
            tooltip: "点击切换会话模式：聊天（可执行全部工具）/ 计划（只读讨论）",
            action: action
        )
    }
}

/// 信任模式胶囊：安全（青绿）/ 信任（橙）。点击切换。
/// 收起态与展开态均显示（你可以在浏览态直接切换）。
struct TrustedPill: View {
    let context: InputBarContext
    let trusted: Bool
    let action: () -> Void

    var body: some View {
        TogglePill(
            visibility: .always,
            context: context,
            isActive: trusted,
            icon: trusted ? "shield.checkered" : "shield",
            text: trusted ? "信任" : "安全",
            color: trusted ? .orange : Color("appAccent"),
            tooltip: "点击切换信任模式：开启后高风险工具（L3）不再弹确认",
            action: action
        )
    }
}

/// 发送/停止按钮：仅展开态显示。流式时 morph 为红色停止，否则为发送箭头。
struct SendStopButton: View {
    let context: InputBarContext
    let isStreaming: Bool
    let canSend: Bool
    let onSend: () -> Void
    let onStop: () -> Void

    var body: some View {
        IconButton(
            visibility: .whenExpanded,
            context: context,
            symbol: isStreaming ? "stop.fill" : "arrow.up",
            tint: isStreaming ? .red : Color("appAccent"),
            isEnabled: isStreaming || canSend,
            tooltip: isStreaming ? "停止生成" : "发送",
            pulsing: isStreaming,
            action: { isStreaming ? onStop() : onSend() }
        )
    }
}
