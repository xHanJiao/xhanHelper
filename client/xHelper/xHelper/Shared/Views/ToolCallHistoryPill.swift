import SwiftUI

/// 历史消息中的工具调用小标签（已完成状态）
struct ToolCallHistoryPill: View {
    let toolCall: ToolCallHistoryItem

    var body: some View {
        HStack(spacing: 6) {
            Image(systemName: toolCall.icon)
                .font(.caption2)
                .foregroundColor(.green)

            Text("\(toolCall.tool).\(toolCall.function)")
                .font(.caption2)
                .foregroundColor(.secondary)
        }
        .padding(.horizontal, 8)
        .padding(.vertical, 4)
        .background(
            RoundedRectangle(cornerRadius: 6)
                .fill(Color.green.opacity(0.1))
        )
        .padding(.leading, 48)
    }
}
