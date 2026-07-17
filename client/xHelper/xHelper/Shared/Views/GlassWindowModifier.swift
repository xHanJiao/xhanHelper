import SwiftUI
#if os(macOS)

struct GlassWindowModifier: ViewModifier {
    @State private var hideFallback = false

    func body(content: Content) -> some View {
        ZStack {
            if !hideFallback {
                Color(nsColor: .windowBackgroundColor)
                    .ignoresSafeArea()
            }
            content
        }
        .background(.ultraThinMaterial)
        .onAppear {
            DispatchQueue.main.async {
                hideFallback = true
            }
        }
    }
}

extension View {
    func glassWindow() -> some View {
        modifier(GlassWindowModifier())
    }
}
#endif
