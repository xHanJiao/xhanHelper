import SwiftUI
#if os(macOS)

struct GlassWindowModifier: ViewModifier {
    func body(content: Content) -> some View {
        content
            .ignoresSafeArea(edges: .top)
            .background {
                // 整窗唯一一组底：照片重模糊成色块（AppBackdrop 自身不透明，内部已 ignoresSafeArea）。
                // 不压整窗 NSVisualEffectView：非不透明窗 + 右侧 WKWebView 共存时，
                // 逐键重合成会让 material 偶发画成灰色兜底，在中栏透明区显形为灰闪（bug 030）。
                AppBackdrop()
            }
    }
}

extension View {
    func glassWindow() -> some View {
        modifier(GlassWindowModifier())
    }
}
#endif
