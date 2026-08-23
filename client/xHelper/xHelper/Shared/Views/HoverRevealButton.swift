import SwiftUI

/// 边缘悬停热区 + 浮现按钮（分离命中层与视觉层）。
///
/// - 命中层：底部透明热区条（宽 `zoneWidth`，整条可命中），负责 hover 检测与点击。
/// - 视觉层：圆形按钮 `.allowsHitTesting(false)`，只镜像 hover 状态，不参与命中。
///   因此悬停热区 → 按钮浮现；指针移向按钮不会让按钮消失（按钮不拦截命中）。
///
/// 思路沿用早期左侧栏收起手柄（split hit-test/layer，见 82b9d30）。
struct HoverRevealButton: View {
    let systemName: String
    let tooltip: String
    let action: () -> Void
    var zoneWidth: CGFloat = 48
    var buttonSize: CGFloat = 32

    @State private var isHovered = false

    var body: some View {
        ZStack(alignment: .center) {
            // 命中层：透明热区条，处理 hover + 点击
            Color.clear
                .frame(width: zoneWidth)
                .contentShape(Rectangle())
                .onHover { isHovered = $0 }
                .onTapGesture { action() }
                .help(tooltip)

            // 视觉层：圆形按钮，不参与命中
            Image(systemName: systemName)
                .font(.system(size: 13, weight: .semibold))
                .foregroundStyle(.primary)
                .frame(width: buttonSize, height: buttonSize)
                .background(.regularMaterial, in: Circle())
                .overlay(Circle().strokeBorder(.quaternary, lineWidth: 0.5))
                .shadow(color: .black.opacity(0.12), radius: 4, y: 1)
                .opacity(isHovered ? 1 : 0)
                .scaleEffect(isHovered ? 1 : 0.8)
                .allowsHitTesting(false)
        }
        .frame(maxHeight: .infinity)
        .animation(.easeOut(duration: 0.15), value: isHovered)
    }
}
