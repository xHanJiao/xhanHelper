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
                    viewModel: viewModel,
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
            headerBadge: doc.isInternal ? TagBadge(text: "内部", color: .secondary) : nil,
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

/// 侧栏模式：目录（文档内导航）/ 列表（同筛选条件下的文档导航）。
/// 见 `docs/design/document-sidebar-modes/design.md`。
private enum DocSidebarMode: String {
    case toc
    case list
}

private struct DocumentFullView: View {
    let document: Document
    let apiClient: APIClient
    /// 侧栏「列表」模式的数据源：同一份筛选条件下的文档列表（见 docs/design/document-sidebar-modes）。
    @ObservedObject var viewModel: DocumentViewModel
    let onBack: () -> Void

    init(document: Document,
         apiClient: APIClient,
         viewModel: DocumentViewModel,
         onBack: @escaping () -> Void) {
        self.document = document
        self.apiClient = apiClient
        self.viewModel = viewModel
        self.onBack = onBack
        #if os(macOS)
        // 阅读器把手跟着文档走（外层 `.id(doc.id)` 保证换篇就重建）
        _reader = StateObject(wrappedValue: DocumentReaderController(documentID: document.id, api: apiClient))
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
                Button { toggleAnnotationPanel() } label: {
                    Image(systemName: "list.bullet.rectangle")
                        .font(.system(size: 12))
                        .foregroundColor(annotOpen ? Color("appAccent") : .secondary)
                }
                .buttonStyle(.plain)
                .help("划线与批注面板")
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
                // macOS：侧栏常驻（没标题也有「列表」可切，入口不能消失）；iOS 仍只在有标题时显示
                #if os(macOS)
                if tocExpanded {
                    tocSidebar
                } else {
                    tocCollapsedStrip
                }
                Divider()
                #else
                if !tocItems.isEmpty {
                    tocSidebar
                    Divider()
                }
                #endif

                #if os(macOS)
                // 正文整块交给 WebView（标题 + 标签 + 正文都在 HTML 里）。
                // 目录栏 / 返回 / 查找栏外壳留在 SwiftUI 侧。
                DocumentReaderView(
                    document: document,
                    controller: reader,
                    fontSize: readingFontSize
                )
                .frame(maxWidth: .infinity, maxHeight: .infinity)
                .onGeometryChange(for: CGRect.self) { $0.frame(in: .global) } action: { readerFrame = $0 }
                .onChange(of: findQuery) { _, query in reader.find(query) }
                // 正文被改写后划线的失活状态要重算：服务端为准，客户端不自己猜
                .onChange(of: document.content) { _, _ in reader.refreshAnnotations() }

                // 批注面板常驻：展开 320，空间不够/手动收起 → 窄条
                if annotExpanded {
                    Divider().opacity(0.3)
                    annotationPanel
                } else if !reader.annotations.isEmpty {
                    Divider().opacity(0.3)
                    annotationStrip
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
            // 量文档 tab 内容宽，供「先收目录、再收批注」的兜底收放判定
            .onGeometryChange(for: CGFloat.self) { $0.size.width } action: { docWidth = $0 }
            // 悬停卡片时的「卡片 → 划线」连线（覆盖正文与批注面板）
            .overlay { AnnotationConnectorLayer(start: connectorStart, end: connectorEnd) }
            // 面板因窗口变窄被硬地板自动收起时，卡片随面板消失、收不到 onHover(false)，
            // 这里补一刀清掉悬停态，避免残留一条指向旧位置的连线。
            .onChange(of: annotExpanded) { _, expanded in if !expanded { clearConnector() } }
        }
        #if os(macOS)
        // 正文点划线请求定位：面板若被收起，先自动展开（受窗宽硬地板限制时可能仍只成窄条）
        .onChange(of: reader.focusRequest) { _, req in
            guard req != nil, !annotOpen else { return }
            withAnimation { annotOpen = true }
        }
        // D11：文档页铺一层"纸"底（material）。正文 WKWebView 是全透明的，没有这层就会
        // 直接压在窗口底图（照片）上、影响可读性；底图只透到页面四周。
        .background(.regularMaterial)
        #endif
    }

    #if os(macOS)
    /// WebView 阅读器把手（在 `init` 里按文档构造）
    @StateObject private var reader: DocumentReaderController
    /// 正文字号跟随全局字号设置。scaledSize 是计算属性，靠 @Published scale 触发重算。
    @ObservedObject private var fontManager = FontSizeManager.shared

