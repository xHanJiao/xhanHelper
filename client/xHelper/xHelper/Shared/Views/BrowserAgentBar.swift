import SwiftUI

/// 浏览器内确认条：仅浏览器内部确认（有下载进行中时关标签、用户点链接触发下载）——
/// L3 工具确认已回归聊天栏，不再同步到此。观察 BrowserActivityCenter.pendingConfirmation。
/// 呼吸动画吸引注意（需要用户同意时）。
struct BrowserAgentConfirmBar: View {
    @ObservedObject private var center = BrowserActivityCenter.shared
    @State private var pulse = false

    var body: some View {
        Group {
            if let conf = center.pendingConfirmation {
                HStack(spacing: 8) {
                    Image(systemName: "exclamationmark.shield.fill")
                        .font(.system(size: 12))
                        .foregroundColor(.orange)
                    VStack(alignment: .leading, spacing: 1) {
                        Text("AI 请求执行：\(conf.displayName)")
                            .font(.system(size: 11, weight: .semibold))
                            .foregroundColor(.primary)
                        Text(conf.argsSummary)
                            .font(.system(size: 10))
                            .foregroundColor(.secondary)
                            .lineLimit(3)
                    }
                    Spacer(minLength: 8)
                    Button("拒绝") { conf.onResolve(false) }
                        .controlSize(.small)
                    Button("允许") { conf.onResolve(true) }
                        .controlSize(.small)
                        .tint(.orange)
                }
                .padding(.horizontal, 10)
                .padding(.vertical, 6)
                .background(
                    Color.orange.opacity(pulse ? 0.20 : 0.12)
                )
                .overlay(alignment: .leading) {
                    Rectangle()
                        .fill(Color.orange)
                        .frame(width: 2)
                }
                // 呼吸高亮（只动背景不透明度，不移动视图/按钮，不影响点击）
                .onAppear {
                    withAnimation(.easeInOut(duration: 0.8).repeatForever(autoreverses: true)) {
                        pulse = true
                    }
                }
            }
        }
        .animation(.easeOut(duration: 0.2), value: center.pendingConfirmation != nil)
    }
}
