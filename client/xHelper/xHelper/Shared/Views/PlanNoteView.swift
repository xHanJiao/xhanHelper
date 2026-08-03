import SwiftUI

/// 执行计划便签：窄幅、右停靠在驾驶舱上缘，底部贴着胶囊上缘，点击展开/收起。
///
/// 由 InputBar 的 `topTrailingNote` 注入点渲染在 720 胶囊上方并右对齐；
/// 宽度随文字内容自适应，最宽由 InputBar 限制为输入框宽度的一半。
/// 展开态显示 goal + 步骤 + 进度条，收起态为一小条胶囊。无计划时为空视图。
struct PlanNoteView: View {
    let goal: String
    let steps: [PlanStep]
    let expanded: Bool
    let onToggle: () -> Void
    let onDismiss: () -> Void

    var body: some View {
        Group {
            if expanded {
                expandedCard
                    .transition(.scale(scale: 0.95).combined(with: .opacity))
            } else {
                collapsedPill
                    .transition(.scale(scale: 0.95).combined(with: .opacity))
            }
        }
        .animation(.easeOut(duration: 0.2), value: expanded)
    }

    private var expandedCard: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(spacing: 6) {
                Text(goal.isEmpty ? "执行计划" : goal)
                    .font(.caption).bold()
                    .foregroundColor(.secondary)
                    .lineLimit(1)
                Button {
                    onDismiss()
                } label: {
                    Image(systemName: "xmark.circle.fill")
                        .font(.caption2)
                        .foregroundColor(.secondary)
                }
                .buttonStyle(.plain)
                .help("隐藏计划")
            }
            ForEach(steps) { step in
                HStack(spacing: 5) {
                    Image(systemName: Self.icon(step.status))
                        .font(.caption2)
                        .foregroundColor(Self.color(step.status))
                    Text(step.content)
                        .font(.caption)
                        .lineLimit(1)
                        .strikethrough(step.status == "completed" || step.status == "cancelled")
                        .foregroundColor(step.status == "cancelled" ? .secondary : .primary)
                }
            }
        }
        .padding(10)
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 10))
        .shadow(color: .black.opacity(0.12), radius: 8, y: 4)
        .contentShape(Rectangle())
        .onTapGesture { onToggle() }
    }

    private var collapsedPill: some View {
        HStack(spacing: 5) {
            Image(systemName: "chevron.down")
                .font(.caption2)
                .foregroundColor(.secondary)
            Text(goal.isEmpty ? "执行计划" : goal)
                .font(.caption).bold()
                .foregroundColor(.secondary)
                .lineLimit(1)
            if totalSteps > 0 {
                Text("\(doneSteps)/\(totalSteps)")
                    .font(.caption2)
                    .foregroundColor(.secondary)
            }
            Button {
                onDismiss()
            } label: {
                Image(systemName: "xmark.circle.fill")
                    .font(.caption2)
                    .foregroundColor(.secondary)
            }
            .buttonStyle(.plain)
            .help("隐藏计划")
        }
        .padding(.horizontal, 10)
        .padding(.vertical, 5)
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 8))
        .shadow(color: .black.opacity(0.12), radius: 6, y: 3)
        .contentShape(Rectangle())
        .onTapGesture { onToggle() }
    }

    private var totalSteps: Int { steps.count }
    private var doneSteps: Int { steps.filter { $0.status == "completed" }.count }

    static func icon(_ status: String) -> String {
        switch status {
        case "completed": return "checkmark.circle.fill"
        case "in_progress": return "circle.inset.filled"
        case "cancelled": return "xmark.circle"
        default: return "circle"
        }
    }

    static func color(_ status: String) -> Color {
        switch status {
        case "completed": return .green
        case "in_progress": return .orange
        case "cancelled": return .secondary
        default: return .gray
        }
    }
}
