import SwiftUI

/// 统一标签胶囊：强制单行、不压缩，长文本完整显示（横向溢出交给外层容器滚动）。
struct TagChip: View {
    let text: String
    let color: Color
    var font: Font = .system(size: 11)
    var hPad: CGFloat = 7
    var vPad: CGFloat = 2
    var bgOpacity: Double = 0.12

    var body: some View {
        Text(text)
            .font(font)
            .foregroundColor(color)
            .lineLimit(1)
            .fixedSize(horizontal: true, vertical: false)
            .padding(.horizontal, hPad)
            .padding(.vertical, vPad)
            .background(color.opacity(bgOpacity), in: Capsule())
    }
}

#if os(macOS)
/// macOS：标签行横向滚动，行高恒定单行；放不下时滑动查看而不是折行。
struct TagScrollRow<Content: View>: View {
    var spacing: CGFloat = 6
    let content: Content

    init(spacing: CGFloat = 6, @ViewBuilder content: () -> Content) {
        self.spacing = spacing
        self.content = content()
    }

    var body: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: spacing) { content }
        }
    }
}
#endif
