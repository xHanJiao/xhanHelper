import SwiftUI

/// 水平间隙：无色无味，仅在 HStack 中占据固定宽度。
struct HSpace: View {
    let w: CGFloat
    init(_ w: CGFloat) { self.w = w }
    var body: some View { Color.clear.frame(width: w) }
}

/// 纵向间隙：无色无味，仅在 VStack 中占据固定高度。
struct VSpace: View {
    let h: CGFloat
    init(_ h: CGFloat) { self.h = h }
    var body: some View { Color.clear.frame(height: h) }
}
