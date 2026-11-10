import SwiftUI

/// 会话模式图标按钮：关闭（青绿·工具全开）/ 计划（橙·只读），点击循环切换。
/// 仅图标，hover 看 tooltip。
struct ModePill: View {
    let context: InputBarContext
    let mode: ChatMode
    let action: () -> Void

    private var tint: Color {
        mode == .normal ? .orange : Color("appAccent")
    }

    private var tooltip: String {
        mode == .normal
            ? "计划模式（仅只读工具），点击退出"
            : "聊天模式（工具全开），点击进入计划模式"
    }

    var body: some View {
        IconButton(
            visibility: .always,
            context: context,
            symbol: "checklist",
            tint: tint,
            tooltip: tooltip,
            action: action
        )
    }
}

/// 完全信任图标按钮（全局持久化开关）：关=灰盾，开=红盾斜杠。
/// 开启属安全降级，二次确认由 ChatView 负责；本视图只负责外观与点击。
struct TrustPill: View {
    let context: InputBarContext
    let isOn: Bool
    let action: () -> Void

    var body: some View {
        IconButton(
            visibility: .always,
            context: context,
            symbol: isOn ? "shield.slash.fill" : "shield.lefthalf.filled",
            tint: isOn ? .red : .secondary,
            tooltip: isOn
                ? "完全信任：本地工具免确认（含 fs 写、shell 写/执行，仅限已授权目录），点击关闭"
                : "完全信任已关闭（fs 写 / shell 写仍会确认），点击开启",
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
            visibility: .hidden, // 2026-10：已从输入栏下架（入口隐藏，功能未删）
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

/// 精炼回答图标按钮：普通（appAccent）/ 精炼（橙），点击切换。仅图标，hover 看 tooltip。
/// 开=服务端往 system prompt 注入「≤500 字」输出约束，让回答短到能整段存进文档批注。
/// 走 prompt 约束而不是 max_tokens —— 后者是静默硬截断，且截断后系统无感知。
struct BriefPill: View {
    let context: InputBarContext
    let isBrief: Bool
    let action: () -> Void

    var body: some View {
        IconButton(
            visibility: .hidden, // 2026-10：已从输入栏下架（入口隐藏，功能未删）
            context: context,
            symbol: isBrief ? "text.badge.minus" : "text.alignleft",
            tint: isBrief ? .orange : Color("appAccent"),
            tooltip: isBrief
                ? "精炼回答 · ≤500 字，点击关闭"
                : "常规长度，点击切换精炼回答（≤500 字）",
            action: action
        )
    }
}

/// 无痕（ephemeral）图标按钮：会话结束后自动销毁（不可恢复）。
/// 空白 tab 开启＝「待生效」（只改本地意图，不建会话）；已有会话开启＝PATCH 落库。
/// 状态由 ChatViewModel 持有（`isEphemeral`），本视图只负责外观与 tooltip。
struct EphemeralPill: View {
    let context: InputBarContext
    let isOn: Bool
    let isBlank: Bool
    var isEnabled: Bool = true
    let action: () -> Void

    var body: some View {
        IconButton(
            visibility: .always,
            context: context,
            symbol: isOn ? "theatermasks.fill" : "theatermasks",
            tint: isOn ? Color("appAccent") : .secondary,
            isEnabled: isEnabled,
            tooltip: tooltip,
            action: action
        )
    }

    private var tooltip: String {
        guard isOn else { return "开启无痕：会话结束后自动销毁（不可恢复）" }
        if isBlank {
            return "无痕已开（待发送）：首条消息后生效，约 30 分钟后自动销毁，点击取消"
        }
        return "无痕生效中：最后一轮对话约 30 分钟后自动销毁，可撤销（进入销毁后不可逆）"
    }
}

/// 会话接力图标按钮：把当前会话整理成接力文档并新开会话续接。仅图标，hover 看 tooltip。
/// 计划模式不能写文档，故置灰并用 tooltip 说明原因（而非「暂时不可点」）。
struct HandoffPill: View {
    let context: InputBarContext
    let isEnabled: Bool
    let isPlanLike: Bool
    let action: () -> Void

    var body: some View {
        IconButton(
            visibility: .hidden, // 2026-10：已从输入栏下架（入口隐藏，功能未删）
            context: context,
            symbol: "arrow.uturn.right.circle",
            tint: Color("appAccent"),
            isEnabled: isEnabled,
            tooltip: isPlanLike
                ? "计划模式不能写文档，先退出计划模式再接力"
                : "整理结论并开始新会话（接力）",
            action: action
        )
    }
}

/// 更多操作展开/收起按钮：收起（折叠）的图标收在它后面，
/// 展开时在按钮行上方多出一行，始终落在胶囊宽度内（不再 overlay 左偏出框）。
struct ExpandPill: View {
    let context: InputBarContext
    let isExpanded: Bool
    let action: () -> Void

    var body: some View {
        IconButton(
            visibility: .always,
            context: context,
            symbol: isExpanded ? "chevron.down" : "chevron.up",
            tint: .secondary,
            tooltip: isExpanded ? "收起更多操作" : "展开更多操作",
            action: action
        )
    }
}

/// 两段式语音模式胶囊：只切档，不录音。当前档高亮。
/// macOS 只有桌面形态，所以录音档叫「点击说话」（移动端 PWA 那边叫「长按说话」）。
struct VoiceModeCapsule: View {
    let mode: VoiceMode
    /// 图标档：中间栏最窄（380）时内容区只剩 304pt，文本胶囊放不下整行，
    /// 退成图标（160pt → 62pt）保住切档能力。见 docs/design/voice-hold-mode。
    var compact: Bool = false
    let onSelect: (VoiceMode) -> Void

    var body: some View {
        HStack(spacing: 2) {
            segment(.continuous, label: "连续对话", symbol: "waveform")
            segment(.hold, label: "点击说话", symbol: "mic")
        }
        .padding(2)
        .background(Capsule().fill(Color.primary.opacity(0.06)))
        .overlay(Capsule().stroke(Color.primary.opacity(0.12), lineWidth: 0.5))
    }

    private func segment(_ value: VoiceMode, label: String, symbol: String) -> some View {
        Button {
            onSelect(value)
        } label: {
            Group {
                if compact {
                    Image(systemName: symbol).font(.system(size: 12, weight: .semibold))
                } else {
                    Text(label).font(.system(size: 12, weight: .semibold))
                }
            }
            .foregroundColor(mode == value ? Color("appAccent") : .secondary)
            .padding(.horizontal, compact ? 8 : 10)
            .frame(height: 26)
            .background(
                Capsule().fill(mode == value ? Color("appAccent").opacity(0.16) : .clear)
            )
        }
        .buttonStyle(.plain)
        // 图标档看不出是哪个档，tooltip 与无障碍标签补上
        .help(label)
        .accessibilityLabel(label)
        .accessibilityAddTraits(mode == value ? .isSelected : [])
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
