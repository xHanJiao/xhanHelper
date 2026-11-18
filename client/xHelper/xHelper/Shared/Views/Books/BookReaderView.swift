#if os(macOS)
import SwiftUI
import WebKit

/// 书阅读页（`BrowsePanel.books` 的二级页）：常驻 chrome + 正文 + 目录/设置。
///
/// 正文是原书 XHTML，交给 `DocumentReaderView` 的同一份模板（BOOK 模式）渲染；
/// 划线与批注也复用那套选区/浮层逻辑，只是数据落本地。
struct BookReaderView: View {
    let book: BookMeta
    /// 顶栏组只在该书的 tab 在前台时出现（`visible` 闭包读它）；`isActive` 也跟着它变
    @ObservedObject var manager: RightSidebarManager

    @StateObject private var controller: BookReaderController
    @State private var settings: BookReadingSettings
    @State private var findQuery = ""
    @FocusState private var findFieldFocused: Bool
    /// 本 tab 是否前台：只有前台才挂快捷键（保活的其它书 tab 仍在层级里，快捷键不能重复注册）
    @State private var isActive = false
    /// 侧栏 tab（目录 / 书签）：不持久化，每次进来先看目录
    @State private var sidebarTab: SidebarTab = .toc
    /// 折叠了哪些目录条（key = `BookTOCItem.key`）：只记得本次会话
    @State private var collapsedTOC: Set<String> = []

    /// 目录/批注面板的展开态：跨书、跨启动记忆（design §9.3：默认收起）
    @AppStorage("xhan_book_toc_open") private var tocOpenStored = false
    @AppStorage("xhan_book_annot_open") private var annotOpenStored = false

    init(book: BookMeta, manager: RightSidebarManager) {
        let loaded = BookReadingSettings.load()
        self.book = book
        self.manager = manager
        _settings = State(initialValue: loaded)
        _controller = StateObject(wrappedValue: BookReaderController(book: book, settings: loaded))
    }

    var body: some View {
        ZStack(alignment: .topTrailing) {
            VStack(spacing: 0) {
                if controller.findOpen {
                    findBar
                }
                HStack(spacing: 0) {
                    if controller.hasChapterNavigation {
                        if controller.tocOpen {
                            sidebarPanel
                        } else {
                            collapsedStrip(icon: "chevron.right", help: "展开目录与书签") {
                                controller.tocOpen = true
                            }
                        }
                        Divider()
                    }
                    readingArea
                    Divider().opacity(0.4)
                    if controller.annotOpen {
                        BookAnnotationPanel(controller: controller) {
                            withAnimation { controller.annotOpen = false }
                        }
                    } else {
                        collapsedStrip(icon: "chevron.left", help: "展开划线与批注") {
                            withAnimation { controller.annotOpen = true }
                        }
                    }
                }
                .frame(maxHeight: .infinity)
            }

            // Aa 弹层：开关在顶栏，弹层仍贴内容区右上方（顶栏胶囊按钮挂不了原生 popover），不遮正文列
            if controller.settingsOpen {
                settingsPopover
                    .padding(.trailing, 12)
                    .padding(.top, 8)
            }
        }
        // 页头已上移顶栏，这里只留快捷键用的隐形按钮（只有前台 tab 才挂，见 `shortcutButtons`）
        .background { shortcutButtons }
        .onChange(of: settings) { _, new in
            new.save()
            controller.applyTypo(new)
        }
        .onChange(of: settings.mode) { _, _ in
            controller.applyReadingMode(settings)
        }
        .onChange(of: findQuery) { _, q in controller.find(q) }
        .onChange(of: controller.findOpen) { _, open in
            if open {
                findFieldFocused = true
            } else {
                findQuery = ""
                controller.clearFind()
            }
        }
        // 顶栏图标/置灰跟着阅读位置走（registry 只观察自己的 version，得主动 bump）
        .onChange(of: controller.sectionIndex) { _, _ in TopBarRegistry.shared.refresh() }
        .onChange(of: controller.bookmarkedHere) { _, _ in TopBarRegistry.shared.refresh() }
        .onChange(of: controller.tocOpen) { _, open in tocOpenStored = open }
        .onChange(of: controller.annotOpen) { _, open in annotOpenStored = open }
        .onChange(of: manager.activeTabId, initial: true) { _, active in
            let mine = active == tabID
            if mine != isActive { isActive = mine }
        }
        .onAppear {
            controller.tocOpen = tocOpenStored
            controller.annotOpen = annotOpenStored
            registerTopBarGroup()
        }
        .onDisappear {
            TopBarRegistry.shared.unregister(id: topBarGroupID)
            controller.leave()
        }
        // 失焦就收阅读区间、回来重开（设计 §八：区间只在「真的在看」时算）
        .onReceive(NotificationCenter.default.publisher(for: NSApplication.didResignActiveNotification)) { _ in
            controller.pauseReading()
        }
        .onReceive(NotificationCenter.default.publisher(for: NSApplication.didBecomeActiveNotification)) { _ in
            controller.resumeReading()
        }
    }

