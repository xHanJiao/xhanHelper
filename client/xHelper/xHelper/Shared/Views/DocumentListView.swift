import SwiftUI
import MarkdownUI
#if os(macOS)
import AppKit
#endif

struct DocumentListView: View {
    @ObservedObject var viewModel: DocumentViewModel
    /// 右键菜单里点了「删除」、等待二次确认的文档。
    @State private var pendingDelete: Document?

    var body: some View {
        Group {
            if let doc = viewModel.openDocument {
                DocumentFullView(
                    document: doc,
                    apiClient: viewModel.apiClient,
                    canGoPrevious: viewModel.canGoPrevious,
                    canGoNext: viewModel.canGoNext,
                    isLoadingNeighbor: viewModel.isLoadingMore,
                    onNavigate: { offset in Task { await viewModel.openNeighbor(offset) } },
                    onBack: { viewModel.openDocument = nil }
                )
                // 换篇重建：滚动位置 / 查找栏 / 目录展开态归零
                .id(doc.id)
            } else {
                #if os(macOS)
                if viewModel.mode == .cloud {
                    cloudContent
                } else if viewModel.mode == .trash {
                    trashContent
                } else {
                    listContent
                }
                #else
                listContent
                #endif
            }
        }
        .task { await viewModel.loadCurrentMode() }
        // 同步选中态到顶栏文档按钮组（显隐由 Coordinator visible 闭包决定）
        .onChange(of: viewModel.openDocument) { _, doc in
            if let doc {
                DocumentSelectionCoordinator.shared.select(doc)
            } else {
                DocumentSelectionCoordinator.shared.deselect()
            }
        }
        #if os(macOS)
        .confirmationDialog(
            "删除文档",
            isPresented: Binding(
                get: { pendingDelete != nil },
                set: { if !$0 { pendingDelete = nil } }
            ),
            titleVisibility: .visible
        ) {
            Button("删除", role: .destructive) {
                if let doc = pendingDelete { Task { await viewModel.delete(doc) } }
            }
            Button("取消", role: .cancel) { }
        } message: {
            Text(pendingDelete.map { "确定删除「\($0.title)」？可在回收站找回，7 天后自动清理。" } ?? "")
        }
        #endif
    }

    @ViewBuilder
    private var listContent: some View {
        VStack(spacing: 0) {
            if !viewModel.activeTags.isEmpty {
                activeTagBar
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
                documentCard(doc)
            }
        }
    }

    #if os(macOS)
    /// 回收站：已软删文档，卡片右上角「恢复」；点卡片本体不进详情（已删文档详情接口 404）。
    private var trashContent: some View {
        PageContainer(
            title: "回收站",
            icon: "trash",
            isLoading: viewModel.isTrashLoading,
            errorMessage: viewModel.errorMessage,
            isEmpty: viewModel.trashDocuments.isEmpty && !viewModel.isTrashLoading,
            emptyMessage: "回收站是空的",
            onRetry: { Task { await viewModel.loadTrash() } }
        ) {
            CardGridView(
                items: viewModel.trashDocuments,
                maxColumns: 1,
                minCardWidth: 300,
                onLoadMore: { await viewModel.loadMoreTrash() }
            ) { doc in
                trashCard(doc)
            }
        }
    }

    private func trashCard(_ doc: Document) -> some View {
        CardView(config: CardConfig(
            title: doc.title,
            subtitle: doc.content,
            tags: parseTags(doc.tags),
            footerText: "删除于 " + formatConversationDate(doc.updatedAt),
            style: .document,
            actions: [
                CardAction(label: "恢复", icon: "arrow.uturn.backward", role: .normal) {
                    Task { await viewModel.restore(doc) }
                }
            ]
        ))
    }
    #endif

