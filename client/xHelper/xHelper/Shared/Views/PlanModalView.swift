import SwiftUI

struct PlanModalView: View {
    @ObservedObject var viewModel: ChatViewModel

    var body: some View {
        if viewModel.isPlanModalVisible, !viewModel.planNodes.isEmpty {
            ZStack {
                Color.black.opacity(0.35)
                    .ignoresSafeArea()
                    .onTapGesture { }

                VStack(spacing: 0) {
                    // 标题栏
                    HStack {
                        HStack(spacing: 6) {
                            Image(systemName: "point.3.connected.trianglepath.dotted")
                                .foregroundColor(.blue)
                            Text("Agent 执行中")
                                .font(.headline)
                        }
                        Spacer()
                        Button { withAnimation { viewModel.isPlanMinimized = true } } label: {
                            Image(systemName: "minus.circle.fill")
                                .font(.title3)
                                .foregroundColor(.secondary)
                        }
                        Button {
                            withAnimation(.easeOut(duration: 0.2)) {
                                viewModel.isPlanModalVisible = false
                            }
                        } label: {
                            Image(systemName: "xmark.circle.fill")
                                .font(.title3)
                                .foregroundColor(.secondary)
                        }
                    }
                    .padding(.horizontal, 20)
                    .padding(.top, 16)
                    .padding(.bottom, 12)

                    Divider().padding(.horizontal)

                    // 节点流
                    ScrollView {
                        VStack(spacing: 0) {
                            // 根节点
                            FlowRootNode()
                            FlowConnector()
                            // 工具节点
                            ForEach(Array(viewModel.planNodes.enumerated()), id: \.element.id) { idx, node in
                                FlowToolNode(
                                    node: node,
                                    icon: toolIcon(node.label),
                                    isLast: idx == viewModel.planNodes.count - 1
                                )
                                if idx < viewModel.planNodes.count - 1 {
                                    FlowConnector()
                                }
                            }
                            FlowConnector()
                            // 终节点
                            FlowEndNode(isDone: !viewModel.isStreaming)
                        }
                        .padding(.vertical, 20)
                        .padding(.horizontal, 24)
                    }

                    // 底部状态栏
                    HStack {
                        HStack(spacing: 6) {
                            Circle().fill(statusIndicatorColor).frame(width: 8, height: 8)
                            Text(statusIndicatorText)
                                .font(.caption)
                                .foregroundColor(.secondary)
                        }
                        Spacer()
                        Text("\(viewModel.planNodes.count) 个步骤")
                            .font(.caption2)
                            .foregroundColor(.secondary)
                    }
                    .padding(.horizontal, 20)
                    .padding(.bottom, 12)
                }
                .frame(maxWidth: 380, maxHeight: 540)
                .background(.regularMaterial)
                .cornerRadius(20)
                .shadow(color: .black.opacity(0.15), radius: 30, y: 10)
                .padding(24)
            }
        }
    }

    private var statusIndicatorText: String {
        let running = viewModel.planNodes.filter { $0.status == "running" }.count
        let failed = viewModel.planNodes.filter { $0.status == "failed" }.count
        if failed > 0 { return "\(failed) 个失败" }
        if running > 0 { return "\(running) 个执行中" }
        return "全部完成"
    }

    private var statusIndicatorColor: Color {
        if viewModel.planNodes.contains(where: { $0.status == "failed" }) { return .red }
        if viewModel.planNodes.contains(where: { $0.status == "running" }) { return .blue }
        return .green
    }
}

// MARK: - 根节点（用户询问）

struct FlowRootNode: View {
    @State private var appeared = false

    var body: some View {
        HStack(spacing: 12) {
            ZStack {
                Circle()
                    .fill(LinearGradient(
                        colors: [Color.blue.opacity(0.7), Color.purple.opacity(0.7)],
                        startPoint: .topLeading, endPoint: .bottomTrailing))
                    .frame(width: 40, height: 40)
                Image(systemName: "person.fill")
                    .font(.system(size: 16, weight: .medium))
                    .foregroundColor(.white)
            }
            Text("用户询问")
                .font(.subheadline.weight(.semibold))
            Spacer()
            Image(systemName: "checkmark.circle.fill")
                .foregroundColor(.green)
                .opacity(appeared ? 1 : 0)
                .scaleEffect(appeared ? 1 : 0.3)
        }
        .padding(12)
        .background(
            RoundedRectangle(cornerRadius: 14)
                .fill(Color.blue.opacity(0.06))
        )
        .onAppear {
            withAnimation(.spring(response: 0.5, dampingFraction: 0.6).delay(0.1)) {
                appeared = true
            }
        }
    }
}

// MARK: - 工具节点

struct FlowToolNode: View {
    let node: PlanNode
    let icon: String
    let isLast: Bool
    @State private var appeared = false

