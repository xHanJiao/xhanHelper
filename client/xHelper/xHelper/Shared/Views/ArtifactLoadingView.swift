import SwiftUI

/// 产物页的骨架态：拉正文期间先占住右栏 tab，别让用户以为"点了没反应"。
///
/// 为什么需要它：内联过大图的页面接近 8MB，拉 JSON → 解析 → 注入基线 → `loadHTMLString`
/// 全在主线程，「拉完再开 tab」的写法在慢链路上就是几秒钟的静默（docs/design/html-artifact §9.8）。
struct ArtifactLoadingView: View {
    let title: String

    var body: some View {
        ZStack {
            Color.white
            VStack(spacing: 12) {
                ProgressView()
                Text(title.isEmpty ? "正在打开…" : title)
                    .font(.system(size: 12))
                    .foregroundColor(Color("assistantText").opacity(0.6))
                    .lineLimit(1)
            }
            .padding(24)
        }
        .ignoresSafeArea()
    }
}
