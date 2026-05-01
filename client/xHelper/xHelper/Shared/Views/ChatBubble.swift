import SwiftUI

struct ChatBubble: View {
    let text: String
    let isUser: Bool
    var isTool: Bool = false

    var body: some View {
        HStack {
            if isUser || isTool { Spacer() }

            if isTool {
                toolBubble
            } else {
                Text(text.isEmpty && !isUser ? " " : text)
                    .padding(.horizontal, 14)
                    .padding(.vertical, 10)
                    .background(isUser ? Color.blue : Color.gray.opacity(0.15))
                    .foregroundColor(isUser ? .white : .primary)
                    .cornerRadius(18)
            }

            if !isUser && !isTool { Spacer() }
        }
        .padding(.horizontal)
    }

    var toolBubble: some View {
        HStack(spacing: 6) {
            Image(systemName: "checkmark.circle.fill")
                .font(.caption)
                .foregroundColor(.green)
            Text(text)
                .font(.caption)
                .foregroundColor(.secondary)
            Spacer()
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 8)
        .background(Color.orange.opacity(0.1))
        .cornerRadius(12)
    }
}
