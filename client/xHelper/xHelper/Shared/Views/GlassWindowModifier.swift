import SwiftUI
#if os(macOS)

struct GlassWindowModifier: ViewModifier {
    func body(content: Content) -> some View {
        content
            .ignoresSafeArea(edges: .top)
            .background(.ultraThinMaterial)
    }
}

extension View {
    func glassWindow() -> some View {
        modifier(GlassWindowModifier())
    }
}
#endif
