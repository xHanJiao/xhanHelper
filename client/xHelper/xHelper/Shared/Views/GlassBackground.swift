import SwiftUI

/// 共享的玻璃拟态背景。macOS 用固定照片底图（`AppBackdrop`，与桌面无关）；
/// iOS 仍用暖调渐变 + 柔和蓝青光晕。
struct GlassBackground: View {
    var body: some View {
        #if os(macOS)
        AppBackdrop()
        #else
        warmGradient
        #endif
    }

    private var warmGradient: some View {
        ZStack {
            LinearGradient(
                colors: [Color("chatBackground"), Color("chatBackgroundDeep")],
                startPoint: .top,
                endPoint: .bottom
            )

            Circle()
                .fill(Color("appAccent").opacity(0.28))
                .frame(width: 320, height: 320)
                .blur(radius: 70)
                .offset(x: -110, y: -220)
            Circle()
                .fill(Color("accentSecondary").opacity(0.30))
                .frame(width: 300, height: 300)
                .blur(radius: 70)
                .offset(x: 130, y: 40)
            Circle()
                .fill(Color("appAccent").opacity(0.18))
                .frame(width: 260, height: 260)
                .blur(radius: 70)
                .offset(x: 40, y: 360)
        }
        .ignoresSafeArea()
    }
}
