import SwiftUI

/// 悬浮提示方向：up 向上浮（按钮上方），down 向下浮（按钮下方）。
enum TooltipDirection {
    case up
    case down
}

/// 通用 hover tooltip：hover 延迟 delay 秒后在按钮外侧浮出玻璃圆角小条，
/// 移开立即消失。输入框与顶栏胶囊按钮共用，保证全 app 提示风格统一。
struct HoverTooltipModifier: ViewModifier {
    let text: String
    var direction: TooltipDirection = .up
    var delay: TimeInterval = 0.45

    @State private var tooltipVisible = false
    @State private var tooltipTask: Task<Void, Never>?

    func body(content: Content) -> some View {
        content
            .onHover { isHovering in
                schedule(isHovering)
            }
            .overlay(alignment: direction == .up ? .top : .bottom) {
                if tooltipVisible && !text.isEmpty {
                    tooltipLabel
                        .transition(.opacity.combined(with: .move(edge: direction == .up ? .bottom : .top)))
                }
            }
            .animation(.easeOut(duration: 0.15), value: tooltipVisible)
    }

    private var tooltipLabel: some View {
        Text(text)
            .font(.caption)
            .lineLimit(1)
            .fixedSize()
            .padding(.horizontal, 8)
            .padding(.vertical, 4)
            .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 6, style: .continuous))
            .overlay(
                RoundedRectangle(cornerRadius: 6, style: .continuous)
                    .strokeBorder(Color.primary.opacity(0.08), lineWidth: 1)
            )
            .shadow(color: .black.opacity(0.15), radius: 6, y: 2)
            .offset(y: direction == .up ? -26 : 26)
            .allowsHitTesting(false) // 纯视觉提示，不拦截其覆盖区域的点击
    }

    private func schedule(_ show: Bool) {
        tooltipTask?.cancel()
        if show {
            tooltipTask = Task {
                try? await Task.sleep(for: .milliseconds(Int(delay * 1000)))
                guard !Task.isCancelled else { return }
                tooltipVisible = true
            }
        } else {
            tooltipVisible = false
        }
    }
}

extension View {
    /// 添加通用 hover tooltip。
    func hoverTooltip(_ text: String, direction: TooltipDirection = .up, delay: TimeInterval = 0.45) -> some View {
        modifier(HoverTooltipModifier(text: text, direction: direction, delay: delay))
    }
}
