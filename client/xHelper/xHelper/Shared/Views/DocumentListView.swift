import SwiftUI
import MarkdownUI
#if os(macOS)
import AppKit
#endif

struct DocumentListView: View {
    @ObservedObject var viewModel: DocumentViewModel

    var body: some View {
        Group {
            if let doc = viewModel.openDocument {
                DocumentFullView(document: doc, onBack: { viewModel.openDocument = nil })
            } else {
                #if os(macOS)
                if viewModel.mode == .cloud {
                    cloudContent
                } else {
                    listContent
                }
                #else
                listContent
                #endif
            }
        }
        .task { await viewModel.load() }
        // 同步选中态到顶栏文档按钮组（显隐由 Coordinator visible 闭包决定）
        .onChange(of: viewModel.openDocument) { _, doc in
            if let doc {
                DocumentSelectionCoordinator.shared.select(doc)
            } else {
                DocumentSelectionCoordinator.shared.deselect()
            }
        }
    }

    @ViewBuilder
    private var listContent: some View {
        VStack(spacing: 0) {
            if let tag = viewModel.selectedTag {
                activeTagBar(tag)
            }
            listPage
        }
    }

    private var listPage: some View {
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
                    footerText: formatConversationDate(doc.updatedAt),
                    style: .document,
                    onTap: { viewModel.openDocument = doc }
                )
            }
        }
    }

    private func activeTagBar(_ tag: String) -> some View {
        HStack(spacing: 6) {
            Image(systemName: "tag.fill")
                .font(.system(size: 11))
            Text(tag)
                .font(.system(size: 12, weight: .medium))
            Button {
                Task { await viewModel.clearTag() }
            } label: {
                Image(systemName: "xmark.circle.fill")
                    .font(.system(size: 12))
            }
            .buttonStyle(.plain)
            .help("清除标签筛选")
            Spacer()
        }
        .foregroundColor(Color("appAccent"))
        .padding(.horizontal, 16)
        .padding(.vertical, 8)
        .background(Color("appAccent").opacity(0.1))
    }

    #if os(macOS)
    @ViewBuilder
    private var cloudContent: some View {
        let visible = viewModel.tags.filter { $0.count >= viewModel.minTagCount }
        PageContainer(
            title: "标签词云",
            icon: "tag",
            isLoading: viewModel.isLoading,
            errorMessage: viewModel.errorMessage,
            isEmpty: visible.isEmpty && !viewModel.isLoading,
            emptyMessage: viewModel.tags.isEmpty ? "还没有标签" : "没有达到 \(viewModel.minTagCount) 篇的标签",
            onRetry: { Task { await viewModel.load() } }
        ) {
            TagCloudView(tags: visible) { tag in
                Task { await viewModel.selectTag(tag) }
            }
        }
    }
    #endif

    private func parseTags(_ tags: String?) -> [TagBadge] {
        guard let tags, !tags.isEmpty else { return [] }
        return tags.split(separator: ",").map {
            TagBadge(text: String($0).trimmingCharacters(in: .whitespaces), color: Color("appAccent"))
        }
    }
}

#if os(macOS)
/// 标签词云：圆盘内分行排布，频次越高字号越大且越靠中心；一屏缩放贴合，不滚动。
private struct TagCloudView: View {
    let tags: [DocumentTag]
    let onTap: (String) -> Void

    @State private var placed: [PlacedTag] = []

    var body: some View {
        GeometryReader { geo in
            ZStack(alignment: .topLeading) {
                ForEach(placed) { item in
                    Button {
                        onTap(item.tag.name)
                    } label: {
                        Text(item.tag.name)
                            .font(.system(size: item.fontSize, weight: item.weight))
                            .foregroundColor(Color.primary.opacity(item.opacity))
                            .fixedSize()
                            .contentShape(Rectangle())
                    }
                    .buttonStyle(.plain)
                    .help("\(item.tag.count) 篇文档")
                    .position(x: item.center.x, y: item.center.y)
                }
            }
            .frame(width: geo.size.width, height: geo.size.height)
            .onAppear { rebuild(in: geo.size) }
            .onChange(of: geo.size) { _, size in rebuild(in: size) }
            .onChange(of: tags) { _, _ in rebuild(in: geo.size) }
        }
    }

