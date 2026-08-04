import SwiftUI

/// 输入栏按钮共享的修饰逻辑：可见性过滤 + 禁用置灰 + 悬停反馈 + 自定义 hover 标签。
/// IconButton 等所有输入栏按钮都复用它，避免重复这套逻辑。
private struct InputBarControlModifier: ViewModifier {
    let visibility: ButtonVisibility
    let context: InputBarContext
    let isEnabled: Bool
    let tooltip: String

    @State private var hovering = false

    func body(content: Content) -> some View {
        let visible = visibility.isVisible(in: context)
        return Group {
            if visible {
                content
                    .opacity(isEnabled ? 1 : 0.35)
                    .scaleEffect(hovering && isEnabled ? 1.08 : 1)
                    .animation(.easeOut(duration: 0.12), value: hovering)
                    .disabled(!isEnabled)
                    .onHover { isHovering in
                        hovering = isHovering
                    }
                    .hoverTooltip(tooltip, direction: .up, delay: 0.45)
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

/// 圆形图标骨架：动作/状态类按钮（模式、信任、发送、停止、未来上传）。
/// symbol 与 tint 可由调用方随状态传入（发送↔停止 morph、模式图标切换即依赖此）。
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
