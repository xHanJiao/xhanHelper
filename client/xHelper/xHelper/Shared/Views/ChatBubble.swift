import SwiftUI
import MarkdownUI

struct ChatBubble: View {
    let text: String
    let isUser: Bool
    var isTool: Bool = false
    var messageId: String = ""

    private var isStreamingPlaceholder: Bool {
        messageId == "__streaming__" && text.isEmpty
    }

    var body: some View {
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
        .padding(.horizontal)
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