    /// 文档卡片：多选态点击切换勾选（不再进详情），右上角勾选圈 + 选中描边。
    private func documentCard(_ doc: Document) -> some View {
        let selecting = viewModel.isSelectingDocuments
        let selected = viewModel.selectedDocumentIDs.contains(doc.id)
        return CardView(config: CardConfig(
            title: doc.title,
            subtitle: doc.content,
            tags: parseTags(doc.tags),
            footerText: formatConversationDate(doc.updatedAt),
            style: .document,
            onTap: {
                if selecting {
                    viewModel.toggleDocumentSelection(doc.id)
                } else {
                    viewModel.openDocument = doc
                }
            }
        ))
        .overlay(alignment: .topTrailing) {
            if selecting {
                Image(systemName: selected ? "checkmark.circle.fill" : "circle")
                    .font(.system(size: 18))
                    .foregroundColor(selected ? Color("appAccent") : .secondary.opacity(0.4))
                    .padding(10)
                    .allowsHitTesting(false)
            }
        }
        .overlay {
            if selected {
                RoundedRectangle(cornerRadius: 12)
                    .strokeBorder(Color("appAccent"), lineWidth: 2)
                    .allowsHitTesting(false)
            }
        }
        #if os(macOS)
        .contextMenu {
            Button {
                DocumentSelectionCoordinator.shared.download(doc)
            } label: {
                Label("下载", systemImage: "arrow.down.circle")
            }
            Button {
                DocumentSelectionCoordinator.shared.share(doc)
            } label: {
                Label("分享", systemImage: "square.and.arrow.up")
            }
            Divider()
            Button(role: .destructive) {
                pendingDelete = doc
            } label: {
                Label("删除", systemImage: "trash")
            }
        }
        #endif
    }

