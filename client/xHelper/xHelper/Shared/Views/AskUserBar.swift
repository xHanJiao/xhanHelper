import SwiftUI

// MARK: - AskUserBar — 子任务用户介入条（非模态悬浮条）
//
// 挂在输入栏上方（L3 确认条同区）。与 ToolActivityToast 不同：
// - 独立发布状态（ChatViewModel.pendingUserRequests），不被文本流清理
// - 多 task 并发时堆叠 + 折叠：第 1 个完整展示，其余折叠为计数，展开后逐条处理
// 交互：ok（已处理）→ 子循环继续；deny（跳过）/ 超时 → 子循环换方案

struct AskUserBar: View {
    let requests: [UserRequest]
    let onResolve: (String, Bool) -> Void
    @State private var expanded = false

    var body: some View {
        VStack(spacing: 6) {
            if let first = requests.first {
                primaryRow(first)
                if expanded && requests.count > 1 {
                    queuedRows(Array(requests.dropFirst()))
                }
            }
        }
        .transition(.move(edge: .bottom).combined(with: .opacity))
    }

    private func primaryRow(_ req: UserRequest) -> some View {
        HStack(spacing: 8) {
            Image(systemName: "hand.raised.fill")
                .font(.caption)
                .foregroundColor(.orange)
            Text(req.message)
                .font(.caption)
                .foregroundColor(.primary)
                .lineLimit(2)
                .frame(maxWidth: .infinity, alignment: .leading)
            if requests.count > 1 {
                Button(expanded ? "收起" : "+\(requests.count - 1) 待处理") {
                    withAnimation(.easeInOut(duration: 0.2)) { expanded.toggle() }
                }
                .font(.caption)
                .buttonStyle(.plain)
            }
            Button("跳过") { onResolve(req.id, false) }
                .font(.caption)
                .buttonStyle(.bordered)
            Button("已处理") { onResolve(req.id, true) }
                .font(.caption)
                .buttonStyle(.borderedProminent)
        }
        .padding(.horizontal, 12).padding(.vertical, 8)
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 10))
        .padding(.horizontal, 8)
    }

    @ViewBuilder
    private func queuedRows(_ queued: [UserRequest]) -> some View {
        ForEach(queued) { req in
            HStack(spacing: 8) {
                Image(systemName: "circle.fill")
                    .font(.system(size: 6))
                    .foregroundColor(.orange)
                Text(req.message)
                    .font(.caption)
                    .foregroundColor(.primary)
                    .lineLimit(1)
                    .frame(maxWidth: .infinity, alignment: .leading)
                Button("跳过") { onResolve(req.id, false) }
                    .font(.caption)
                    .buttonStyle(.plain)
                Button("已处理") { onResolve(req.id, true) }
                    .font(.caption)
                    .buttonStyle(.bordered)
            }
            .padding(.horizontal, 12).padding(.vertical, 4)
            .background(.ultraThinMaterial, in: RoundedRectangle(cornerRadius: 8))
            .padding(.horizontal, 8)
        }
    }
}