    /// 正文基准字号：与旧 MarkdownUI 正文一致（scaledSize × 1.18）
    private var readingFontSize: CGFloat { fontManager.scaledSize * 1.18 }
    #else
    /// iOS 仍走原来的 MarkdownUI 正文，目录跳转用 ScrollViewReader 的锚点
    @State private var scrollToAnchor: String = ""
    #endif

    /// 批注面板常驻/收起、目录收放——全局记忆（跨文档、跨启动）。
    /// 收放是简单兜底（c2）：手动优先，只在窗口窄到触发硬地板时强制收起。
    @AppStorage("xhan_doc_annot_open") private var annotOpen = true
    @AppStorage("xhan_doc_toc_collapsed") private var tocCollapsed = true
    /// 侧栏模式（目录 / 列表）。持久化：换篇会走外层 `.id(doc.id)` 重建，@State 留不住。
    @AppStorage("xhan_doc_sidebar_mode") private var sidebarMode: DocSidebarMode = .toc
    @State private var showFindBar = false
    /// 侧栏「文档列表」行鼠标悬停项（仅 macOS 用；hover 底色与顶栏会话下拉同源）
    #if os(macOS)
    @State private var hoveredDocID: String?
    #endif
    @State private var findQuery = ""
    @State private var currentMatch = 0
    /// 与 docBlocks 等长，逐块命中数（mermaid 块恒为 0）
    @State private var matchCounts: [Int] = []
    @FocusState private var findFieldFocused: Bool
    /// 文档 tab 内容宽（`onGeometryChange` 写入），用于收放兜底判定
    @State private var docWidth: CGFloat = 0
    /// 阅读器（WebView）在 `.global` 的 frame —— 连线把 DOM 视口坐标换算成 global 用
    @State private var readerFrame: CGRect = .zero
    /// 悬停中的批注卡片（画「卡片 → 划线」连线用）
    @State private var connectorID: String?
    @State private var connectorCardFrame: CGRect?

    /// 连线起点 = 卡片左缘中点（global）
    private var connectorStart: CGPoint? {
        #if os(macOS)
        guard connectorID != nil, let f = connectorCardFrame else { return nil }
        return CGPoint(x: f.minX, y: f.midY)
        #else
        return nil
        #endif
    }

    /// 连线终点 = 划线首行所在「正文列那一行」的竖缘中点 + 6px 外扩（DOM 视口坐标 +
    /// 阅读器原点 → global）；滚出视口则不画。`annotationRect` 的 x/w 是 #content 左右缘，
    /// 所以终点落在右侧留白里，不横穿该行后半段文字（+6px 进 padding，满行时尾端也不贴字）。
    /// 卡片在划线右侧取右缘、否则取左缘（当前面板恒在右 → 仍右缘）。
    private var connectorEnd: CGPoint? {
        #if os(macOS)
        guard connectorID != nil, readerFrame != .zero, let r = reader.connectorRect else { return nil }
        let g = CGRect(x: readerFrame.minX + r.minX, y: readerFrame.minY + r.minY,
                       width: r.width, height: r.height)
        guard g.maxY > readerFrame.minY, g.minY < readerFrame.maxY else { return nil }
        let cardX = connectorCardFrame?.minX ?? g.maxX
        let right = cardX >= g.midX
        return CGPoint(x: (right ? g.maxX + 6 : g.minX - 6), y: g.midY)
        #else
        return nil
        #endif
    }

    /// 清掉悬停连线（卡片移出/面板收起/窗口收窄均走这里）。
    private func clearConnector() {
        connectorID = nil
        connectorCardFrame = nil
        #if os(macOS)
        reader.endConnectorTracking()
        #endif
    }

    /// 有效可用宽：首次布局前按「很宽」算，避免开场闪一下收窄
    private var layoutWidth: CGFloat { docWidth <= 0 ? .greatestFiniteMagnitude : docWidth }
    /// 目录展开：手动优先，窗口窄过硬地板则强制收起（先收目录）
    private var tocExpanded: Bool { !tocCollapsed && layoutWidth >= Layout.docTocHardFloor }
    /// 批注面板展开：手动优先，窗口窄过硬地板则强制收起（后收批注）
    private var annotExpanded: Bool { annotOpen && layoutWidth >= Layout.docAnnotHardFloor }

