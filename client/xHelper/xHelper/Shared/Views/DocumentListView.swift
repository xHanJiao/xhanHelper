import SwiftUI
import MarkdownUI

struct DocumentListView: View {
    @ObservedObject var viewModel: DocumentViewModel

    var body: some View {
        Group {
            if let doc = viewModel.openDocument {
                DocumentFullView(document: doc, onBack: { viewModel.openDocument = nil })
            } else {
                PageContainer(
                    title: "文档",
                    icon: "doc.text",
                    isLoading: viewModel.isLoading,
                    errorMessage: viewModel.errorMessage,
                    isEmpty: viewModel.documents.isEmpty && !viewModel.isLoading,
                    emptyMessage: viewModel.keyword.isEmpty ? "还没有文档" : "未找到匹配的文档",
                    onRetry: { Task { await viewModel.load() } }
                ) {
                    CardGridView(
                        items: viewModel.documents,
                        maxColumns: 1,
                        minCardWidth: 300,
                        onLoadMore: { await viewModel.loadMore() }
                    ) { doc in
                        CardConfig(
                            title: doc.title,
                            subtitle: doc.content,
                            tags: parseTags(doc.tags),
                            style: .document,
                            onTap: { viewModel.openDocument = doc }
                        )
                    }
                }
                .task { await viewModel.load() }
            }
        }
        // 同步选中态到顶栏文档按钮组（显隐由 Coordinator visible 闭包决定）
        .onChange(of: viewModel.openDocument) { _, doc in
            if let doc {
                DocumentSelectionCoordinator.shared.select(doc)
            } else {
                DocumentSelectionCoordinator.shared.deselect()
            }
        }
    }

    private func parseTags(_ tags: String?) -> [TagBadge] {
        guard let tags, !tags.isEmpty else { return [] }
        return tags.split(separator: ",").map {
            TagBadge(text: String($0).trimmingCharacters(in: .whitespaces), color: Color("appAccent"))
        }
    }
}

private struct TOCItem: Identifiable {
    var id: String { anchor }
    let level: Int
    let text: String
    let anchor: String
}

