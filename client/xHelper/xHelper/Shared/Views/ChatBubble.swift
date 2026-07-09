import SwiftUI
import MarkdownUI

struct ThinkingBubble: View {
    let text: String
    let isStreaming: Bool
    @State private var isExpanded = false

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            Button {
                withAnimation(.easeInOut(duration: 0.2)) { isExpanded.toggle() }
            } label: {
                HStack(spacing: 6) {
                    Image(systemName: isExpanded ? "chevron.down" : "chevron.forward")
                        .font(.caption2)
                    if isStreaming {
                        PulsingText("思考中...")
                    } else {
                        Text("思考内容")
                            .font(.caption)
                            .foregroundColor(.secondary)
                    }
                }
            }
            .buttonStyle(.plain)

            if isExpanded {
                Text(text)
                    .font(.caption)
                    .foregroundColor(.secondary)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(.leading, 16)
            }
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 8)
        .background(Color.gray.opacity(0.08))
        .cornerRadius(12)
    }
}

private struct PulsingText: View {
    let text: String
    @State private var opacity: Double = 1.0

    init(_ text: String) { self.text = text }

    var body: some View {
        Text(text)
            .font(.caption)
            .foregroundColor(.secondary)
            .opacity(opacity)
            .onAppear {
                withAnimation(.easeInOut(duration: 0.8).repeatForever(autoreverses: true)) {
                    opacity = 0.3
                }
            }
    }
}

struct ChatBubble: View {
    let text: String
    let isUser: Bool
    var isTool: Bool = false
    var messageId: String = ""
    var createdAt: String? = nil
    var showTime: Bool = false
    var onTapToShowTime: (() -> Void)? = nil

    private var isStreamingPlaceholder: Bool {
        messageId == "__streaming__" && text.isEmpty
    }

    var body: some View {
        VStack(spacing: 2) {
            HStack {
                if isUser || isTool { Spacer() }

                if isTool {
                    toolBubble
                } else if isUser {
                    Text(text)
                        .textSelection(.enabled)
                        .fixedSize(horizontal: false, vertical: true)
                        .padding(.horizontal, 14).padding(.vertical, 10)
                        .background(Color.blue).foregroundColor(.white).cornerRadius(18)
                        .contextMenu { copyButton }
                } else if isStreamingPlaceholder {
                    HStack(spacing: 5) {
                        ForEach(0..<3) { i in
                            DotView(delay: 0.2 * Double(i))
                        }
                    }
                    .padding(.horizontal, 14).padding(.vertical, 12)
                    .background(Color.gray.opacity(0.15))
                    .cornerRadius(18)
                } else {
                    Markdown(text)
                        .markdownTheme(.gitHub)
                        .markdownCodeSyntaxHighlighter(.plainText)
                        .frame(alignment: .leading)
                        .padding(.horizontal, 14).padding(.vertical, 10)
                        .background(Color.gray.opacity(0.15))
                        .cornerRadius(18)
                        .contextMenu { copyButton }
                }

                if !isUser && !isTool { Spacer() }
            }

            if let time = createdAt, showTime {
                Text(formatCreatedAt(time))
                    .font(.caption2)
                    .foregroundColor(.secondary)
                    .frame(maxWidth: .infinity, alignment: isUser ? .trailing : .leading)
            }
        }
        .padding(.horizontal)
        .onTapGesture {
            guard createdAt != nil else { return }
            onTapToShowTime?()
        }
    }

    private func formatCreatedAt(_ iso: String) -> String {
        let fmt = ISO8601DateFormatter()
        fmt.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        guard let date = fmt.date(from: iso) ?? ISO8601DateFormatter().date(from: iso) else {
            return iso
        }
        if Calendar.current.isDateInToday(date) {
            let f = DateFormatter()
            f.dateFormat = "HH:mm"
            return f.string(from: date)
        } else {
            let f = DateFormatter()
            f.dateFormat = "yyyy/MM/dd HH:mm:ss"
            return f.string(from: date)
        }
    }

    var copyButton: some View {
        #if os(iOS)
        Button {
            UIPasteboard.general.string = text
        } label: {
            Label("拷贝", systemImage: "doc.on.doc")
        }
        #else
        Button {
            NSPasteboard.general.clearContents()
            NSPasteboard.general.setString(text, forType: .string)
        } label: {
            Label("拷贝", systemImage: "doc.on.doc")
        }
        #endif
    }

    var toolBubble: some View {
        HStack(spacing: 6) {
            Image(systemName: "checkmark.circle.fill")
                .font(.caption).foregroundColor(.green)
            Text(text).font(.caption).foregroundColor(.secondary).fixedSize(horizontal: false, vertical: true)
            Spacer()
        }
        .padding(.horizontal, 12).padding(.vertical, 8)
        .background(Color.orange.opacity(0.1)).cornerRadius(12)
    }
}

private struct DotView: View {
    let delay: Double
    @State private var animating = false

    var body: some View {
        Circle()
            .frame(width: 7, height: 7)
            .foregroundColor(.gray.opacity(0.4))
            .scaleEffect(animating ? 1.3 : 0.7)
            .animation(.easeInOut(duration: 0.6).repeatForever().delay(delay), value: animating)
            .onAppear { animating = true }
    }
}