    // MARK: - 顶栏按钮组

    private var tabID: String { RightSidebarManager.bookTabID(book.id) }

    private var topBarGroupID: String { "book-actions-\(book.id)" }

    /// 阅读页动作注册进顶栏中心槽（priority 8，与文档/灵阅详情同档）；
    /// 组的 `visible` 判「右栏前台正停在这本书的 tab」——保活的其它书 tab 不会露按钮。
    /// 快捷键**不挂在这里**：降级时按钮会进 `⋯`，挂在按钮上的键会一起丢（见 `shortcutButtons`）。
    private func registerTopBarGroup() {
        let controller = self.controller
        let manager = self.manager
        let tabID = self.tabID
        TopBarRegistry.shared.register(TopBarGroup(
            id: topBarGroupID,
            slot: .center,
            priority: 8,
            buttons: [
                TopBarButton(id: "book-prev-section",
                             icon: { "chevron.left" },
                             enabled: { controller.canGoPreviousSection },
                             action: { controller.jumpSection(-1) },
                             help: { "上一节（⌥⌘← / ⌥⌘↑）" },
                             visible: { controller.hasChapterNavigation }),
                TopBarButton(id: "book-next-section",
                             icon: { "chevron.right" },
                             enabled: { controller.canGoNextSection },
                             action: { controller.jumpSection(1) },
                             help: { "下一节（⌥⌘→ / ⌥⌘↓）" },
                             visible: { controller.hasChapterNavigation }),
                TopBarButton(id: "book-toc",
                             icon: { "sidebar.leading" },
                             enabled: { true },
                             action: { withAnimation { controller.tocOpen.toggle() } },
                             help: { controller.tocOpen ? "收起目录与书签" : "展开目录与书签" },
                             visible: { controller.hasChapterNavigation }),
                TopBarButton(id: "book-bookmark",
                             icon: { controller.bookmarkedHere ? "bookmark.fill" : "bookmark" },
                             enabled: { true },
                             action: { controller.toggleBookmark() },
                             help: { controller.bookmarkedHere ? "取消书签（⌘D）" : "记住这一页（⌘D）" }),
                TopBarButton(id: "book-find",
                             icon: { "magnifyingglass" },
                             enabled: { true },
                             action: { controller.findOpen.toggle() },
                             help: { "在本节查找（⌘F）" }),
                TopBarButton(id: "book-annot",
                             icon: { "list.bullet.rectangle" },
                             enabled: { true },
                             action: { withAnimation { controller.annotOpen.toggle() } },
                             help: { controller.annotOpen ? "收起划线与批注（⌘⇧A）" : "展开划线与批注（⌘⇧A）" }),
                TopBarButton(id: "book-settings",
                             icon: { "gearshape" },
                             enabled: { true },
                             action: { controller.settingsOpen.toggle() },
                             help: { "阅读设置（字号、行距）" }),
            ],
            visible: { manager.isOpen && manager.activeTabId == tabID },
            autoOverflow: true
        ))
    }

