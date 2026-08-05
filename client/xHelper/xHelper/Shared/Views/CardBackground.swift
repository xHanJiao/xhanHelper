import SwiftUI

struct CardBackgroundModifier: ViewModifier {
    let isLifted: Bool
    @Environment(\.colorScheme) private var colorScheme

    func body(content: Content) -> some View {
        if colorScheme == .dark {
            content.modifier(DarkCardBackgroundModifier(isLifted: isLifted))
        } else {
            content.modifier(LightCardBackgroundModifier(isLifted: isLifted))
        }
    }
}

private struct LightCardBackgroundModifier: ViewModifier {
    let isLifted: Bool

    func body(content: Content) -> some View {
        content
            .clipShape(RoundedRectangle(cornerRadius: 12))
            .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 12))
            .modifier(CardShadowRenderer(
                isLifted: isLifted,
                shadowColor: .black,
                shadowOpacity: isLifted ? 0.2 : 0.08,
                borderOpacity: 0))
    }
}

private struct DarkCardBackgroundModifier: ViewModifier {
    let isLifted: Bool

    func body(content: Content) -> some View {
        content
            .clipShape(RoundedRectangle(cornerRadius: 12))
            .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 12))
            .modifier(CardShadowRenderer(
                isLifted: isLifted,
                shadowColor: .white,
                shadowOpacity: isLifted ? 0.25 : 0.08,
                borderOpacity: isLifted ? 0.2 : 0))
    }
}

private struct CardShadowRenderer: ViewModifier {
    let isLifted: Bool
    let shadowColor: Color
    let shadowOpacity: CGFloat
    let borderOpacity: CGFloat

    func body(content: Content) -> some View {
        content
            .shadow(
                color: shadowColor.opacity(shadowOpacity),
                radius: isLifted ? 12 : 6,
                y: isLifted ? 6 : 2)
            .overlay(
                RoundedRectangle(cornerRadius: 12)
                    .stroke(.white.opacity(borderOpacity), lineWidth: 0.5))
            .compositingGroup()
            .animation(.easeInOut(duration: 0.25), value: isLifted)
    }
}

/// 带 hover 浮动提升的玻璃卡片行容器（下载/收藏夹/配置页共用）。
struct HoverCardRow<Content: View>: View {
    @State private var isHovered = false
    @ViewBuilder let content: () -> Content

    var body: some View {
        content()
            .padding(10)
            .modifier(CardBackgroundModifier(isLifted: isHovered))
            .onHover { isHovered = $0 }
    }
}
