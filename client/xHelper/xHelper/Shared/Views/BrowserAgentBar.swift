import SwiftUI

#if os(macOS)
/// G2：浏览器地址栏下方的细条指示器——agent 操作浏览器的可见性。
/// 状态 A（运行中）：转圈；状态 B（完成）：✓，2.5s 后由 BrowserActivityCenter 自动消退。
/// 方案 B：实色 accent 条 + 对比自适应文字（浅色=白字加粗、深色=近黑字）。
struct BrowserAgentActivityBar: View {
    @ObservedObject private var center = BrowserActivityCenter.shared
    @Environment(\.colorScheme) private var colorScheme

    /// 实色 accent 条上的对比文字色：浅色 accent=蓝 → 白字；深色 accent=浅青绿 → 近黑字（白字会不可见）。
    private var statusForeground: Color {
        colorScheme == .dark ? Color(white: 0.12) : .white
    }

    var body: some View {
        Group {
            if let act = center.activity {
                ZStack {
                    HStack(spacing: 6) {
                        Text("AI 正在操作浏览器 ·")
                            .foregroundColor(statusForeground.opacity(0.75))
                        Text(act.verb)
                            .fontWeight(.bold)
                            .foregroundColor(statusForeground)
                        if !act.target.isEmpty {
                            Text(act.target)
                                .fontWeight(.semibold)
                                .foregroundColor(statusForeground)
                        }
                    }
                    .font(.system(size: 12, weight: .medium))
                    .multilineTextAlignment(.center)
                    .fixedSize(horizontal: false, vertical: true)
                    .frame(maxWidth: .infinity)
                    .padding(.vertical, 5)
                    .padding(.horizontal, 28)
                    .overlay(alignment: .leading) {
                        Image(systemName: "sparkles")
                            .font(.system(size: 12, weight: .semibold))
                            .foregroundColor(statusForeground)
                            .padding(.leading, 10)
                    }
                    .overlay(alignment: .trailing) {
                        if act.state == .running {
                            ProgressView()
                                .controlSize(.mini)
                                .tint(statusForeground)
                                .padding(.trailing, 12)
                        } else {
                            Image(systemName: "checkmark")
                                .font(.system(size: 11, weight: .bold))
                                .foregroundColor(statusForeground)
                                .padding(.trailing, 13)
                        }
                    }
                }
                .background(
                    RoundedRectangle(cornerRadius: 8, style: .continuous)
                        .fill(Color("appAccent"))
                )
                .overlay(
                    RoundedRectangle(cornerRadius: 8, style: .continuous)
                        .stroke(statusForeground.opacity(0.28), lineWidth: 1)
                )
                .padding(.horizontal, 10)
                .padding(.vertical, 4)
                .transition(.move(edge: .top).combined(with: .opacity))
            }
        }
        .animation(.easeOut(duration: 0.2), value: center.activity != nil)
    }
}

/// G2 状态 C：L3 高危工具请求，浏览器内同步确认（与聊天区确认条二选一即可）。
/// 观察 BrowserActivityCenter.pendingConfirmation（awaitToolConfirmation 桥接发布，与聊天条同步出现）。
/// 震荡动画吸引注意（需要用户同意时）。
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
#endif
