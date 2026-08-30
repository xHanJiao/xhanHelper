import SwiftUI

// MARK: - AskUserBar — 子任务用户介入条（非模态悬浮条）
//
// 挂在输入栏上方（L3 确认条同区）。与 ToolActivityToast 不同：
// - 独立发布状态（ChatViewModel.pendingUserRequests），不被文本流清理
// - 多 task 并发时堆叠 + 折叠：第 1 个完整展示，其余折叠为计数，展开后逐条处理
// 交互：
// - 输入框（可选）：用户可直接回答子任务的问题（LLM 的问题可能是选择题/需要具体回答）
// - [已处理]：无文本直接确认继续；有文本时按钮变为 [发送]（带回答文本）
// - [跳过]：子循环换方案
// - 超时自动视为跳过（服务端预算由下发 timeout_secs 控制）

struct AskUserBar: View {
    let requests: [UserRequest]
    let onResolve: (String, String?) -> Void // (callID, reply)：nil=跳过；""=已处理；非空=回答
    @State private var expanded = false
    @State private var drafts: [String: String] = [:]

    var body: some View {
        VStack(spacing: 6) {
            if let first = requests.first {
                primaryCard(first)
                if expanded && requests.count > 1 {
                    queuedRows(Array(requests.dropFirst()))
                }
            }
        }
        .transition(.move(edge: .bottom).combined(with: .opacity))
    }

    private func draftBinding(for id: String) -> Binding<String> {
        Binding(
            get: { drafts[id] ?? "" },
            set: { drafts[id] = $0 }
        )
    }

    private func primaryCard(_ req: UserRequest) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 8) {
                Image(systemName: "hand.raised.fill")
                    .font(.caption)
                    .foregroundColor(.orange)
                Text(req.message)
                    .font(.caption)
                    .foregroundColor(.primary)
                    .lineLimit(2)
                    .frame(maxWidth: .infinity, alignment: .leading)
                if req.hasHandoffTab {
                    Button("前往处理") {
                        if let owner = req.owner {
                            BrowserToolProvider.shared.activateHandoffTab(owner)
                        }
                    }
                    .font(.caption)
                    .buttonStyle(.borderedProminent)
                    .help("切到待处理的浏览器页面")
                }
                if requests.count > 1 {
                    Button(expanded ? "收起" : "+\(requests.count - 1) 待处理") {
                        withAnimation(.easeInOut(duration: 0.2)) { expanded.toggle() }
                    }
                    .font(.caption)
                    .buttonStyle(.plain)
                }
                Button("跳过") {
                    drafts[req.id] = nil
                    onResolve(req.id, nil)
                }
                .font(.caption)
                .buttonStyle(.bordered)
                let draft = drafts[req.id] ?? ""
                if draft.isEmpty {
                    Button("已处理") {
                        onResolve(req.id, "")
                    }
                    .font(.caption)
                    .buttonStyle(.borderedProminent)
                } else {
                    Button("发送") {
                        let reply = drafts[req.id] ?? ""
                        drafts[req.id] = nil
                        onResolve(req.id, reply)
                    }
                    .font(.caption)
                    .buttonStyle(.borderedProminent)
                }
            }

            // 回答输入框（可选）：子任务的问题可能需要具体回答，不只 ok/deny
            TextField("回复子任务（可选，如：选 1）", text: draftBinding(for: req.id))
                .textFieldStyle(.plain)
                .font(.caption)
                .padding(.horizontal, 8).padding(.vertical, 5)
                .background(.ultraThinMaterial, in: RoundedRectangle(cornerRadius: 6))
        }
        .padding(.horizontal, 12).padding(.vertical, 8)
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 10))
    }

    @ViewBuilder
    private func queuedRows(_ queued: [UserRequest]) -> some View {
        ForEach(queued) { req in
            VStack(alignment: .leading, spacing: 4) {
                HStack(spacing: 8) {
                    Image(systemName: "circle.fill")
                        .font(.system(size: 6))
                        .foregroundColor(.orange)
                    Text(req.message)
                        .font(.caption)
                        .foregroundColor(.primary)
                        .lineLimit(1)
                        .frame(maxWidth: .infinity, alignment: .leading)
                    Button("跳过") { onResolve(req.id, nil) }
                        .font(.caption)
                        .buttonStyle(.plain)
                    let draft = drafts[req.id] ?? ""
                    if draft.isEmpty {
                        Button("已处理") { onResolve(req.id, "") }
                            .font(.caption)
                            .buttonStyle(.bordered)
                    } else {
                        Button("发送") {
                            let reply = drafts[req.id] ?? ""
                            drafts[req.id] = nil
                            onResolve(req.id, reply)
                        }
                        .font(.caption)
                        .buttonStyle(.borderedProminent)
                    }
                }
                TextField("回复子任务（可选）", text: draftBinding(for: req.id))
                    .textFieldStyle(.plain)
                    .font(.caption)
                    .padding(.horizontal, 8).padding(.vertical, 4)
                    .background(.ultraThinMaterial, in: RoundedRectangle(cornerRadius: 6))
            }
            .padding(.horizontal, 12).padding(.vertical, 6)
            .background(.ultraThinMaterial, in: RoundedRectangle(cornerRadius: 8))
        }
    }
}