    private func rebuild(in size: CGSize) {
        guard size.width > 1, size.height > 1 else { return }
        placed = BallCloud.place(tags: tags, in: size)
    }
}

private struct PlacedTag: Identifiable {
    let tag: DocumentTag
    let center: CGPoint
    let fontSize: CGFloat
    let weight: Font.Weight
    let opacity: Double

    var id: String { tag.name }
}

/// 圆盘分行排布：标签按频次降序，从中心行向上下交替填充，每行宽度受圆弦长约束。
/// 行高固定 + 行宽不超弦长 → 天然不重叠；放不下则整体缩小字号后重算。
/// ponytail: 字号按总面积缩放迭代 ≤10 次，标签量级小可接受。
private enum BallCloud {
    static let hGap: CGFloat = 14
    static let vGap: CGFloat = 8
    static let padding: CGFloat = 16
    static let minFont: CGFloat = 9

    /// 确定性伪随机（SplitMix64）：以标签名做种子，保证同一标签每次布局位置稳定不乱跳。
    private struct SeededRNG {
        private var state: UInt64
        init(_ seed: UInt64) { state = seed == 0 ? 0x9E3779B97F4A7C15 : seed }
        mutating func unit() -> Double {
            state &+= 0x9E3779B97F4A7C15
            var z = state
            z = (z ^ (z >> 30)) &* 0xBF58476D1CE4E5B9
            z = (z ^ (z >> 27)) &* 0x94D049BB133111EB
            z ^= z >> 31
            return Double(z >> 11) / Double(1 << 53)
        }
    }

    /// 按「标签名 + 盐」生成稳定的 0..<1 噪声。
    private static func noise(_ name: String, _ salt: UInt64) -> Double {
        var h: UInt64 = 0xcbf29ce484222325
        for b in name.utf8 { h = (h ^ UInt64(b)) &* 0x100000001B3 }
        var rng = SeededRNG(h &+ salt &* 0x9E3779B97F4A7C15)
        return rng.unit()
    }

    static func place(tags: [DocumentTag], in size: CGSize) -> [PlacedTag] {
        guard !tags.isEmpty else { return [] }
        let maxCount = tags.map(\.count).max() ?? 1
        let minCount = tags.map(\.count).min() ?? 1
        let cx = size.width / 2
        let cy = size.height / 2
        let maxRadius = max(40, min(size.width, size.height) / 2 - padding)

        func fits(_ radius: CGFloat, _ scale: CGFloat) -> Bool {
            !pack(tags, cx: cx, cy: cy, radius: radius, scale: scale, maxCount: maxCount, minCount: minCount).overflow
        }

        // 1) 缩字号，保证在面板最大半径内能全部装下
        var scale: CGFloat = 1
        for _ in 0..<10 {
            if fits(maxRadius, scale) { break }
            scale *= 0.88
        }

        // 2) 二分找恰好容纳全部标签的最小半径 → 完整的圆，随后随标签多少自动缩放，不再是面板中间的一条
        var lo: CGFloat = 1
        var hi: CGFloat = maxRadius
        if fits(hi, scale) {
            for _ in 0..<12 {
                let mid = (lo + hi) / 2
                if fits(mid, scale) { hi = mid } else { lo = mid }
            }
        }
        return pack(tags, cx: cx, cy: cy, radius: hi, scale: scale, maxCount: maxCount, minCount: minCount).placed
    }

    private struct PackResult {
        var placed: [PlacedTag]
        var overflow: Bool
    }

