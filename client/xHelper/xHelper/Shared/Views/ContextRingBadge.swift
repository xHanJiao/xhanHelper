import Foundation
import SwiftUI

#if os(macOS)
/// 输入栏底部左侧的上下文占用圆环（Codex 风格）。
/// 仅用圆环表示 contextTokens / contextWindow（阈值变色，无文字）；
/// hover 出自定义浮层展示上下文占用与消耗金额。
struct ContextRingBadge: View {
    /// 当前上下文 token 数（即时）
    let contextTokens: Int
    /// 上下文窗口上限（按模型档位，0=未配置）
    let contextWindow: Int
    /// 会话消耗金额（慢更新）
    let usage: ConversationUsage?

    @State private var hovering = false

    private var ratio: Double {
        guard contextWindow > 0 else { return 0 }
        return min(max(Double(contextTokens) / Double(contextWindow), 0), 1)
    }

    /// 阈值变色（激进档）：<15% 次要色、15~35% 橙、>35% 红。
    private var ringColor: Color {
        switch ratio {
        case ..<0.15: return .secondary
        case ..<0.35: return .orange
        default: return .red
        }
    }

    var body: some View {
        if contextTokens > 0 || contextWindow > 0 {
            ZStack {
                Circle()
                    .stroke(Color.secondary.opacity(0.25), lineWidth: 2)
                Circle()
                    .trim(from: 0, to: ratio)
                    .stroke(ringColor, style: StrokeStyle(lineWidth: 2, lineCap: .round))
                    .rotationEffect(.degrees(-90))
            }
            .frame(width: 16, height: 16)
            .contentShape(Rectangle())
            .onHover { hovering = $0 }
            .popover(isPresented: $hovering, arrowEdge: .top) {
                popoverContent
            }
        }
    }

    private var popoverContent: some View {
        VStack(alignment: .leading, spacing: 6) {
            Text("上下文   \(fmtTokens(contextTokens)) / \(contextWindow > 0 ? fmtTokens(contextWindow) : "—")\(ratioText)")
            Divider()
            Text("会话合计   \(fmtCost(usage?.totalCost))")
            Text("窗口消耗   \(fmtCost(usage?.windowCost))")
            if let usage = usage, usage.subagentCount > 0 {
                Text("子智能体   \(usage.subagentCount) 个 · \(fmtCost(usage.subagentCost))")
            }
        }
        .font(.caption)
        .padding(12)
        .frame(minWidth: 190, alignment: .leading)
    }

    private var ratioText: String {
        guard contextWindow > 0 else { return "" }
        return String(format: " · %.1f%%", ratio * 100)
    }

    private func fmtTokens(_ n: Int) -> String {
        guard n > 0 else { return "0" }
        if n >= 1_000_000 { return String(format: "%.1fM", Double(n) / 1_000_000) }
        if n >= 1_000 { return String(format: "%.1fk", Double(n) / 1_000) }
        return "\(n)"
    }

    private func fmtCost(_ c: Double?) -> String {
        guard let c else { return "—" }
        if c >= 1 { return String(format: "¥%.2f", c) }
        return String(format: "¥%.4f", c)
    }
}
#endif