    /// 阅读页快捷键：隐形按钮挂在内容区（登不进 `⋯`，也不会被降级吃掉），且只在本书 tab 前台时挂载。
    /// SwiftUI 一个 view 只能挂一个快捷键，⌥⌘↑ / ⌥⌘↓ 与 ⌥⌘← / ⌥⌘→ 等价，所以四个方向键都要各挂一次。
    @ViewBuilder
    private var shortcutButtons: some View {
        Group {
            if isActive {
                Button("") { controller.toggleBookmark() }
                    .keyboardShortcut("d", modifiers: .command)
                Button("") { controller.findOpen.toggle() }
                    .keyboardShortcut("f", modifiers: .command)
                Button("") { withAnimation { controller.annotOpen.toggle() } }
                    .keyboardShortcut("a", modifiers: [.command, .shift])
                Button("") { controller.jumpSection(-1) }
                    .keyboardShortcut(.leftArrow, modifiers: [.command, .option])
                    .disabled(!controller.canGoPreviousSection)
                Button("") { controller.jumpSection(-1) }
                    .keyboardShortcut(.upArrow, modifiers: [.command, .option])
                    .disabled(!controller.canGoPreviousSection)
                Button("") { controller.jumpSection(1) }
                    .keyboardShortcut(.rightArrow, modifiers: [.command, .option])
                    .disabled(!controller.canGoNextSection)
                Button("") { controller.jumpSection(1) }
                    .keyboardShortcut(.downArrow, modifiers: [.command, .option])
                    .disabled(!controller.canGoNextSection)
            }
        }
        .opacity(0)
        .accessibilityHidden(true)
    }

    // MARK: - 查找栏

    private var findBar: some View {
        HStack(spacing: 8) {
            Image(systemName: "magnifyingglass")
                .font(.system(size: 11))
                .foregroundColor(.secondary)
            TextField("在本节查找", text: $findQuery)
                .textFieldStyle(.plain)
                .font(.system(size: 12))
                .focused($findFieldFocused)
                .onAppear { findFieldFocused = true }
                .onSubmit { controller.findNext() }
                .onKeyPress(.escape, phases: .down) { _ in
                    controller.findOpen = false
                    return .handled
                }
            Text(matchLabel)
                .font(.system(size: 11))
                .monospacedDigit()
                .fixedSize()
            Button { controller.findPrevious() } label: {
                Image(systemName: "chevron.up").font(.system(size: 11))
            }
            .buttonStyle(.plain)
            .disabled(controller.matchCount == 0)
            .help("上一个匹配")
            Button { controller.findNext() } label: {
                Image(systemName: "chevron.down").font(.system(size: 11))
            }
            .buttonStyle(.plain)
            .disabled(controller.matchCount == 0)
            .help("下一个匹配")
            Button { controller.findOpen = false } label: {
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

    private var matchLabel: String {
        guard !findQuery.isEmpty else { return "" }
        guard controller.matchCount > 0 else { return "无匹配" }
        return "\(controller.matchIndex + 1)/\(controller.matchCount)"
    }

    // MARK: - 正文

    private var readingArea: some View {
        ZStack {
            // WKWebView 是 NSView，没有 intrinsic size：不给撑满的 frame，会被布局压成 0 宽，
            // 表现就是「右侧一片空白、也不转圈」。
            BookReaderWebView(controller: controller, settings: settings)
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            if controller.isLoadingChapter {
                ProgressView()
                    .controlSize(.small)
                    .transition(.opacity)
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        // 自动换节时内容一换就到了，转圈只该在「真的慢」时出现：
        // 250ms 内加载完就根本不显示（否则每节闪一下比手动点还烦）。
        .animation(.easeIn(duration: 0.12).delay(0.25), value: controller.isLoadingChapter)
    }

    // MARK: - 侧栏（目录 / 书签）

    private enum SidebarTab: String, CaseIterable {
        case toc = "目录", bookmarks = "书签"
    }

    private var sidebarPanel: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: 2) {
                ForEach(SidebarTab.allCases, id: \.self) { tab in
                    sidebarTabButton(tab)
                }
                Spacer(minLength: 4)
                Button { withAnimation { controller.tocOpen = false } } label: {
                    Image(systemName: "chevron.left").font(.system(size: 10, weight: .semibold))
                }
                .buttonStyle(.plain)
                .help("收起侧栏")
            }
            .padding(.horizontal, 8)
            .padding(.top, 7)

            Text(sidebarMeta)
                .font(.system(size: 10))
                .foregroundColor(.secondary)
                .padding(.horizontal, 12)
                .padding(.top, 2)
                .padding(.bottom, 4)

            if sidebarTab == .toc { tocList } else { bookmarkList }
        }
        .frame(width: 220)
        .background(Color.primary.opacity(0.02))
    }

    private func sidebarTabButton(_ tab: SidebarTab) -> some View {
        Button { sidebarTab = tab } label: {
            Text(tab.rawValue)
                .font(.system(size: 11, weight: sidebarTab == tab ? .semibold : .regular))
                .foregroundColor(sidebarTab == tab ? .primary : .secondary)
                .padding(.horizontal, 8)
                .padding(.vertical, 4)
                .background(sidebarTab == tab ? Color.primary.opacity(0.07) : Color.clear,
                            in: RoundedRectangle(cornerRadius: 5))
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
    }

    private var bookPercent: Int { Int((controller.bookProgress * 100).rounded()) }

    private var sidebarMeta: String {
        switch sidebarTab {
        case .toc:
            let count = controller.book.toc.isEmpty
                ? "\(controller.book.chapterCount) 章" : "\(controller.book.toc.count) 条"
            return "\(count) · 全书 \(bookPercent)%"
        case .bookmarks:
            if controller.bookmarksFailed { return "没能载入（本次不改动已有书签）" }
            return controller.bookmarks.isEmpty ? "⌘D 记住这一页" : "\(controller.bookmarks.count) 条"
        }
    }

    // MARK: - 目录

    private var tocList: some View {
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 1) {
                    ForEach(visibleTOCLines) { line in
                        tocRow(line).id(line.id)
                    }
                }
                .padding(.bottom, 8)
            }
            .onAppear {
                revealCurrentTOC()
                scrollTOC(proxy, animated: false)
            }
            // 读到哪就跟到哪：高亮挪出视口后目录自己滚（折叠态下目标不在列表里就自然不动）
            .onChange(of: controller.currentTOCKey) { _, _ in
                scrollTOC(proxy, animated: true)
            }
        }
    }