private func extractHeadings(_ markdown: String) -> [TOCItem] {
    let pattern = try! NSRegularExpression(pattern: #"^(#{1,6})\s+(.+)$"#, options: .anchorsMatchLines)
    let range = NSRange(markdown.startIndex..., in: markdown)
    return pattern.matches(in: markdown, range: range).compactMap { match in
        guard let levelRange = Range(match.range(at: 1), in: markdown),
              let textRange = Range(match.range(at: 2), in: markdown) else { return nil }
        let level = levelRange.upperBound.utf16Offset(in: markdown) - levelRange.lowerBound.utf16Offset(in: markdown)
        let rawText = String(markdown[textRange]).trimmingCharacters(in: .whitespaces)
        let anchor = rawText.lowercased()
            .components(separatedBy: .alphanumerics.inverted)
            .map { $0.lowercased() }
            .joined(separator: "-")
        return TOCItem(level: level, text: rawText, anchor: anchor)
    }
}

private struct DocumentFullView: View {
    let document: Document
    let onBack: () -> Void

    private var tocItems: [TOCItem] {
        extractHeadings(document.content)
    }

    var body: some View {
        VStack(spacing: 0) {
            HStack(spacing: 8) {
                Button(action: onBack) {
                    HStack(spacing: 4) {
                        Image(systemName: "chevron.left")
                            .font(.body.weight(.medium))
                        Text("返回文档列表")
                            .font(.body)
                    }
                    .foregroundColor(Color("appAccent"))
                }
                .buttonStyle(.plain)

                Spacer()
            }
            .padding(.horizontal, 16)
            .padding(.vertical, 10)

            Divider()

            HStack(alignment: .top, spacing: 0) {
                if !tocItems.isEmpty {
                    #if os(macOS)
                    if isTocCollapsed {
                        tocCollapsedStrip
                    } else {
                        tocSidebar
                    }
                    #else
                    tocSidebar
                    #endif
                    Divider()
                }

                ScrollViewReader { proxy in
                    ScrollView {
                        VStack(alignment: .leading, spacing: 14) {
                            Text(document.title)
                                .font(.system(size: FontSizeManager.shared.scaledSize * 1.65, weight: .semibold))

                            if let tags = document.tags, !tags.isEmpty {
                                HStack(spacing: 6) {
                                    ForEach(parseTags(document.tags)) { tag in
                                        Text(tag.text)
                                            .font(.system(size: FontSizeManager.shared.scaledSize * 0.76))
                                            .foregroundColor(tag.color)
                                            .padding(.horizontal, 8)
                                            .padding(.vertical, 3)
                                            .background(tag.color.opacity(0.12))
                                            .clipShape(Capsule())
                                    }
                                }
                            }

                            Divider()

                            let segments = MermaidUtility.splitContent(document.content)
                            if segments.isEmpty {
                                Markdown(document.content)
                                    .markdownTheme(.chatBubble(fontSize: FontSizeManager.shared.scaledSize * 1.18))
                                    .markdownCodeSyntaxHighlighter(.plainText)
                                    .macosTextSelection()
                            } else {
                                VStack(alignment: .leading, spacing: 0) {
                                    ForEach(segments) { segment in
                                        switch segment.type {
                                        case .markdown:
                                            Markdown(segment.content)
                                                .markdownTheme(.chatBubble(fontSize: FontSizeManager.shared.scaledSize * 1.18))
                                                .markdownCodeSyntaxHighlighter(.plainText)
                                                .macosTextSelection()
                                        case .mermaid:
                                            MermaidDiagramInlineView(code: segment.content)
                                        }
                                    }
                                }
                            }
                        }
                        .padding(24)
                        .frame(maxWidth: .infinity, alignment: .leading)
                    }
                    .onChange(of: scrollToAnchor) { _, anchor in
                        guard !anchor.isEmpty else { return }
                        proxy.scrollTo(anchor, anchor: .top)
                    }
                }
            }
        }
    }

    @State private var scrollToAnchor: String = ""
    /// 目录栏收起态（仅 macOS；不持久化，每次进文档默认展开）
    @State private var isTocCollapsed = false

    /// 展开态目录栏：标题行（右侧收起按钮）+ 标题大纲。
    private var tocSidebar: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: 4) {
                Text("目录")
                    .font(.caption.weight(.semibold))
                    .foregroundColor(.secondary)
                Spacer()
                #if os(macOS)
                Button {
                    withAnimation { isTocCollapsed = true }
                } label: {
                    Image(systemName: "chevron.left")
                        .font(.caption.weight(.semibold))
                        .foregroundColor(.secondary)
                        .frame(width: 24, height: 24)
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .help("收起目录")
                #endif
            }
            .padding(.horizontal, 16)
            .padding(.vertical, 8)

            Divider().opacity(0.3)

            ScrollView {
                VStack(alignment: .leading, spacing: 2) {
                    ForEach(tocItems) { item in
                        Button {
                            withAnimation {
                                scrollToAnchor = item.anchor
                            }
                        } label: {
                            Text(item.text)
                                .font(item.level <= 2 ? .caption : .caption2)
                                .foregroundColor(item.level == 1 ? .primary : .secondary)
                                .lineLimit(2)
                                .frame(maxWidth: .infinity, alignment: .leading)
                        }
                        .buttonStyle(.plain)
                        .padding(.horizontal, 16)
                        .padding(.vertical, 3)
                        .padding(.leading, CGFloat(item.level - 1) * 12)
                    }
                }
                .padding(.vertical, 8)
            }
        }
        .frame(width: 220)
    }

    /// 收起态窄条：保留展开入口，垂直居中一个 chevron.right。
    #if os(macOS)
    private var tocCollapsedStrip: some View {
        VStack(spacing: 0) {
            Spacer()
            Button {
                withAnimation { isTocCollapsed = false }
            } label: {
                Image(systemName: "chevron.right")
                    .font(.caption.weight(.semibold))
                    .foregroundColor(.secondary)
                    .frame(width: 28, height: 28)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .help("展开目录")
            Spacer()
        }
        .frame(width: 32)
    }
    #endif

    private func parseTags(_ tags: String?) -> [TagBadge] {
        guard let tags else { return [] }
        return tags.split(separator: ",").map {
            TagBadge(text: String($0).trimmingCharacters(in: .whitespaces), color: Color("appAccent"))
        }
    }
}
