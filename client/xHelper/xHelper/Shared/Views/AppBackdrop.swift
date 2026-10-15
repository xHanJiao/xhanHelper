#if os(macOS)
import AppKit
import SwiftUI

/// 整窗底图：当前照片（内置默认图或用户池）重度模糊成"色块"，再压一层明暗遮罩稳住正文对比度。
///
/// **永远不返回空**——没有用户照片时用内置默认图 `bgDefault`；连默认图都取不到（资源缺失等）
/// 则回落到不透明纯色。故底层始终不透明，玻璃模糊的是我们自己的照片，与桌面/其他 app 无关。
///
/// 只应出现在窗口根（`GlassWindowModifier`）——**任何列/列表都不许再自铺底层色**
/// （曾因把色块挂在中间列导致"标题栏只盖一半"）。
struct AppBackdrop: View {
    @ObservedObject private var store = BackgroundPhotoStore.shared
    @Environment(\.colorScheme) private var colorScheme
    @Environment(\.accessibilityReduceTransparency) private var reduceTransparency

    /// 重度模糊半径（只留色块；`opaque: true` 防四边渐隐透出底层）。预览靶调出的值。
    private let blurRadius: CGFloat = 60
    /// 明暗遮罩不透明度。真机调出的值。
    private let veil: Double = 0.40
    /// 中性白霜不透明度：补偿移除整窗 `.regularMaterial` 后丢失的提亮（bug 030）。
    /// 浅色偏白霜、深色偏浅灰（深色材质只提到浅灰），故深色取值更小。真机调出的值。
    private let frostLight: Double = 0.40
    private let frostDark: Double = 0.08
    /// 当前色系的白霜不透明度。
    private var frost: Double {
        colorScheme == .dark ? frostDark : frostLight
    }

    /// 遮罩色：浅色用暖米压白，深色用近黑压暗（与 WindowsBasePreview 的 tint 一致）。
    private var veilColor: Color {
        colorScheme == .dark ? Color("chatBackgroundDeep") : Color("chatBackground")
    }

    var body: some View {
        if reduceTransparency {
            // 降透明度模式：交给不透明窗底，不画照片（也不透明，避免 sheet 里透出下层）。
            Color(nsColor: .windowBackgroundColor).ignoresSafeArea()
        } else {
            ZStack {
                // 最底不透明纯色兜底：照片层若被裁/掉帧也只露这个色，绝不"透桌面"。
                Color("chatBackground")
                photoLayer
                veilColor.opacity(veil)
                Color.white.opacity(frost)
            }
            // `.ignoresSafeArea()` 必须是最外层修饰符——后面再挂 `.clipped()` 会把扩到
            // 安全区（32pt 标题栏）的部分裁回去，顶上一条就露桌面。裁剪收到 photoLayer 内部。
            .ignoresSafeArea()
        }
    }

    @ViewBuilder
    private var photoLayer: some View {
        if store.currentImage.size.width > 0 {
            Image(nsImage: store.currentImage)
                .resizable()
                .interpolation(.high)
                .scaledToFill()
                .frame(maxWidth: .infinity, maxHeight: .infinity)
                .blur(radius: blurRadius, opaque: true)
                .clipped()
        } else {
            // 兜底：默认图取不到时用不透明纯色，绝不落到"空图 → 透桌面"。
            Color("chatBackground")
        }
    }
}
#endif