    private func tocRow(_ line: TOCLine) -> some View {
        let folded = collapsedTOC.contains(line.id)
        return HStack(spacing: 6) {
            if line.hasChildren {
                Button {
                    if folded { collapsedTOC.remove(line.id) } else { collapsedTOC.insert(line.id) }
                } label: {
                    Image(systemName: "chevron.right")
                        .font(.system(size: 8, weight: .semibold))
                        .foregroundColor(.secondary)
                        .rotationEffect(.degrees(folded ? 0 : 90))
                        .frame(width: 12, height: 12)
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .help(folded ? "展开子项" : "收起子项")
            } else {
                Color.clear.frame(width: 12, height: 12)
            }
            Text(line.item.title)
                .font(.system(size: 12, weight: line.isCurrent ? .semibold : .regular))
                .foregroundColor(line.isCurrent ? .primary : .secondary)
                .lineLimit(1)
            Spacer(minLength: 0)
        }
        .padding(.leading, CGFloat(line.item.depth) * 12 + 4)
        .padding(.trailing, 10)
        .padding(.vertical, 5)
        .background(
            line.isCurrent ? Color("appAccent").opacity(0.10) : Color.clear,
            in: RoundedRectangle(cornerRadius: 5)
        )
        .padding(.horizontal, 6)
        .contentShape(Rectangle())
        .onTapGesture { controller.goToTOC(line.item) }
    }

    /// 目录条目：TOC 为空（坏 NCX）时退化成「每章一行」，没有层级也就没有折叠。
    ///
    /// 行的 id 用「下标 + item.key」：作者把两个标题指到同一章同一 `#frag` 时 `key` 会撞，
    /// 撞了就是「折一条连另一条一起折 + `ForEach` 重复 id」——下标保证唯一。
    private var tocOutline: [TOCLine] {
        if controller.book.toc.isEmpty {
            return controller.book.chapters.map { chapter in
                let item = BookTOCItem(title: chapter.title, chapterIndex: chapter.index,
                                       depth: 0, fragment: nil)
                return TOCLine(id: "chapter-\(chapter.index)", item: item, parent: nil,
                               hasChildren: false, isCurrent: chapter.index == controller.chapterIndex)
            }
        }
        // 扁平目录带 depth：用栈还原父子（父 = 前面最近一个 depth 更小的条目）
        var rows: [TOCLine] = []
        var stack: [(depth: Int, id: String)] = []
        for (index, item) in controller.book.toc.enumerated() {
            while let last = stack.last, last.depth >= item.depth { stack.removeLast() }
            let id = "\(index)#\(item.key)"
            rows.append(TOCLine(id: id, item: item, parent: stack.last?.id,
                                hasChildren: false, isCurrent: false))
            stack.append((item.depth, id))
        }
        var withChildren = Set<String>()
        for row in rows { if let parent = row.parent { withChildren.insert(parent) } }
        return rows.map {
            TOCLine(id: $0.id, item: $0.item, parent: $0.parent,
                    hasChildren: withChildren.contains($0.id),
                    isCurrent: $0.item.key == controller.currentTOCKey)
        }
    }

    /// 折叠后实际显示的行：被折叠条的整棵子树按 depth 跳过
    private var visibleTOCLines: [TOCLine] {
        guard !collapsedTOC.isEmpty else { return tocOutline }
        var hiddenUnder: Int?
        var out: [TOCLine] = []
        for line in tocOutline {
            if let depth = hiddenUnder, line.item.depth > depth { continue }
            hiddenUnder = nil
            out.append(line)
            if collapsedTOC.contains(line.id), line.hasChildren { hiddenUnder = line.item.depth }
        }
        return out
    }

    private var currentTOCLineID: String? {
        guard let key = controller.currentTOCKey else { return nil }
        return tocOutline.first { $0.item.key == key }?.id
    }

    /// 打开侧栏时把当前条的所有祖先展开（否则「当前」被折叠在里面看不到）
    private func revealCurrentTOC() {
        let lines = tocOutline
        guard let target = lines.first(where: { $0.item.key == controller.currentTOCKey }) else { return }
        var byID: [String: TOCLine] = [:]
        for line in lines { byID[line.id] = line }
        var cursor = target.parent
        while let current = cursor {
            collapsedTOC.remove(current)
            cursor = byID[current]?.parent
        }
    }

    private func scrollTOC(_ proxy: ScrollViewProxy, animated: Bool) {
        guard let id = currentTOCLineID else { return }
        // 下一拍：刚展开/刚切 tab 时行还没布局，同拍 scrollTo 找不到目标
        DispatchQueue.main.async {
            if animated {
                withAnimation(.easeInOut(duration: 0.18)) { proxy.scrollTo(id, anchor: .center) }
            } else {
                proxy.scrollTo(id, anchor: .center)
            }
        }
    }

    private struct TOCLine: Identifiable {
        let id: String
        let item: BookTOCItem
        let parent: String?
        let hasChildren: Bool
        let isCurrent: Bool
    }

    // MARK: - 书签

    private var bookmarkList: some View {
        Group {
            if controller.bookmarks.isEmpty {
                VStack(spacing: 6) {
                    Image(systemName: controller.bookmarksFailed ? "exclamationmark.triangle" : "bookmark")
                        .font(.system(size: 20, weight: .light))
                        .foregroundColor(.secondary.opacity(0.5))
                    Text(controller.bookmarksFailed
                         ? "书签没能载入（本次不改动已有书签）"
                         : "读到想记住的地方，按 ⌘D")
                        .font(.system(size: 11))
                        .foregroundColor(.secondary)
                        .multilineTextAlignment(.center)
                        .padding(.horizontal, 12)
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                ScrollView {
                    LazyVStack(alignment: .leading, spacing: 1) {
                        ForEach(controller.bookmarks) { item in
                            bookmarkRow(item)
                        }
                    }
                    .padding(.horizontal, 6)
                    .padding(.bottom, 8)
                }
            }
        }
    }

    private func bookmarkRow(_ item: BookBookmark) -> some View {
        HStack(alignment: .top, spacing: 6) {
            VStack(alignment: .leading, spacing: 2) {
                Text(item.quote.isEmpty ? "（没有摘录）" : item.quote)
                    .font(.system(size: 11.5))
                    .foregroundColor(.primary)
                    .lineLimit(2)
                    .multilineTextAlignment(.leading)
                Text(bookmarkSubtitle(item))
                    .font(.system(size: 10))
                    .foregroundColor(.secondary)
                    .lineLimit(1)
            }
            Spacer(minLength: 0)
            Button { controller.removeBookmark(id: item.id) } label: {
                Image(systemName: "xmark")
                    .font(.system(size: 9, weight: .semibold))
                    .foregroundColor(.secondary)
                    .frame(width: 16, height: 16)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .help("删除这个书签")
        }
        .padding(.horizontal, 6)
        .padding(.vertical, 5)
        .contentShape(Rectangle())
        .onTapGesture { controller.goToBookmark(item) }
    }

    private func bookmarkSubtitle(_ item: BookBookmark) -> String {
        let title = controller.book.section(at: item.sectionIndex)?.title ?? ""
        return title.isEmpty ? "第 \(item.chapterIndex + 1) 章" : title
    }

    private func collapsedStrip(icon: String, help: String, action: @escaping () -> Void) -> some View {
        VStack {
            Spacer()
            Button(action: action) {
                Image(systemName: icon)
                    .font(.caption.weight(.semibold))
                    .foregroundColor(.secondary)
                    .frame(width: 28, height: 28)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .help(help)
            Spacer()
        }
        .frame(width: 32)
        .background(Color.primary.opacity(0.02))
    }

    // MARK: - Aa 弹层

    private var settingsPopover: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("阅读设置（全局生效）")
                .font(.system(size: 11, weight: .semibold))
                .foregroundColor(.secondary)

            settingsRow("翻页") {
                Picker("", selection: $settings.mode) {
                    ForEach(BookReadingSettings.ReadingMode.allCases, id: \.self) {
                        Text($0.rawValue).tag($0)
                    }
                }
                .pickerStyle(.segmented)
                .labelsHidden()
                .controlSize(.small)
                .frame(width: 150)
            }

            settingsRow("字号") {
                HStack(spacing: 2) {
                    stepButton("minus") { settings.fontSize = max(13, settings.fontSize - 1) }
                    Text("\(Int(settings.fontSize))")
                        .font(.system(size: 11.5))
                        .monospacedDigit()
                        .frame(width: 46)
                    stepButton("plus") { settings.fontSize = min(28, settings.fontSize + 1) }
                }
            }

            settingsRow("行距") {
                Picker("", selection: $settings.line) {
                    ForEach(BookReadingSettings.LinePreset.allCases, id: \.self) {
                        Text($0.rawValue).tag($0)
                    }
                }
                .pickerStyle(.segmented)
                .labelsHidden()
                .controlSize(.small)
                .frame(width: 150)
            }

            settingsRow("段距") {
                Picker("", selection: $settings.paragraph) {
                    ForEach(BookReadingSettings.ParagraphPreset.allCases, id: \.self) {
                        Text($0.rawValue).tag($0)
                    }
                }
                .pickerStyle(.segmented)
                .labelsHidden()
                .controlSize(.small)
                .frame(width: 150)
            }

            settingsRow("页宽") {
                Picker("", selection: $settings.width) {
                    ForEach(BookReadingSettings.PageWidth.allCases, id: \.self) {
                        Text($0.rawValue).tag($0)
                    }
                }
                .pickerStyle(.segmented)
                .labelsHidden()
                .controlSize(.small)
                .frame(width: 150)
            }

            settingsRow("字体") {
                Picker("", selection: $settings.font) {
                    ForEach(BookReadingSettings.FontChoice.allCases, id: \.self) {
                        Text($0.rawValue).tag($0)
                    }
                }
                .pickerStyle(.segmented)
                .labelsHidden()
                .controlSize(.small)
                .frame(width: 110)
            }
        }
        .padding(12)
        .frame(width: 308)
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 12, style: .continuous))
        .overlay {
            RoundedRectangle(cornerRadius: 12, style: .continuous)
                .strokeBorder(Color.primary.opacity(0.08), lineWidth: 0.5)
        }
    }