    #if os(macOS)
    /// 常驻批注面板：数据/动作全部来自 reader，视图本身纯展示。
    private var annotationPanel: some View {
        AnnotationPanel(
            annotations: reader.annotations,
            replies: reader.repliesByAnnotation,
            expandedIDs: reader.expandedAnnotationIDs,
            loadingIDs: reader.loadingReplyIDs,
            busyIDs: reader.busyReplyIDs,
            focusRequest: reader.focusRequest,
            onToggle: { reader.toggleThread(id: $0) },
            onJump: { reader.scrollToAnnotation(id: $0) },
            onReply: { id, content in Task { await reader.reply(annotationId: id, content: content) } },
            onDelete: { reader.deleteAnnotation(id: $0) },
            onCollapse: { clearConnector(); withAnimation { annotOpen = false } },
            onCollapseAll: { reader.collapseAllThreads() },
            onHoverCard: { id, inside, frame in
                if inside {
                    connectorID = id
                    connectorCardFrame = frame
                    reader.beginConnectorTracking(id: id)
                } else if connectorID == id {
                    clearConnector()
                }
            }
        )
    }

    /// 收起的窄条：一个 chevron + 批注数，点开还原（空间仍不够时会留在窄条，即硬地板）。
    private var annotationStrip: some View {
        VStack(spacing: 0) {
            Spacer()
            Button { withAnimation { annotOpen = true } } label: {
                VStack(spacing: 3) {
                    Image(systemName: "chevron.left")
                        .font(.caption.weight(.semibold))
                    Text("\(reader.annotations.count)")
                        .font(.system(size: 9))
                }
                .foregroundColor(.secondary)
                .frame(width: 28, height: 48)
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .help("展开划线与批注")
            Spacer()
        }
        .frame(width: Layout.docPanelStripWidth)
    }
    #endif

    /// 节名：本文目录 / 文档列表（列表带总数，取 listTotal 以免翻页时数字抖动）。
    private func modeTitle(_ mode: DocSidebarMode) -> String {
        guard mode == .list else { return "本文目录" }
        guard let n = viewModel.listTotal else { return "文档列表" }
        return "文档列表 · \(n) 篇"
    }

    private var sidebarModeLabel: String { modeTitle(sidebarMode) }

    /// 展开态侧栏：头部（节名菜单 + 收起按钮）+ 体（本文目录 / 文档列表）。
    private var tocSidebar: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: 4) {
                #if os(macOS)
                // 单行菜单：不占宽度、没有按钮底框，当前节名直接写在脸上。
                // 用 Toggle 而不是 Picker —— 空 label 的 Picker 在 Menu 里会自己长出一个
                // 空菜单项（灰色空框），见 docs/design/document-sidebar-modes 的后续变更。
                Menu {
                    Toggle("本文目录", isOn: Binding(
                        get: { sidebarMode == .toc },
                        set: { if $0 { sidebarMode = .toc } }
                    ))
                    Toggle(modeTitle(.list), isOn: Binding(
                        get: { sidebarMode == .list },
                        set: { if $0 { sidebarMode = .list } }
                    ))
                } label: {
                    HStack(spacing: 3) {
                        Text(sidebarModeLabel)
                            .font(.caption.weight(.semibold))
                        Image(systemName: "chevron.down")
                            .font(.system(size: 8, weight: .semibold))
                    }
                    .foregroundColor(.secondary)
                }
                .menuStyle(.borderlessButton)
                .menuIndicator(.hidden)
                // label 不压缩、外层固定 220：label + 4pt 间隔 + 24pt 收起按钮 + 24pt 内边距
                // 必须塞进 220。当前量级 ≈165pt；`listTotal` 到万篇（5 位数）才会开始压。
                .fixedSize()
                #else
                Text("本文目录")
                    .font(.caption.weight(.semibold))
                    .foregroundColor(.secondary)
                #endif
                Spacer(minLength: 4)
                #if os(macOS)
                Button {
                    withAnimation { tocCollapsed = true }
                } label: {
                    Image(systemName: "chevron.left")
                        .font(.caption.weight(.semibold))
                        .foregroundColor(.secondary)
                        .frame(width: 24, height: 24)
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .help("收起侧栏")
                #endif
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 8)

            Divider().opacity(0.3)

            #if os(macOS)
            if sidebarMode == .list {
                documentNavList
            } else {
                tocOutline
            }
            #else
            tocOutline
            #endif
        }
        .frame(width: 220)
    }

    /// 目录体：本文档标题大纲；无标题时给空态提示（macOS 侧栏常驻，得有说法）。
    private var tocOutline: some View {
        ScrollView {
            if tocItems.isEmpty {
                Text("本文档没有标题")
                    .font(.caption)
                    .foregroundColor(.secondary)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(.horizontal, 16)
                    .padding(.vertical, 8)
            } else {
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
        }
    }

    #if os(macOS)
    /// 列表体：同一份筛选条件下的文档列表（`viewModel.documents`），点行换文档。
    /// 当前文档不在已加载页内时（聊天内链跳进来 / 翻在第 2 页往后）顶部钉一行标出——
    /// `shortcut:` 只钉不插回原位（列表按 updated_at 排，位置还得靠拉页），导航够用。
    private var documentNavList: some View {
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 2) {
                    if !documentInOpenList {
                        documentNavRow(document, isCurrent: true)
                        Divider().opacity(0.3).padding(.vertical, 2)
                    }
                    ForEach(viewModel.documents, id: \.id) { doc in
                        documentNavRow(doc, isCurrent: doc.id == document.id)
                            .id(doc.id)
                            .onAppear {
                                if doc.id == viewModel.documents.last?.id {
                                    Task { await viewModel.loadMore() }
                                }
                            }
                    }
                    if viewModel.documents.isEmpty {
                        Text("当前筛选下没有文档")
                            .font(.caption)
                            .foregroundColor(.secondary)
                            .padding(.horizontal, 16)
                            .padding(.vertical, 8)
                    }
                }
                .padding(.horizontal, 6)
                .padding(.vertical, 6)
            }
            .onAppear {
                if documentInOpenList { proxy.scrollTo(document.id, anchor: .center) }
            }
        }
    }

    /// 当前文档是否在已加载的列表里（决定要不要顶部钉行）。
    private var documentInOpenList: Bool {
        viewModel.documents.contains { $0.id == document.id }
    }

    /// 列表行：标题一行 + 「相对时间 · 标签」次信息行，hover / 当前项给圆角底色。
    /// 取法与顶栏会话下拉同源（`TopBarView.conversationMenuRow`）。
    private func documentNavRow(_ doc: Document, isCurrent: Bool) -> some View {
        let isHovered = hoveredDocID == doc.id
        return Button { viewModel.openDocument = doc } label: {
            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 4) {
                    Text(doc.title.isEmpty ? "（无标题）" : doc.title)
                        .font(.system(size: 12, weight: isCurrent ? .semibold : .regular))
                        .foregroundColor(.primary)
                        .lineLimit(1)
                    if isCurrent {
                        Image(systemName: "checkmark")
                            .font(.system(size: 10, weight: .bold))
                            .foregroundColor(Color("appAccent"))
                    }
                    Spacer(minLength: 0)
                }
                Text(documentNavSubtitle(doc))
                    .font(.system(size: 11))
                    .foregroundColor(.secondary)
                    .lineLimit(1)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .padding(.horizontal, 10)
        .padding(.vertical, 6)
        .background(
            isCurrent
                ? Color("appAccent").opacity(0.10)
                : (isHovered ? Color.primary.opacity(0.06) : .clear),
            in: RoundedRectangle(cornerRadius: 6)
        )
        .onHover { hoveredDocID = $0 ? doc.id : nil }
    }

    /// 行次信息：相对时间 + 前 3 个标签（都没有则省略该行内容）。
    private func documentNavSubtitle(_ doc: Document) -> String {
        var parts = [formatConversationDate(doc.updatedAt)]
        let tags = parseTags(doc.tags).prefix(3).map(\.text)
        if !tags.isEmpty { parts.append(tags.joined(separator: " ")) }
        return parts.joined(separator: " · ")
    }
    #endif

    /// 收起态窄条：保留展开入口，垂直居中一个 chevron.right。
    #if os(macOS)
    private var tocCollapsedStrip: some View {
        VStack(spacing: 0) {
            Spacer()
            Button {
                withAnimation { tocCollapsed = false }
            } label: {
                Image(systemName: "chevron.right")
                    .font(.caption.weight(.semibold))
                    .foregroundColor(.secondary)
                    .frame(width: 28, height: 28)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .help("展开侧栏")
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
        Markdown(text, imageBaseURL: URL(string: apiClient.baseURL))
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
    /// 顶栏按钮：开/合常驻批注面板（全局记忆。空间不够时仍会留在窄条）。
    private func toggleAnnotationPanel() {
        withAnimation { annotOpen.toggle() }
    }
    #endif

    private func closeFind() {
        showFindBar = false
        findQuery = ""
        currentMatch = 0
        matchCounts = []
    }
}