    var body: some View {
        HStack(spacing: 12) {
            ZStack {
                Circle()
                    .fill(nodeColor.opacity(0.15))
                    .frame(width: 40, height: 40)
                Image(systemName: icon)
                    .font(.system(size: 16, weight: .medium))
                    .foregroundColor(nodeColor)
            }
            .overlay {
                if node.status == "running" {
                    Circle()
                        .stroke(nodeColor, lineWidth: 2)
                        .scaleEffect(1.3)
                        .opacity(0.4)
                        .animation(.easeInOut(duration: 1).repeatForever(autoreverses: true), value: node.status)
                }
            }

            VStack(alignment: .leading, spacing: 2) {
                Text(nodeFunctionDisplayName(node.label))
                    .font(.subheadline.weight(.medium))
                Text(statusText)
                    .font(.caption)
                    .foregroundColor(nodeColor)
            }
            Spacer()
            statusIcon
        }
        .padding(12)
        .background(
            RoundedRectangle(cornerRadius: 14)
                .fill(nodeColor.opacity(0.06))
                .overlay(
                    RoundedRectangle(cornerRadius: 14)
                        .stroke(nodeColor.opacity(node.status == "running" ? 0.3 : 0.1), lineWidth: 1)
                )
        )
        .shadow(color: node.status == "running" ? nodeColor.opacity(0.15) : .clear, radius: 8, y: 4)
        .scaleEffect(appeared ? 1 : 0.9)
        .opacity(appeared ? 1 : 0)
        .onAppear {
            withAnimation(.spring(response: 0.4, dampingFraction: 0.7).delay(0.05)) {
                appeared = true
            }
        }
    }

    private var nodeColor: Color {
        switch node.status {
        case "running": return .blue
        case "done":    return .green
        case "failed":  return .red
        default:        return .secondary
        }
    }

    private var statusText: String {
        switch node.status {
        case "running": return "执行中..."
        case "done":    return "完成"
        case "failed":  return "失败"
        default:        return "等待中"
        }
    }

    @ViewBuilder
    private var statusIcon: some View {
        switch node.status {
        case "running":
            ProgressView().scaleEffect(0.7)
        case "done":
            Image(systemName: "checkmark.circle.fill")
                .foregroundColor(.green)
                .transition(.scale.combined(with: .opacity))
        case "failed":
            Image(systemName: "xmark.circle.fill")
                .foregroundColor(.red)
                .transition(.scale.combined(with: .opacity))
        default:
            Circle().stroke(Color.secondary.opacity(0.3), lineWidth: 2).frame(width: 18, height: 18)
        }
    }
}

// MARK: - 终节点

struct FlowEndNode: View {
    let isDone: Bool
    @State private var appeared = false

    var body: some View {
        HStack(spacing: 12) {
            ZStack {
                Circle()
                    .fill(isDone ? Color.green.opacity(0.15) : Color.secondary.opacity(0.1))
                    .frame(width: 40, height: 40)
                Image(systemName: isDone ? "checkmark" : "ellipsis")
                    .font(.system(size: 16, weight: .medium))
                    .foregroundColor(isDone ? .green : .secondary)
            }
            Text(isDone ? "完成" : "等待 LLM 回复...")
                .font(.subheadline.weight(.semibold))
                .foregroundColor(isDone ? .primary : .secondary)
            Spacer()
            if isDone {
                Image(systemName: "checkmark.circle.fill")
                    .foregroundColor(.green)
                    .opacity(appeared ? 1 : 0)
                    .scaleEffect(appeared ? 1 : 0.3)
            } else {
                ProgressView().scaleEffect(0.7)
            }
        }
        .padding(12)
        .background(
            RoundedRectangle(cornerRadius: 14)
                .fill(isDone ? Color.green.opacity(0.06) : Color.secondary.opacity(0.04))
        )
        .onAppear {
            if isDone {
                withAnimation(.spring(response: 0.5, dampingFraction: 0.6).delay(0.2)) {
                    appeared = true
                }
            }
        }
    }
}

// MARK: - 连接线

struct FlowConnector: View {
    @State private var draw = false

    var body: some View {
        HStack {
            RoundedRectangle(cornerRadius: 1)
                .fill(LinearGradient(
                    colors: [Color.secondary.opacity(0.15), Color.blue.opacity(0.2)],
                    startPoint: .top, endPoint: .bottom))
                .frame(width: 2.5, height: draw ? 24 : 0)
                .padding(.leading, 19)
            Spacer()
        }
        .onAppear {
            withAnimation(.easeOut(duration: 0.4)) {
                draw = true
            }
        }
    }
}

// MARK: - 工具函数名映射

private func toolIcon(_ name: String) -> String {
    if name.contains("create") || name.contains("record") { return "plus.circle.fill" }
    if name.contains("update") { return "pencil.circle.fill" }
    if name.contains("delete") || name.contains("cancel") { return "trash.circle.fill" }
    if name.contains("query") || name.contains("search") { return "magnifyingglass.circle.fill" }
    if name.contains("stats") || name.contains("analyze") { return "chart.bar.fill" }
    if name.contains("note") { return "note.text.badge.plus" }
    if name.contains("time") || name.contains("date") { return "clock.fill" }
    if name.contains("calc") || name.contains("math") { return "function" }
    if name.contains("uuid") || name.contains("generate") { return "number.circle.fill" }
    return "wrench.fill"
}

private func nodeFunctionDisplayName(_ name: String) -> String {
    let m: [String: String] = [
        "create_bill": "创建账单", "update_bill": "修改账单", "cancel_bill": "作废账单",
        "query_bills": "查询账单", "get_monthly_stats": "月度统计", "get_category_stats": "分类统计",
        "get_current_time": "获取时间", "evaluate": "计算", "generate": "生成 UUID",
        "record_memory": "记录记忆", "query_notes": "查询笔记",
    ]
    return m[name] ?? name
}