    private func settingsRow<Content: View>(_ label: String,
                                            @ViewBuilder content: () -> Content) -> some View {
        HStack(spacing: 8) {
            Text(label).font(.system(size: 12))
            Spacer(minLength: 8)
            content()
        }
    }

    private func stepButton(_ systemName: String, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Image(systemName: systemName)
                .font(.system(size: 10, weight: .semibold))
                .frame(width: 22, height: 20)
                .background(Color.primary.opacity(0.06), in: RoundedRectangle(cornerRadius: 5))
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
    }
}

// MARK: - WebView

private struct BookReaderWebView: NSViewRepresentable {
    @ObservedObject var controller: BookReaderController
    let settings: BookReadingSettings

    func makeCoordinator() -> Coordinator { Coordinator(controller: controller) }

    func makeNSView(context: Context) -> WKWebView {
        let config = WKWebViewConfiguration()
        config.userContentController.add(context.coordinator, name: "reader")
        let webView = WKWebView(frame: .zero, configuration: config)
        // 全透明：底色由外层卡片材质给（与文档阅读器同一约束）
        webView.setValue(false, forKey: "drawsBackground")
        webView.underPageBackgroundColor = .clear
        webView.navigationDelegate = context.coordinator
        controller.webView = webView

        // 页面必须落成**文件**再 `loadFileURL`：`loadHTMLString(baseURL:)` 只给「该目录」读权限，
        // 书里的图在 `content/OEBPS/…` 这种子目录，实测 350 张全被挡（CSP 不是原因）。
        // 页面放在 content/ 里、授权整个 content/ → 图全量可读。删除书时随目录一起清掉。
        let store = BookStore(root: LocalPaths.booksDir)
        let pageURL = store.contentDir(controller.book.id).appendingPathComponent(".xhan-reader.html")
        let html = ReaderHTML.buildBook(typography: settings.typography,
                                        language: controller.book.language)
        do {
            // 内容没变就不重写，省掉每次打开的写盘
            let existing = try? String(contentsOf: pageURL, encoding: .utf8)
            if existing != html {
                try html.write(to: pageURL, atomically: true, encoding: .utf8)
            }
            webView.loadFileURL(pageURL, allowingReadAccessTo: store.contentDir(controller.book.id))
        } catch {
            LogBuffer.shared.error("书阅读器：写阅读页失败 \(error)，退回不带头图的内联加载")
            webView.loadHTMLString(html, baseURL: store.contentDir(controller.book.id))
        }
        return webView
    }

