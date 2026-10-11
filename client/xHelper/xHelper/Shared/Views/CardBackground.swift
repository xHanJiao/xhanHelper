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

/// 卡片投影：给**半透明**卡片投射「只在卡片外」的软阴影，让它比周围"高一层"（Xcode 侧栏那种观感）。
/// 直接对半透明卡片用 `.shadow` 会把投影渗进卡片内部（等于把里面压暗），故先画不透明阴影形、
/// 再用**卡片同形**挖掉内部（`destinationOut` + `compositingGroup` 隔离，别把洞打到窗口底图上）。
/// 浅色黑阴影 / 深色白阴影 —— 与 `CardBackgroundModifier` 的卡片语言一致。
struct CardElevationShadow: ViewModifier {
    /// 卡片相对自身 frame 的**上溢量**（与描边/填充同形；见 `Layout.cardTopRise`）。
    var topExtension: CGFloat
    var cornerRadius: CGFloat = 12

    @Environment(\.colorScheme) private var colorScheme

    func body(content: Content) -> some View {
        content.background {
            let shape = RoundedRectangle(cornerRadius: cornerRadius)
            ZStack {
                shape
                    .fill((colorScheme == .dark ? Color.white : .black).opacity(0.10))
                    .blur(radius: 8)
                    .offset(y: 3)
                shape.fill(.black).blendMode(.destinationOut)   // 挖掉卡片内部
            }
            .compositingGroup()
            .padding(.top, -topExtension)
            .allowsHitTesting(false)
        }
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
