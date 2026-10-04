import SwiftUI
#if os(macOS)

struct GlassWindowModifier: ViewModifier {
    func body(content: Content) -> some View {
        content
            .ignoresSafeArea(edges: .top)
            .background {
                // 整窗唯一一组底：照片在最底、玻璃压上（两者都满铺，含 32pt 系统标题栏）。
                ZStack {
                    AppBackdrop()
                    Rectangle().fill(.regularMaterial)
                }
                .ignoresSafeArea()
            }
    }
}

extension View {
    func glassWindow() -> some View {
        modifier(GlassWindowModifier())
    }
}
#endif