    func updateNSView(_ webView: WKWebView, context: Context) {
        // 章节与排版都走 JS，不重建 WebView（重建会丢选区与滚动位置）
        //
        // 首次进层级时把正文设为第一响应者：键盘事件只有 WebView 有焦点才进得来。
        // 只用滚轮、不点正文的读者否则一直按不到键（真机踩过 —— 症状是「鼠标能滚、
        // 按键毫无反应」，日志里连 `导航 key` 都没有）。
        if !context.coordinator.didFocus, let window = webView.window {
            context.coordinator.didFocus = true
            window.makeFirstResponder(webView)
            LogBuffer.shared.info("书阅读器：正文设为第一响应者，键盘可用")
        }
    }

    static func dismantleNSView(_ webView: WKWebView, coordinator: Coordinator) {
        webView.configuration.userContentController.removeScriptMessageHandler(forName: "reader")
        webView.stopLoading()
    }

    final class Coordinator: NSObject, WKScriptMessageHandler, WKNavigationDelegate {
        let controller: BookReaderController
        /// 首次进层级抢过一次焦点就够了：之后用户点查找框/目录时不能把焦点抢回来
        var didFocus = false

        init(controller: BookReaderController) {
            self.controller = controller
        }

        func userContentController(_ controller: WKUserContentController, didReceive message: WKScriptMessage) {
            guard message.name == "reader", let body = message.body as? [String: Any] else { return }
            Task { @MainActor in self.controller.apply(message: body) }
        }

        /// 书里不让任何导航发生：内链在上游已剥掉 href，只放行初始 `loadHTMLString`。
        nonisolated func webView(_ webView: WKWebView, decidePolicyFor navigationAction: WKNavigationAction,
                                 decisionHandler: @escaping (WKNavigationActionPolicy) -> Void) {
            decisionHandler(navigationAction.navigationType == .other ? .allow : .cancel)
        }
    }
}
#endif
