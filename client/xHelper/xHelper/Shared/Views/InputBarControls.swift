import SwiftUI

/// 输入栏按钮共享的修饰逻辑：可见性过滤 + 禁用置灰 + tooltip + 悬停反馈。
/// TogglePill 与 IconButton 都复用它，避免"胶囊/图标按钮"之间重复这套逻辑。
private struct InputBarControlModifier: ViewModifier {
    let visibility: ButtonVisibility
    let context: InputBarContext
    let isEnabled: Bool
    let tooltip: String

    @State private var hovering = false
    @State private var lastVisible: Bool?

    func body(content: Content) -> some View {
        let visible = visibility.isVisible(in: context)
        return Group {
            if visible {
                content
                    .opacity(isEnabled ? 1 : 0.35)
                    .scaleEffect(hovering && isEnabled ? 1.08 : 1)
                    .animation(.easeOut(duration: 0.12), value: hovering)
                    .disabled(!isEnabled)
                    .help(tooltip)
                    .onHover { hovering = $0 }
            }
        }
        .onChange(of: visible) { _, nowVisible in
            if lastVisible != nowVisible {
                let name = tooltip.isEmpty ? "button" : tooltip
                print("[InputBar][\(name)] visible=\(nowVisible) (collapsed=\(context.isCollapsed) streaming=\(context.isStreaming))")
                lastVisible = nowVisible
            }
        }
    }
}

extension View {
    func inputBarControl(
        _ visibility: ButtonVisibility,
        context: InputBarContext,
        isEnabled: Bool = true,
        tooltip: String = ""
    ) -> some View {
        modifier(InputBarControlModifier(
            visibility: visibility,
            context: context,
            isEnabled: isEnabled,
            tooltip: tooltip
        ))
    }
}

/// 胶囊骨架：状态切换类按钮（工作/计划、正常/信任、未来模型切换）。
/// 功能按钮只需提供配置参数，样式/交互全在这里。
struct TogglePill: View {
    var visibility: ButtonVisibility
    var context: InputBarContext
    var isActive: Bool
    var icon: String
    var text: String
    var color: Color
    var isEnabled: Bool = true
    var tooltip: String = ""
    var action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: 5) {
                Image(systemName: icon)
                    .font(.caption2)
                    .contentTransition(.symbolEffect(.replace))
                Text(text)
                    .font(.caption)
                    .lineLimit(1)
                    .truncationMode(.tail)
            }
            .foregroundColor(color)
            .fixedSize(horizontal: true, vertical: false)
            .padding(.horizontal, 10)
            .padding(.vertical, 4)
            .background(
                Capsule().fill(color.opacity(isActive ? 0.18 : 0.08))
            )
            .overlay(
                Capsule().strokeBorder(color.opacity(isActive ? 0.45 : 0.18), lineWidth: 1)
            )
        }
        .buttonStyle(.plain)
        .inputBarControl(visibility, context: context, isEnabled: isEnabled, tooltip: tooltip)
        .animation(.easeInOut(duration: 0.2), value: isActive)
    }
}

/// 圆形图标骨架：动作类按钮（语音/发送/停止/未来上传）。
/// symbol 可由调用方随状态传入（发送↔停止 morph 即依赖此）。
struct IconButton: View {
    var visibility: ButtonVisibility
    var context: InputBarContext
    var symbol: String
    var tint: Color
    var isEnabled: Bool = true
    var tooltip: String = ""
    /// 是否持续脉冲（如流式中的停止按钮）。
    var pulsing: Bool = false
    var action: () -> Void

    var body: some View {
        Button(action: action) {
            Image(systemName: symbol)
                .font(.system(size: 22, weight: .medium))
                .frame(width: 34, height: 34)
                .foregroundColor(tint)
                .contentTransition(.symbolEffect(.replace))
                .symbolEffect(.pulse, options: .repeating, isActive: pulsing)
        }
        .buttonStyle(.plain)
        .inputBarControl(visibility, context: context, isEnabled: isEnabled, tooltip: tooltip)
    }
}
