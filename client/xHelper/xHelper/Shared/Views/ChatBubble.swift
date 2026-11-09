import SwiftUI
import MarkdownUI
#if os(iOS)
import UIKit
#endif

extension Theme {
    static func chatBubble(fontSize: CGFloat) -> Theme {
        Theme.gitHub
            .text {
                ForegroundColor(Color("assistantText"))
                BackgroundColor(.clear)
                FontSize(fontSize)
            }
            .link {
                // 覆盖 gitHub 内置蓝，对齐文档正文链接色（--accent / appAccent）
                ForegroundColor(Color("appAccent"))
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
            }
        }
        .padding(.leading, 16)
        .padding(.vertical, 4)
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

/// 句级朗读进度：由 VoiceChatService 的流水线给出。
/// `sentences` 必须能拼回原文，否则退回 markdown（切分规则不一致时别丢字）。
struct SpeechProgress: Equatable {
    var sentences: [String]
    var index: Int
}

struct ChatBubble: View {
    let text: String
    let isUser: Bool
    var messageId: String = ""
    var createdAt: String? = nil
    var showTime: Bool = false
    var onTapToShowTime: (() -> Void)? = nil
    /// 正在朗读时按句上色：已播正常 / 当前高亮加粗 / 未播淡显
    var speech: SpeechProgress? = nil
    @EnvironmentObject var fontSizeManager: FontSizeManager

    private var chatFont: Font {
        #if os(iOS)
        return .system(size: UIFontMetrics.default.scaledValue(for: fontSizeManager.scaledSize))
        #else
        return .system(size: fontSizeManager.scaledSize)
        #endif
    }

    private var isStreamingPlaceholder: Bool {
        messageId == "__streaming__" && text.isEmpty
    }

    var body: some View {
        VStack(spacing: 2) {
            HStack {
                if isUser { Spacer() }

                if isUser {
                    Text(text)
                        .font(chatFont)
                        .fixedSize(horizontal: false, vertical: true)
                        .padding(.horizontal, 14).padding(.vertical, 10)
                        .foregroundColor(Color("userText"))
                } else if isStreamingPlaceholder {
                    HStack(spacing: 5) {
                        ForEach(0..<3) { i in
                            DotView(delay: 0.2 * Double(i))
                        }
                    }
                    .padding(.horizontal, 14).padding(.vertical, 12)
                } else if let progress = speech,
                          messageId == "__streaming__" || progress.sentences.joined() == text {
                    // 流式气泡的正文比流水线慢一拍（ViewModel 100ms 节流），以流水线文本为准
                    speechText(progress)
                        .font(chatFont)
                        .fixedSize(horizontal: false, vertical: true)
                        .padding(.horizontal, 14).padding(.vertical, 10)
                        .macosTextSelection()
                        .frame(maxWidth: .infinity, alignment: .leading)
                } else {
                    let isStreaming = messageId == "__streaming__"
                    let segments = isStreaming ? [] : MermaidUtility.splitContent(text)
                    Group {
                        if isStreaming || segments.isEmpty {
                            Markdown(text)
                                .markdownTheme(.chatBubble(fontSize: fontSizeManager.scaledSize))
                                .markdownCodeSyntaxHighlighter(.plainText)
                                .frame(alignment: .leading)
                                .macosTextSelection()
                        } else {
                            VStack(alignment: .leading, spacing: 0) {
                                ForEach(segments) { segment in
                                    switch segment.type {
                                    case .markdown:
                                        Markdown(segment.content)
                                            .markdownTheme(.chatBubble(fontSize: fontSizeManager.scaledSize))
                                            .markdownCodeSyntaxHighlighter(.plainText)
                                            .frame(alignment: .leading)
                                            .macosTextSelection()
                                    case .mermaid:
                                        MermaidDiagramInlineView(code: segment.content)
                                    }
                                }
                            }
                        }
                    }
                    .padding(.horizontal, 14).padding(.vertical, 10)
                }

                if !isUser { Spacer() }
            }

            // macOS：始终占位、只切透明度，保住行高 → 消息间距不随显示/隐藏变化。
            // iOS：保持原样（按需出现，出现时撑开行高）。
            #if os(macOS)
            if let time = createdAt { timeRow(time) }
            #else
            if let time = createdAt, showTime { timeRow(time) }
            #endif
        }
        .padding(.horizontal, Layout.chatHorizontalPadding)
        // iOS：点气泡显示时间（顺带收键盘）。macOS：用 simultaneousGesture，普通 onTapGesture
        // 会抢在 MarkdownUI 内链之前吃掉点击；simultaneous 让内链照常打开、同时切时间。
        #if os(iOS)
        .onTapGesture {
            guard createdAt != nil else { return }
            UIApplication.shared.sendAction(#selector(UIResponder.resignFirstResponder), to: nil, from: nil, for: nil)
            onTapToShowTime?()
        }
        #else
        .simultaneousGesture(TapGesture().onEnded {
            guard createdAt != nil else { return }
            onTapToShowTime?()
        })
        #endif
    }

    private func speechText(_ progress: SpeechProgress) -> Text {
        let base = Color("assistantText")
        return progress.sentences.enumerated().reduce(Text("")) { acc, item in
            var run = Text(item.element)
            if item.offset < progress.index {
                run = run.foregroundColor(base)
            } else if item.offset == progress.index {
                run = run.bold().foregroundColor(base)
            } else {
                run = run.foregroundColor(base.opacity(0.45))
            }
            return acc + run
        }
    }

    @ViewBuilder
    private func timeRow(_ time: String) -> some View {
        Text(formatCreatedAt(time))
            .font(.caption2)
            .foregroundColor(.secondary)
            .padding(.horizontal, 14)
            .frame(maxWidth: .infinity, alignment: isUser ? .trailing : .leading)
            .opacity(showTime ? 1 : 0)
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
