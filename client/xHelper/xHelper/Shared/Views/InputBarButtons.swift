import SwiftUI

/// 会话模式图标按钮：聊天（青绿）/ 计划（橙），点击切换。仅图标，hover 看 tooltip。
struct ModePill: View {
    let context: InputBarContext
    let isPlanMode: Bool
    let action: () -> Void

    var body: some View {
        IconButton(
            visibility: .always,
            context: context,
            symbol: isPlanMode ? "eye.slash" : "bubble.left",
            tint: isPlanMode ? .orange : Color("appAccent"),
            tooltip: isPlanMode
                ? "计划模式 · 只读，点击切换聊天"
                : "聊天模式，点击切换计划",
            action: action
        )
    }
}

/// 信任模式图标按钮：安全（青绿）/ 信任（橙），点击切换。仅图标，hover 看 tooltip。
struct TrustedPill: View {
    let context: InputBarContext
    let trusted: Bool
    let action: () -> Void

    var body: some View {
        IconButton(
            visibility: .always,
            context: context,
            symbol: trusted ? "shield.checkered" : "shield",
            tint: trusted ? .orange : Color("appAccent"),
            tooltip: trusted
                ? "信任模式 · 免确认，点击切换安全"
                : "安全模式，点击切换信任",
            action: action
        )
    }
}

/// 模型切换图标按钮：快速（bolt 绿）/ 深度（pro+thinking，蓝紫），点击切换。仅图标，hover 看 tooltip。
struct ModelPill: View {
    let context: InputBarContext
    let isDeep: Bool
    let action: () -> Void

    @Environment(\.colorScheme) private var colorScheme

    /// 深度模式蓝紫：明暗自适应（不用 asset 命名色，避免新增 colorset 在增量构建时
    /// 未被 actool 编译导致 Color("name") 解析失败变透明）。
    private var deepColor: Color {
        colorScheme == .dark
            ? Color(red: 0.63, green: 0.58, blue: 1.0)
            : Color(red: 0.42, green: 0.36, blue: 0.95)
    }

    var body: some View {
        IconButton(
            visibility: .always,
            context: context,
            symbol: isDeep ? "brain.head.profile.fill" : "bolt",
            tint: isDeep ? deepColor : Color("appAccent"),
            tooltip: isDeep
                ? "深度模型 · pro+思考，点击切换快速"
                : "快速模型，点击切换深度",
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
            visibility: .always,
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