    private static func pack(
        _ tags: [DocumentTag], cx: CGFloat, cy: CGFloat, radius: CGFloat,
        scale: CGFloat, maxCount: Int, minCount: Int
    ) -> PackResult {
        let ratio: (Int) -> Double = { count in
            maxCount > minCount ? Double(count - minCount) / Double(maxCount - minCount) : 1
        }
        let font: (Int) -> CGFloat = { max(minFont, (13 + CGFloat(ratio($0).squareRoot()) * 17) * scale) }
        let weightOf: (Int) -> Font.Weight = { ratio($0) > 0.5 ? .semibold : .regular }

        var maxHeight: CGFloat = 0
        for tag in tags {
            maxHeight = max(maxHeight, measure(tag.name, font: font(tag.count), semibold: weightOf(tag.count) == .semibold).height)
        }
        let rowHeight = maxHeight + vGap

        var placed: [PlacedTag] = []
        var idx = 0
        var overflow = false
        var rowK = 0

        while idx < tags.count {
            let offsetIndex: Int
            if rowK == 0 { offsetIndex = 0 }
            else if rowK % 2 == 1 { offsetIndex = (rowK + 1) / 2 }
            else { offsetIndex = -(rowK / 2) }
            let y = CGFloat(offsetIndex) * rowHeight
            let chord = radius * radius - y * y
            if chord <= 0 { overflow = true; break }
            let maxRowWidth = 2 * chord.squareRoot()

            var row: [(tag: DocumentTag, size: CGSize, font: CGFloat, weight: Font.Weight, gap: CGFloat)] = []
            var rowWidth: CGFloat = 0
            while idx < tags.count {
                let tag = tags[idx]
                let fs = font(tag.count)
                let wt = weightOf(tag.count)
                let s = measure(tag.name, font: fs, semibold: wt == .semibold)
                let gap = hGap * CGFloat(0.6 + 0.8 * noise(tag.name, 1))
                if row.isEmpty {
                    if s.width > maxRowWidth { overflow = true; break }
                } else if rowWidth + gap + s.width > maxRowWidth {
                    break
                }
                rowWidth += gap + s.width
                row.append((tag, s, fs, wt, gap))
                idx += 1
            }
            if overflow { break }
            guard let first = row.first else { overflow = true; break }

            // 行内重排：最高频居中，其余依次向两侧展开
            var left: [(tag: DocumentTag, size: CGSize, font: CGFloat, weight: Font.Weight, gap: CGFloat)] = []
            var right: [(tag: DocumentTag, size: CGSize, font: CGFloat, weight: Font.Weight, gap: CGFloat)] = []
            for (k, item) in row.enumerated() where k > 0 {
                if k % 2 == 1 { right.append(item) } else { left.append(item) }
            }
            let display = Array(left.reversed()) + [first] + right

            // 行整体左右错位 + 词在行带内上下漂移 → 凌乱感（仍不重叠、高频不跑偏）
            // 中心行不动、错位量加上限，避免高频词被推出中心
            let slack = max(0, (maxRowWidth - rowWidth) / 2)
            let rowOffset = rowK == 0 ? 0 : min(slack, radius * 0.1) * CGFloat(2 * noise(first.tag.name, 3 + UInt64(rowK)) - 1)
            let leftPad = display[0].gap
            var x = cx + rowOffset - (rowWidth - leftPad) / 2
            for (j, item) in display.enumerated() {
                if j > 0 { x += item.gap }
                let jitter = (rowHeight - item.size.height) / 2 * 0.7
                    * CGFloat(2 * noise(item.tag.name, 2) - 1)
                let halfW = item.size.width / 2
                let halfH = item.size.height / 2
                let px = min(max(x + halfW, halfW), 2 * cx - halfW)
                let py = min(max(cy + y + jitter, halfH), 2 * cy - halfH)
                placed.append(PlacedTag(
                    tag: item.tag,
                    center: CGPoint(x: px, y: py),
                    fontSize: item.font,
                    weight: item.weight,
                    opacity: 0.5 + ratio(item.tag.count) * 0.5
                ))
                x += item.size.width
            }
            rowK += 1
        }
        return PackResult(placed: placed, overflow: overflow)
    }

    private static func measure(_ text: String, font: CGFloat, semibold: Bool) -> CGSize {
        let nsFont = NSFont.systemFont(ofSize: font, weight: semibold ? .semibold : .regular)
        let size = (text as NSString).size(withAttributes: [.font: nsFont])
        return CGSize(width: size.width.rounded(.up), height: size.height.rounded(.up))
    }
}
#endif

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