    /// 列表模式的标签筛选条：交集可能多标签，逐 chip 展示。
    private var activeTagBar: some View {
        HStack(spacing: 6) {
            Image(systemName: "tag.fill")
                .font(.system(size: 11))
            #if os(macOS)
            TagScrollRow {
                ForEach(viewModel.activeTags, id: \.self) { tag in
                    TagChip(
                        text: tag,
                        color: Color("appAccent"),
                        font: .system(size: 12, weight: .medium),
                        hPad: 7,
                        vPad: 1,
                        bgOpacity: 0.16
                    )
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            #else
            ForEach(viewModel.activeTags, id: \.self) { tag in
                Text(tag)
                    .font(.system(size: 12, weight: .medium))
                    .padding(.horizontal, 7)
                    .padding(.vertical, 1)
                    .background(Capsule().fill(Color("appAccent").opacity(0.16)))
            }
            Spacer()
            #endif
            Button {
                Task { await viewModel.clearFilter() }
            } label: {
                Image(systemName: "xmark.circle.fill")
                    .font(.system(size: 12))
            }
            .buttonStyle(.plain)
            .help("清除标签筛选")
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
        let neighbors = viewModel.neighbors(of: viewModel.selectedTags)
        PageContainer(
            title: "标签词云",
            icon: "tag",
            isLoading: viewModel.isLoading,
            errorMessage: viewModel.errorMessage,
            isEmpty: visible.isEmpty && !viewModel.isLoading,
            emptyMessage: viewModel.tags.isEmpty ? "还没有标签" : "没有达到 \(viewModel.minTagCount) 篇的标签",
            onRetry: { Task { await viewModel.load() } }
        ) {
            ZStack(alignment: .bottom) {
                TagCloudView(
                    tags: visible,
                    query: viewModel.cloudQuery,
                    selected: viewModel.selectedTags,
                    neighbors: neighbors,
                    onTap: { tag in Task { await viewModel.selectTag(tag) } },
                    onCmdTap: { tag in viewModel.toggleSelect(tag) },
                    helpLabel: "篇文档"
                )
                VStack(spacing: 6) {
                    if !viewModel.selectedTags.isEmpty {
                        selectionBar
                    }
                    HStack {
                        Spacer()
                        cloudHint
                    }
                }
                .padding(12)
            }
        }
    }

    /// 多选动作条：已选标签 + 交集篇数入口（点进列表模式）。
    private var selectionBar: some View {
        HStack(spacing: 8) {
            ScrollView(.horizontal, showsIndicators: false) {
                HStack(spacing: 5) {
                    ForEach(viewModel.selectedTags.sorted(), id: \.self) { tag in
                        TagChip(
                            text: tag,
                            color: Color("appAccent"),
                            font: .system(size: 11),
                            hPad: 7,
                            vPad: 2,
                            bgOpacity: 0.14
                        )
                    }
                }
            }
            Button {
                Task { await viewModel.filterBySelection() }
            } label: {
                Text(viewModel.resultCount.map { "查看 \($0) 篇" } ?? "查看结果")
                    .font(.system(size: 12, weight: .semibold))
                    .padding(.horizontal, 4)
            }
            .buttonStyle(.borderedProminent)
            .tint(Color("appAccent"))
            .controlSize(.small)
            .fixedSize()
        }
        .padding(.horizontal, 10)
        .padding(.vertical, 7)
        .background(.ultraThinMaterial, in: RoundedRectangle(cornerRadius: 10))
        .overlay(
            RoundedRectangle(cornerRadius: 10)
                .strokeBorder(Color.primary.opacity(0.08), lineWidth: 0.5)
        )
    }

    /// 右下角操作提示（不参与命中测试）。
    private var cloudHint: some View {
        HStack(spacing: 5) {
            KeycapView(label: "⌘")
            Text("+ 点击 = 多选标签")
            Text("·").opacity(0.4)
            Text("点击 = 筛选")
        }
        .font(.system(size: 10))
        .foregroundColor(.secondary)
        .allowsHitTesting(false)
    }
    #endif

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

/// 详情页正文的可定位渲染单元：每块独立渲染并挂 `.id`，供查找跳转 `scrollTo`。
private enum DocBlock: Identifiable {
    case markdown(id: Int, text: String)
    case mermaid(id: Int, code: String)

    var id: Int {
        switch self {
        case .markdown(let id, _), .mermaid(let id, _): return id
        }
    }
}

/// 按顶层块（空行分隔）切分 markdown，围栏代码块内的空行不切。
/// ponytail: 空行分隔的列表会被拆开（有序列表可能重新编号），真被咬到再换正规 block parser。
private func splitMarkdownBlocks(_ text: String) -> [String] {
    var blocks: [String] = []
    var current: [String] = []
    var fence: String?
    func flush() {
        let joined = current.joined(separator: "\n")
        if !joined.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty { blocks.append(joined) }
        current.removeAll()
    }
    for line in text.components(separatedBy: "\n") {
        let trimmed = line.trimmingCharacters(in: .whitespaces)
        if let open = fence {
            current.append(line)
            if trimmed.hasPrefix(open) { fence = nil }
        } else if trimmed.hasPrefix("```") || trimmed.hasPrefix("~~~") {
            fence = String(trimmed.prefix(3))
            current.append(line)
        } else if trimmed.isEmpty {
            flush()
        } else {
            current.append(line)
        }
    }
    flush()
    return blocks
}

private func attributedMarkdown(_ text: String) -> AttributedString {
    (try? AttributedString(
        markdown: text,
        options: .init(interpretedSyntax: .full, failurePolicy: .returnPartiallyParsedIfPossible)
    )) ?? AttributedString(text)
}

private func matchRanges(in text: AttributedString, query: String) -> [Range<AttributedString.Index>] {
    guard !query.isEmpty else { return [] }
    var ranges: [Range<AttributedString.Index>] = []
    var start = text.startIndex
    while start < text.endIndex,
          let r = text[start...].range(of: query, options: .caseInsensitive) {
        ranges.append(r)
        start = r.upperBound
    }
    return ranges
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
    let apiClient: APIClient
    let canGoPrevious: Bool
    let canGoNext: Bool
    let isLoadingNeighbor: Bool
    /// offset：-1 上一篇 / +1 下一篇
    let onNavigate: (Int) -> Void
    let onBack: () -> Void

    init(document: Document,
         apiClient: APIClient,
         canGoPrevious: Bool,
         canGoNext: Bool,
         isLoadingNeighbor: Bool,
         onNavigate: @escaping (Int) -> Void,
         onBack: @escaping () -> Void) {
        self.document = document
        self.apiClient = apiClient
        self.canGoPrevious = canGoPrevious
        self.canGoNext = canGoNext
        self.isLoadingNeighbor = isLoadingNeighbor
        self.onNavigate = onNavigate
        self.onBack = onBack
        #if os(macOS)
        // 阅读器把手跟着文档走（外层 `.id(doc.id)` 保证换篇就重建）
        _reader = StateObject(wrappedValue: DocumentReaderController(documentID: document.id, title: document.title, api: apiClient))
        #endif
    }

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

                #if os(macOS)
                Button { toggleAnnotationDrawer() } label: {
                    Image(systemName: "list.bullet.rectangle")
                        .font(.system(size: 12))
                        .foregroundColor(showAnnotationDrawer ? Color("appAccent") : .secondary)
                }
                .buttonStyle(.plain)
                .help("划线与批注列表")
                #endif

                Button { toggleFind() } label: {
                    Image(systemName: "magnifyingglass")
                        .font(.system(size: 12))
                        .foregroundColor(showFindBar ? Color("appAccent") : .secondary)
                }
                .buttonStyle(.plain)
                .help("在文档中查找（⌘F）")
                .keyboardShortcut("f", modifiers: .command)
            }
            .padding(.horizontal, 16)
            .padding(.vertical, 10)

            if showFindBar {
                findBar
            }

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

                #if os(macOS)
                // 正文整块交给 WebView（标题 + 标签 + 正文都在 HTML 里）。
                // 目录栏 / 返回 / 查找栏外壳留在 SwiftUI 侧。
                DocumentReaderView(
                    document: document,
                    controller: reader,
                    fontSize: readingFontSize
                )
                .frame(maxWidth: .infinity, maxHeight: .infinity)
                .onChange(of: findQuery) { _, query in reader.find(query) }
                // 正文被改写后划线的失活状态要重算：服务端为准，客户端不自己猜
                .onChange(of: document.content) { _, _ in reader.refreshAnnotations() }
                // 「问 AI」回程用（存为批注 → 预填浮层 / 点胶囊 → 跳回这段）：
                // 只在文档详情页在屏时注册，离开时注销（换篇会先出现新页 onAppear）
                .onAppear { DocumentAIBridge.shared.reader = reader }
                .onDisappear {
                    if DocumentAIBridge.shared.reader === reader { DocumentAIBridge.shared.reader = nil }
                }

                if showAnnotationDrawer {
                    Divider().opacity(0.3)
                    AnnotationDrawer(reader: reader) {
                        withAnimation { showAnnotationDrawer = false }
                    }
                }
                #else
                ScrollViewReader { proxy in
                    ScrollView {
                        VStack(alignment: .leading, spacing: 14) {
                            Text(document.title)
                                .font(.system(size: FontSizeManager.shared.scaledSize * 1.65, weight: .semibold))
                                .macosTextSelection()

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

                            if findQuery.isEmpty {
                                readingContent
                            } else {
                                // 查找态按块渲染：逐块 .id 供 scrollTo 定位
                                VStack(alignment: .leading, spacing: 14) {
                                    ForEach(Array(docBlocks.enumerated()), id: \.element.id) { pair in
                                        blockView(index: pair.offset, block: pair.element)
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
                    .onChange(of: findQuery) { _, _ in
                        recomputeMatches()
                        currentMatch = 0
                        // 首次输入时视图刚从单 Markdown 切成带 id 的块，延后一帧再滚
                        DispatchQueue.main.async { scrollToCurrent(proxy) }
                    }
                    .onChange(of: currentMatch) { _, _ in
                        scrollToCurrent(proxy)
                    }
                    .onChange(of: document.content) { _, _ in
                        recomputeMatches()
                    }
                }
                #endif
            }
        }
        #if os(macOS)
        .background { shortcutCarrier }
        #endif
    }

    #if os(macOS)
    /// WebView 阅读器把手（在 `init` 里按文档构造）
    @StateObject private var reader: DocumentReaderController
    /// 正文字号跟随全局字号设置。scaledSize 是计算属性，靠 @Published scale 触发重算。
    @ObservedObject private var fontManager = FontSizeManager.shared
    /// 聊天输入框焦点（避免 ⌘←/⌘→ 被文档上/下篇快捷键劫持，见 shortcutCarrier）
    @ObservedObject private var chatInputFocus = ChatInputFocus.shared

    /// 正文基准字号：与旧 MarkdownUI 正文一致（scaledSize × 1.18）
    private var readingFontSize: CGFloat { fontManager.scaledSize * 1.18 }
    #else
    /// iOS 仍走原来的 MarkdownUI 正文，目录跳转用 ScrollViewReader 的锚点
    @State private var scrollToAnchor: String = ""
    #endif

    @State private var isTocCollapsed = false
    @State private var showAnnotationDrawer = false
    @State private var showFindBar = false
    @State private var findQuery = ""
    @State private var currentMatch = 0
    /// 与 docBlocks 等长，逐块命中数（mermaid 块恒为 0）
    @State private var matchCounts: [Int] = []
    @FocusState private var findFieldFocused: Bool

    /// 展开态目录栏：标题行（右侧收起按钮）+ 标题大纲 + 页脚方向行。
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
                    ForEach(Array(tocItems.enumerated()), id: \.element.id) { pair in
                        Button {
                            #if os(macOS)
                            reader.scrollToHeading(
                                level: pair.element.level,
                                text: pair.element.text,
                                occurrence: headingOccurrence(at: pair.offset)
                            )
                            #else
                            withAnimation { scrollToAnchor = pair.element.anchor }
                            #endif
                        } label: {
                            Text(pair.element.text)
                                .font(pair.element.level <= 2 ? .caption : .caption2)
                                .foregroundColor(pair.element.level == 1 ? .primary : .secondary)
                                .lineLimit(2)
                                .frame(maxWidth: .infinity, alignment: .leading)
                        }
                        .buttonStyle(.plain)
                        .padding(.horizontal, 16)
                        .padding(.vertical, 3)
                        .padding(.leading, CGFloat(pair.element.level - 1) * 12)
                    }
                }
                .padding(.vertical, 8)
            }

            #if os(macOS)
            Divider().opacity(0.3)
            HStack(spacing: 14) {
                TOCNavRow(
                    title: "上一篇", icon: "chevron.left", leadsIcon: true,
                    enabled: canGoPrevious && !isLoadingNeighbor, isLoading: false
                ) { onNavigate(-1) }
                TOCNavRow(
                    title: "下一篇", icon: "chevron.right", leadsIcon: false,
                    enabled: canGoNext && !isLoadingNeighbor, isLoading: isLoadingNeighbor
                ) { onNavigate(1) }
            }
            .frame(maxWidth: .infinity)
            .padding(.vertical, 6)
            #endif
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

    /// ⌘←/⌘→ 快捷键载体：目录栏只在有标题时渲染，快捷键不依赖它，故挂在最外层。
    /// 尺寸归零 + 透明，只提供键位；查找栏打开、或聊天输入框聚焦时禁用，
    /// 把 ⌘←/⌘→ 还给文本框移动光标（否则输入时会被这两个键劫持）。
    private var shortcutCarrier: some View {
        Group {
            Button("") { onNavigate(-1) }
                .keyboardShortcut(.leftArrow, modifiers: .command)
                .disabled(!canGoPrevious || showFindBar || chatInputFocus.isFocused)
            Button("") { onNavigate(1) }
                .keyboardShortcut(.rightArrow, modifiers: .command)
                .disabled(!canGoNext || showFindBar || chatInputFocus.isFocused)
        }
        .frame(width: 0, height: 0)
        .opacity(0)
    }
    #endif

    private func parseTags(_ tags: String?) -> [TagBadge] {
        guard let tags else { return [] }
        return tags.split(separator: ",").map {
            TagBadge(text: String($0).trimmingCharacters(in: .whitespaces), color: Color("appAccent"))
        }
    }

    /// 第 `index` 个目录项在「同级同文字」里是第几个。DOM 侧标题只能按文字找，
    /// 靠这个序号区分同名标题（否则重复的「小结」永远跳到第一个）。
    private func headingOccurrence(at index: Int) -> Int {
        let item = tocItems[index]
        return tocItems[..<index].filter { $0.level == item.level && $0.text == item.text }.count
    }

    // MARK: - 文档内查找

    private var docBlocks: [DocBlock] {
        var result: [DocBlock] = []
        var idx = 0
        for segment in MermaidUtility.splitContent(document.content) {
            switch segment.type {
            case .mermaid:
                result.append(.mermaid(id: idx, code: segment.content))
                idx += 1
            case .markdown:
                for block in splitMarkdownBlocks(segment.content) {
                    result.append(.markdown(id: idx, text: block))
                    idx += 1
                }
            }
        }
        return result
    }

    private var totalMatches: Int {
        #if os(macOS)
        // macOS 的命中所属权在 JS（CSS Custom Highlight API 不改 DOM），Swift 只收计数
        return reader.matchCount
        #else
        return matchCounts.reduce(0, +)
        #endif
    }

    private var matchLabel: String {
        if findQuery.isEmpty { return "" }
        #if os(macOS)
        let index = reader.matchIndex
        return (totalMatches == 0 || index < 0) ? "无匹配" : "\(index + 1)/\(totalMatches)"
        #else
        return totalMatches == 0 ? "无匹配" : "\(currentMatch + 1)/\(totalMatches)"
        #endif
    }

    private var findBar: some View {
        HStack(spacing: 8) {
            Image(systemName: "magnifyingglass")
                .font(.system(size: 11))
                .foregroundColor(.secondary)
            TextField("在文档中查找", text: $findQuery)
                .textFieldStyle(.plain)
                .font(.system(size: 12))
                .focused($findFieldFocused)
                .onAppear { findFieldFocused = true }
                .onSubmit { gotoMatch(1) }
                .onKeyPress(.escape, phases: .down) { _ in
                    closeFind()
                    return .handled
                }
            Text(matchLabel)
                .font(.system(size: 11))
                .monospacedDigit()
                .fixedSize()
            Button { gotoMatch(-1) } label: {
                Image(systemName: "chevron.up").font(.system(size: 11))
            }
            .buttonStyle(.plain)
            .disabled(totalMatches == 0)
            .help("上一个匹配")
            Button { gotoMatch(1) } label: {
                Image(systemName: "chevron.down").font(.system(size: 11))
            }
            .buttonStyle(.plain)
            .disabled(totalMatches == 0)
            .help("下一个匹配")
            Button { closeFind() } label: {
                Image(systemName: "xmark").font(.system(size: 11))
            }
            .buttonStyle(.plain)
            .help("关闭查找")
        }
        .foregroundColor(.secondary)
        .padding(.horizontal, 16)
        .padding(.vertical, 8)
        .background(Color("surfaceInput").opacity(0.5))
    }

    @ViewBuilder
    private func blockView(index: Int, block: DocBlock) -> some View {
        switch block {
        case .mermaid(let id, let code):
            MermaidDiagramInlineView(code: code).id(id)
        case .markdown(let id, let text):
            if findQuery.isEmpty || matchRanges(in: attributedMarkdown(text), query: findQuery).isEmpty {
                markdownView(text).id(id)
            } else {
                Text(highlightedBlock(text, index: index))
                    .font(.system(size: FontSizeManager.shared.scaledSize * 1.18))
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .macosTextSelection()
                    .id(id)
            }
        }
    }

    /// 命中块渲染：AttributedString 上给命中子串加底色，当前项加深。
    private func highlightedBlock(_ text: String, index: Int) -> AttributedString {
        var attr = attributedMarkdown(text)
        let offset = index < matchCounts.count ? matchCounts.prefix(index).reduce(0, +) : 0
        for (k, r) in matchRanges(in: attr, query: findQuery).enumerated() {
            attr[r].backgroundColor = offset + k == currentMatch
                ? Color.orange.opacity(0.55)
                : Color.yellow.opacity(0.4)
        }
        return attr
    }

    private func markdownView(_ text: String) -> some View {
        Markdown(text)
            .markdownTheme(.chatBubble(fontSize: FontSizeManager.shared.scaledSize * 1.18))
            .markdownCodeSyntaxHighlighter(.plainText)
            .macosTextSelection()
    }

    /// 阅读态：整段交给一个 Markdown 视图，块间距/列表编号由 MarkdownUI 主题统一处理。
    @ViewBuilder
    private var readingContent: some View {
        let segments = MermaidUtility.splitContent(document.content)
        if segments.isEmpty {
            markdownView(document.content)
        } else {
            VStack(alignment: .leading, spacing: 0) {
                ForEach(segments) { segment in
                    switch segment.type {
                    case .markdown:
                        markdownView(segment.content)
                    case .mermaid:
                        MermaidDiagramInlineView(code: segment.content)
                    }
                }
            }
        }
    }

    private func recomputeMatches() {
        guard !findQuery.isEmpty else { matchCounts = []; return }
        matchCounts = docBlocks.map { block in
            guard case .markdown(_, let text) = block else { return 0 }
            return matchRanges(in: attributedMarkdown(text), query: findQuery).count
        }
    }

    private func blockID(forMatch n: Int) -> Int? {
        var remaining = n
        for (i, count) in matchCounts.enumerated() where count > 0 {
            if remaining < count { return docBlocks[i].id }
            remaining -= count
        }
        return nil
    }

    private func scrollToCurrent(_ proxy: ScrollViewProxy) {
        guard totalMatches > 0, let id = blockID(forMatch: currentMatch) else { return }
        withAnimation { proxy.scrollTo(id, anchor: .top) }
    }

    private func gotoMatch(_ delta: Int) {
        #if os(macOS)
        if delta > 0 { reader.findNext() } else { reader.findPrev() }
        #else
        guard totalMatches > 0 else { return }
        currentMatch = (currentMatch + delta + totalMatches) % totalMatches
        #endif
    }

    private func toggleFind() {
        if showFindBar { closeFind() } else { showFindBar = true }
    }

    #if os(macOS)
    /// 抽屉是第三列，会占正文宽度 —— 开的时候顺手收起目录栏（用户还能手动再展开）。
    private func toggleAnnotationDrawer() {
        withAnimation {
            showAnnotationDrawer.toggle()
            if showAnnotationDrawer { isTocCollapsed = true }
        }
    }
    #endif

    private func closeFind() {
        showFindBar = false
        findQuery = ""
        currentMatch = 0
        matchCounts = []
    }
}

#if os(macOS)
/// 目录栏页脚的方向行：平时纯文字（与目录条目同族），hover 只把文字由 secondary 提到 primary，
/// 不加底块/阴影/位移。两条居中排列，间距由调用方给定。
private struct TOCNavRow: View {
    let title: String
    let icon: String
    /// 图标在文字外侧（上一篇在左、下一篇在右）。
    let leadsIcon: Bool
    let enabled: Bool
    let isLoading: Bool
    let action: () -> Void

    @State private var isHovered = false

    var body: some View {
        Button(action: action) {
            HStack(spacing: 4) {
                if leadsIcon { iconView }
                Text(title).font(.caption)
                if !leadsIcon { iconView }
            }
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .foregroundColor(isHovered && enabled ? .primary : .secondary)
        .opacity(enabled ? 1 : 0.35)
        .onHover { isHovered = $0 }
        .disabled(!enabled)
        .help("\(title)（⌘\(leadsIcon ? "←" : "→")）")
    }

    @ViewBuilder
    private var iconView: some View {
        if isLoading {
            ProgressView().controlSize(.mini)
        } else {
            Image(systemName: icon).font(.caption2.weight(.semibold))
        }
    }
}
#endif
