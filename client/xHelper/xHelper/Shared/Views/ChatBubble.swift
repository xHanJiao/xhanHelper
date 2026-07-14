import SwiftUI
import MarkdownUI
#if os(iOS)
import UIKit
#endif

extension Theme {
    static let chatBubble: Theme = Theme.gitHub
        .text {
            ForegroundColor(Color("assistantText"))
            BackgroundColor(.clear)
            FontSize(17)
        }
        .code {
            FontFamilyVariant(.monospaced)
            FontSize(.em(0.85))
            BackgroundColor(.clear)
        }
        .codeBlock { configuration in
            ScrollView(.horizontal) {
                configuration.label
                    .fixedSize(horizontal: false, vertical: true)
                    .relativeLineSpacing(.em(0.225))
                    .markdownTextStyle {
                        FontFamilyVariant(.monospaced)
                        FontSize(.em(0.85))
                    }
                    .padding(14)
            }
            .background(.ultraThinMaterial, in: RoundedRectangle(cornerRadius: 10))
            .markdownMargin(top: 0, bottom: 16)
        }
        .blockquote { configuration in
            HStack(spacing: 0) {
                RoundedRectangle(cornerRadius: 3)
                    .fill(Color.secondary.opacity(0.4))
                    .frame(width: 3)
                configuration.label
                    .markdownTextStyle { ForegroundColor(Color("assistantText")) }
                    .padding(.leading, 12)
            }
            .fixedSize(horizontal: false, vertical: true)
        }
        .table { configuration in
            configuration.label
                .fixedSize(horizontal: false, vertical: true)
                .markdownTableBorderStyle(.init(color: .secondary.opacity(0.3)))
                .markdownTableBackgroundStyle(
                    .alternatingRows(.clear, Color.secondary.opacity(0.06))
                )
                .markdownMargin(top: 0, bottom: 16)
        }
}

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
        .background(.ultraThinMaterial, in: RoundedRectangle(cornerRadius: 12))
        .overlay(RoundedRectangle(cornerRadius: 12).stroke(.white.opacity(0.35), lineWidth: 0.6))
        .shadow(color: .black.opacity(0.06), radius: 4, y: 2)
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
                if isUser { Spacer() }

                if isUser {
                    Text(text)
                        .fixedSize(horizontal: false, vertical: true)
                        .padding(.horizontal, 14).padding(.vertical, 10)
                        .foregroundColor(Color("userText"))
                        .background(.ultraThinMaterial, in: RoundedRectangle(cornerRadius: 14))
                        .background(Color("bubbleUserTint"), in: RoundedRectangle(cornerRadius: 14))
                        .overlay(RoundedRectangle(cornerRadius: 14).stroke(.white.opacity(0.35), lineWidth: 0.6))
                        .shadow(color: .black.opacity(0.06), radius: 4, y: 2)
                } else if isStreamingPlaceholder {
                    HStack(spacing: 5) {
                        ForEach(0..<3) { i in
                            DotView(delay: 0.2 * Double(i))
                        }
                    }
                    .padding(.horizontal, 14).padding(.vertical, 12)
                    .background(.ultraThinMaterial, in: RoundedRectangle(cornerRadius: 18))
                    .overlay(RoundedRectangle(cornerRadius: 18).stroke(.white.opacity(0.35), lineWidth: 0.6))
                    .shadow(color: .black.opacity(0.06), radius: 4, y: 2)
                } else {
                    Markdown(text)
                        .markdownTheme(.chatBubble)
                        .markdownCodeSyntaxHighlighter(.plainText)
                        .frame(alignment: .leading)
                        .padding(.horizontal, 14).padding(.vertical, 10)
                        .background(.ultraThinMaterial, in: RoundedRectangle(cornerRadius: 18))
                        .overlay(RoundedRectangle(cornerRadius: 18).stroke(.white.opacity(0.35), lineWidth: 0.6))
                        .shadow(color: .black.opacity(0.06), radius: 4, y: 2)
                }

                if !isUser { Spacer() }
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
            #if os(iOS)
            UIApplication.shared.sendAction(#selector(UIResponder.resignFirstResponder), to: nil, from: nil, for: nil)
            #endif
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
