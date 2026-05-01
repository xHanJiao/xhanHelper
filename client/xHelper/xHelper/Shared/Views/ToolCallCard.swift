import SwiftUI

struct ToolCallCard: View {
    let toolCall: ToolCallData

    var body: some View {
        HStack(spacing: 12) {
            Image(systemName: toolCall.icon)
                .font(.title3)
                .foregroundColor(.orange)

            VStack(alignment: .leading, spacing: 4) {
                Text("执行中...")
                    .font(.subheadline)
                    .fontWeight(.medium)

                Text(toolCall.displayName)
                    .font(.caption)
                    .foregroundColor(.secondary)

                ForEach(toolCall.argsSummary, id: \.0) { key, val in
                    Text("\(key): \(val)")
                        .font(.caption2)
                        .foregroundColor(.secondary)
                }
            }

            Spacer()

            ProgressView().scaleEffect(0.7)
        }
        .padding(12)
        .background(
            RoundedRectangle(cornerRadius: 12)
                .stroke(Color.orange, lineWidth: 1)
        )
        .padding(.horizontal)
    }
}
