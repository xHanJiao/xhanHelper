import SwiftUI

// MARK: - ToolActivityToast — 工具调用 & 响应检验气泡
//
// 仿前端 ToolCallToast：挂在最新助手气泡下方。
// - 工具 pill：显示队列第一个（running 脉冲 → done ✅ → failed ❌），
//   后端 tool_result 更新后自然切换，无需定时器。
// - verify pill：工具队列清空后展示，checking 常驻，done/retry 更新文字。
// - 文本流开始（message delta）→ VM 清 toolQueue/verifyStatus → 自动退场。

struct ToolActivityToast: View {
    let toolQueue: [ToolCallDisplay]
    let verifyStatus: VerifyStatus?

    var body: some View {
        HStack(spacing: 8) {
            if let current = toolQueue.first {
                toolPill(current)
            } else if let vs = verifyStatus {
                verifyPill(vs)
            }
        }
        .animation(.easeInOut(duration: 0.25), value: toolQueue.first?.status)
    }

    // MARK: Tool pill

    @ViewBuilder
    private func toolPill(_ tc: ToolCallDisplay) -> some View {
        HStack(spacing: 6) {
            Image(systemName: iconFor(tc.status))
                .font(.caption)
                .foregroundColor(iconColorFor(tc.status))
            Text(pillText(for: tc))
                .font(.caption)
                .foregroundColor(.secondary)
                .lineLimit(1)
        }
        .padding(.horizontal, 12).padding(.vertical, 6)
        .background(.ultraThinMaterial, in: RoundedRectangle(cornerRadius: 12))
        .overlay(RoundedRectangle(cornerRadius: 12).stroke(.white.opacity(0.35), lineWidth: 0.5))
    }

    private func pillText(for tc: ToolCallDisplay) -> String {
        switch tc.status {
        case .running: return tc.display
        case .done, .failed: return tc.summary ?? tc.display
        }
    }

    private func iconFor(_ s: ToolCallDisplay.Status) -> String {
        switch s {
        case .running: return "gearshape"
        case .done: return "checkmark.circle.fill"
        case .failed: return "xmark.circle.fill"
        }
    }

    private func iconColorFor(_ s: ToolCallDisplay.Status) -> Color {
        switch s {
        case .running: return .accentColor
        case .done: return .green
        case .failed: return .red
        }
    }

    // MARK: Verify pill

    @ViewBuilder
    private func verifyPill(_ vs: VerifyStatus) -> some View {
        HStack(spacing: 6) {
            Image(systemName: "text.magnifyingglass")
                .font(.caption)
                .foregroundColor(.accentColor)
            Text(verifyText(for: vs))
                .font(.caption)
                .foregroundColor(.secondary)
                .lineLimit(1)
        }
        .padding(.horizontal, 12).padding(.vertical, 6)
        .background(.ultraThinMaterial, in: RoundedRectangle(cornerRadius: 12))
        .overlay(RoundedRectangle(cornerRadius: 12).stroke(.white.opacity(0.35), lineWidth: 0.5))
    }

    private func verifyText(for vs: VerifyStatus) -> String {
        switch vs.status {
        case "checking": return "结果检查中...（第\(vs.retry)次）"
        case "done": return "✅ 结果检查通过"
        case "retry": return "检查未通过，正在完善...（第\(vs.retry)次）"
        default: return "检验中..."
        }
    }
}
