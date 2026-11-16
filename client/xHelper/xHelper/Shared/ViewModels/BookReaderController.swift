#if os(macOS)
import AppKit
import Combine
import Foundation
import SwiftUI
import WebKit

/// 书的阅读设置（全局生效，不按书记忆 —— 设计 E8）。
nonisolated struct BookReadingSettings: Equatable {
    enum ReadingMode: String, CaseIterable {
        case scroll = "上下滚动"
        case paged = "左右翻页"
    }

    enum LinePreset: String, CaseIterable {
        case compact = "紧凑", standard = "标准", loose = "宽松"
        var value: Double {
            switch self {
            case .compact: return 1.55
            case .standard: return 1.78
            case .loose: return 2.0
            }
        }
    }

    enum ParagraphPreset: String, CaseIterable {
        case compact = "紧凑", standard = "标准", loose = "宽松"
        var value: Double {
            switch self {
            case .compact: return 0.28
            case .standard: return 0.55
            case .loose: return 0.95
            }
        }
    }

    enum PageWidth: String, CaseIterable {
        case narrow = "窄", standard = "标准", wide = "宽"
        var value: Double {
            switch self {
            case .narrow: return 640
            case .standard: return 728
            case .wide: return 860
            }
        }
    }

    enum FontChoice: String, CaseIterable {
        case system = "系统", serif = "衬线"
        var css: String {
            switch self {
            case .system: return #"-apple-system, "PingFang SC", "Helvetica Neue", sans-serif"#
            case .serif: return #""Songti SC", "Times New Roman", serif"#
            }
        }
    }

    var fontSize: Double = 19
    var line: LinePreset = .standard
    var paragraph: ParagraphPreset = .standard
    var width: PageWidth = .standard
    var font: FontChoice = .system
    var mode: ReadingMode = .scroll

    var typography: ReaderHTML.BookTypography {
        ReaderHTML.BookTypography(
            fontSize: fontSize,
            lineHeight: line.value,
            paragraphGap: paragraph.value,
            measure: width.value,
            fontFamily: font.css,
            readingMode: mode == .paged ? "paged" : "scroll"
        )
    }

    // 全局一份：改一次所有书都跟着变
    private static let keyPrefix = "xhan_book_"

    static func load() -> BookReadingSettings {
        let d = UserDefaults.standard
        var s = BookReadingSettings()
        if let v = d.object(forKey: keyPrefix + "fontSize") as? Double { s.fontSize = v }
        if let raw = d.string(forKey: keyPrefix + "line"), let v = LinePreset(rawValue: raw) { s.line = v }
        if let raw = d.string(forKey: keyPrefix + "paragraph"), let v = ParagraphPreset(rawValue: raw) { s.paragraph = v }
        if let raw = d.string(forKey: keyPrefix + "width"), let v = PageWidth(rawValue: raw) { s.width = v }
        if let raw = d.string(forKey: keyPrefix + "font"), let v = FontChoice(rawValue: raw) { s.font = v }
        if let raw = d.string(forKey: keyPrefix + "mode"), let v = ReadingMode(rawValue: raw) { s.mode = v }
        return s
    }

    func save() {
        let d = UserDefaults.standard
        d.set(fontSize, forKey: Self.keyPrefix + "fontSize")
        d.set(line.rawValue, forKey: Self.keyPrefix + "line")
        d.set(paragraph.rawValue, forKey: Self.keyPrefix + "paragraph")
        d.set(width.rawValue, forKey: Self.keyPrefix + "width")
        d.set(font.rawValue, forKey: Self.keyPrefix + "font")
        d.set(mode.rawValue, forKey: Self.keyPrefix + "mode")
    }
}

/// 书阅读状态：当前章、进度、本地批注。WebView 只渲染，状态都在这里。
///
/// 进度口径 = 当前章扁平文本的 UTF-16 偏移（JS `String.length`），字号/栏宽/翻页方式变化都不影响。
@MainActor
final class BookReaderController: ObservableObject {

    @Published private(set) var book: BookMeta
    private let store: BookStore

    /// 当前阅读单元（`book.effectiveSections` 下标）
    @Published private(set) var sectionIndex: Int
    @Published private(set) var bookProgress: Double
    /// 全书批注（面板按章分组显示）；`currentAnnotations` 是喂给 JS 画线的那份。
    @Published private(set) var allAnnotations: [BookAnnotation] = []
    /// 批注载入失败（面板要区分「真的没有批注」和「没读出来」）
    @Published private(set) var annotationsFailed = false
    /// 当前读到目录里的哪一条（key = chapterIndex:fragment）。滚动时跟着变，
    /// 面板高亮与顶栏章名都靠它 —— 不然「一个 spine 文件装整本书」的书会把整列都标成当前。
    @Published private(set) var currentTOCKey: String?
    @Published private(set) var annotations: [BookAnnotation] = []
    @Published private(set) var isReady = false
    @Published private(set) var isLoadingChapter = true
    /// 目录/批注面板开合（记忆跨书、跨启动）
    @Published var tocOpen = false
    @Published var annotOpen = false
    /// 批注面板里刚被点中的那条（滚动 + 闪烁）
    @Published var focusedAnnotationID: String?
    /// 本章查找（切章后要按同一关键词重跑，否则栏里显示「无匹配」但框里还有字）
    private var activeFindQuery = ""
    @Published private(set) var matchCount = 0
    @Published private(set) var matchIndex = -1

    weak var webView: WKWebView?

    private var pendingOffset: Int
    private var saveTask: Task<Void, Never>?
    /// 换节令牌：只有最后一次 `loadSection` 允许写进 `sectionIndex`
    private var chapterLoadToken = UUID()
    /// 注释文件令牌：连点两个注标时，只认最后一次读回来的内容
    private var noteRequestToken = UUID()
    /// 本章锚点（fragment → 拍平偏移），由 JS 在 `chapterReady` 后回报
    private var anchorOffsets: [String: Int] = [:]
    /// 跨章跳目录时暂存锚点，等新章渲染完再滚过去
    private var pendingAnchor: String?
    /// 批注是否已从磁盘读成功：失败时禁止写盘，免得把用户的批注覆盖成空
    private var annotationsLoaded = false
    /// `meta.json` 的写盘（进度 / 目录补全）走同一条串行队列：
    /// 两者都是「读-改-整份写」，并发时后完成者会把对方的改动盖回去。
    private let metaWriteQueue = DispatchQueue(label: "xhanHelper.book.meta.write")
    /// 书内批注写盘串行队列：并发 detached 各自快照整份列表，后完成者会写回旧快照
    /// （快速「建一条 + 删一条」会把删掉的写回来），所以按修改顺序串行落盘
    private let annotationWriteQueue = DispatchQueue(label: "xhanHelper.book.annotations.write")
    private var readTrace: BookActivity.ReadTrace?
    private let settings: BookReadingSettings

    init(book: BookMeta, settings: BookReadingSettings) {
        self.settings = settings
        let store = BookStore(root: LocalPaths.booksDir)
        self.store = store
        // **以磁盘为准**：书架里那份 `BookMeta` 只在「点‹书架」时重载，从别的面板切走再进来
        // 拿到的是旧快照 —— 拿它定位会把读者送回上一次的章节（真机踩过）
        var latest = book
        do {
            if let disk = try store.meta(id: book.id) { latest = disk }
        } catch {
            LogBuffer.shared.warning("书阅读器：读 meta.json 失败，暂用书架快照 \(error)")
        }
        self.book = latest
        let progress = latest.progress
            ?? BookProgress(chapterIndex: 0, sectionIndex: 0, offset: 0, updatedAt: Date())
        let sections = latest.effectiveSections
        self.sectionIndex = max(0, min(progress.sectionIndex ?? 0, max(sections.count - 1, 0)))
        self.pendingOffset = max(0, progress.offset)
        self.bookProgress = latest.progressFraction
        refreshTOCIfStale()
    }

    /// 老 `meta.json`（旧解析器写的：目录没锚点 / 带空标题 / 没切节 / 层级全平）在打开时补一次。
    /// `parserVersion` 落后就补，正常书不会白跑解析。
    private func refreshTOCIfStale() {
        // 版本号为主：老 meta（nil）与「被旧解析器写坏」的都补一次；新导入的书直接跳过
        guard (book.parserVersion ?? 0) < BookStore.currentParserVersion else { return }
        let store = self.store
        let id = book.id
        Task {
            // 与进度写盘排在同一条队列：否则补全写回的 meta 可能盖掉刚存的进度
            let refreshed = await withCheckedContinuation { (cont: CheckedContinuation<BookMeta?, Never>) in
                metaWriteQueue.async {
                    do { cont.resume(returning: try store.refreshStructure(id: id)) }
                    catch {
                        NSLog("[BookReader] 目录补全失败：\(error)")
                        cont.resume(returning: nil)
                    }
                }
            }
            guard let refreshed else { return }
            LogBuffer.shared.info("书阅读器：结构已按新解析补全（\(refreshed.toc.count) 条目录 / \(refreshed.effectiveSections.count) 节）")
            book = refreshed
            // 结构重算会把批注按新节号重映射（磁盘已改）：重读一次，否则内存里还是旧节号，划线全画不出
            if annotationsLoaded { loadAnnotations() }
            sectionIndex = max(0, min(sectionIndex, max(refreshed.effectiveSections.count - 1, 0)))
            // 迁移后的进度可能落在别的节（老进度只记了章内偏移）：跟过去，别让读者回到书首
            if let migrated = refreshed.progress?.sectionIndex,
               migrated != sectionIndex,
               refreshed.section(at: migrated) != nil {
                sectionIndex = migrated
                pendingOffset = max(0, refreshed.progress?.offset ?? 0)
                if isReady { loadSection(migrated, offset: pendingOffset, anchor: nil) }
            }
            // 新目录的锚点要立刻重新换算，否则「当前读到哪一条」还是空的
            if isReady { loadAnchorOffsets() }
            updateCurrentTOC(offset: pendingOffset)
        }
    }

    var currentSection: BookSection? { book.section(at: sectionIndex) }
    var chapterIndex: Int { currentSection?.chapterIndex ?? 0 }
    var chapter: BookChapter? { book.chapter(at: chapterIndex) }
    var hasChapterNavigation: Bool { book.hasChapterNavigation }
    var sectionCount: Int { book.effectiveSections.count }
    var chapterLabel: String {
        hasChapterNavigation ? "第 \(chapterIndex + 1)/\(book.chapterCount) 章" : ""
    }
    /// 本章内的一节都没了 → 整本到底
    var canGoPreviousSection: Bool { sectionIndex > 0 }
    var canGoNextSection: Bool { sectionIndex + 1 < sectionCount }
    var currentAnnotations: [BookAnnotation] {
        // 老批注没有 sectionIndex：仍要按章过滤 —— 否则同一句话在别的章会被误画，
        // 而且每节都会重试定位、刷一堆「未画出」的日志
        annotations.filter {
            $0.chapterIndex == chapterIndex && ($0.sectionIndex == nil || $0.sectionIndex == sectionIndex)
        }
    }

    /// 当前目录条目标题（滚动时更新；没有 TOC 退回章标题）
    var currentTOCTitle: String? {
        guard let key = currentTOCKey else { return nil }
        return book.toc.first { $0.key == key }?.title
    }

    /// 顶栏标签：「一文件装整本书」的书用目录条目标题，普通书用 第 N/M 章
    var headerLabel: String {
        if book.toc.count > book.chapterCount, let title = currentTOCTitle { return title }
        return chapterLabel
    }

    // MARK: - 生命周期

    /// WebView 首次 ready 之后再灌正文（否则 `reader` 对象还不存在）。
    func handleReady() {
        isReady = true
        loadAnnotations()
        loadSection(sectionIndex, offset: pendingOffset, anchor: nil)
        let focused = webView.map { $0.window?.firstResponder === $0 } ?? false
        LogBuffer.shared.info("书阅读器：页面就绪，正文是否第一响应者=\(focused ? "是" : "否（键盘进不来）")")
        // WebView 重建会让 ready 再来一次：先收旧区间，否则旧的 started 永远配不上 ended
        if let old = readTrace {
            ActivityClient.emit(old.ended(outcome: "ok", chapter: chapterIndex,
                                          progressPct: bookProgress * 100))
        }
        let trace = BookActivity.ReadTrace(bookID: book.id)
        readTrace = trace
        ActivityClient.emit(trace.started(chapter: chapterIndex, progressPct: bookProgress * 100))
    }

    /// 离开阅读页 / app 失焦：收进度、收阅读区间（回来再开新区间）。
    func pauseReading() {
        saveTask?.cancel()
        // 先把内存里的位置写下去：离开时 WebView 正在被拆，异步取位置经常拿不到值，
        // 只靠它就会把进度写回节首（JS 每 200ms 滚动都会回报，pendingOffset 就是当前值）
        persistProgress(offset: pendingOffset)
        capturePosition { [weak self] offset in
            guard let self else { return }
            self.persistProgress(offset: offset)
        }
        guard let trace = readTrace else { return }
        ActivityClient.emit(trace.ended(outcome: "ok", chapter: chapterIndex,
                                        progressPct: bookProgress * 100))
        readTrace = nil
    }

    /// 回到前台且仍在阅读页：开一段新的阅读区间（design §八）
    func resumeReading() {
        guard readTrace == nil, isReady else { return }
        let trace = BookActivity.ReadTrace(bookID: book.id)
        readTrace = trace
        ActivityClient.emit(trace.started(chapter: chapterIndex, progressPct: bookProgress * 100))
    }

    /// 离开阅读页
    func leave() { pauseReading() }

    // MARK: - 节（阅读单元）

    func goToSection(_ index: Int, offset: Int = 0, anchor: String? = nil) {
        guard book.section(at: index) != nil else { return }
        if index == sectionIndex, anchor == nil {
            evaluate("reader.restorePosition(\(max(0, offset)))")
            pendingOffset = max(0, offset)
            return
        }
        // 换节前先把本节位置存下来（offset 属于旧节，回调可能在新节加载后才回来）
        let leaving = sectionIndex
        capturePosition { [weak self] off in
            guard let self else { return }
            self.persistProgress(offset: off, sectionIndex: leaving)
            self.loadSection(index, offset: offset, anchor: anchor)
        }
    }

    /// 目录点击：定位到该条目录所在的节；同一节里再按锚点滚（未切分的书靠这个）
    func goToTOC(_ item: BookTOCItem) {
        let target = item.sectionIndex ?? firstSectionIndex(inChapter: item.chapterIndex)
        currentTOCKey = item.key
        guard let target else { return }
        if target == sectionIndex {
            if let fragment = item.fragment {
                evaluate("reader.scrollToAnchor(\(MarkdownWebUtils.jsString(fragment)))")
            } else {
                evaluate("reader.restorePosition(0)")
            }
            pendingOffset = item.fragment.flatMap { anchorOffsets[$0] } ?? 0
            return
        }
        goToSection(target, offset: 0, anchor: item.fragment)
    }

    private func firstSectionIndex(inChapter chapter: Int) -> Int? {
        book.effectiveSections.first { $0.chapterIndex == chapter }?.index
    }

    func nextSection() { goToSection(sectionIndex + 1) }
    func previousSection() { goToSection(sectionIndex - 1) }

    /// ⌥⌘+方向：跳上一节/下一节**开头**（与手势的「上一节末页」不是一个语义）。
    /// 正文里的 JS 和 SwiftUI 快捷键都会响同一次按键，250ms 内只认一次，否则一跳两节。
    private var lastSectionJumpAt: Date?
    func jumpSection(_ delta: Int) {
        let now = Date()
        if let last = lastSectionJumpAt, now.timeIntervalSince(last) < 0.25 { return }
        lastSectionJumpAt = now
        if delta > 0 { nextSection() } else { previousSection() }
    }

    /// 翻页到头再往前翻：回上一节的**末页**（不是首行，那样等于白翻一半）。
    /// 偏移给上一节的源码区间长度，JS 会把它夹到最后一个字，`snapPage()` 收到末页。
    func previousSectionFromEnd() {
        let target = sectionIndex - 1
        guard target >= 0, let section = book.section(at: target) else { return }
        goToSection(target, offset: max(0, section.end - section.start))
    }

    /// 换节：**读盘成功后才改 `sectionIndex`**。
    /// 先改再异步换正文会开一个窗口 —— 那期间 DOM 还是旧节，而 `pauseReading`/节流保存
    /// 会拿旧节的 offset 记到新节头上（进度污染）。
    private func loadSection(_ index: Int, offset: Int, anchor: String?) {
        let sections = book.effectiveSections
        guard sections.indices.contains(index) else { return }
        let section = sections[index]
        let href = book.chapter(at: section.chapterIndex)?.href ?? ""
        let token = UUID()
        chapterLoadToken = token
        let previous = sectionIndex
        isLoadingChapter = true
        pendingOffset = max(0, offset)
        pendingAnchor = anchor
        matchCount = 0
        matchIndex = -1

        let store = self.store
        let bookID = book.id
        Task {
            do {
                // 读盘 + 抠 <body> + 切片都在后台（单章上限 20MB，不能压主线程）
                let prepared = try await Task.detached(priority: .userInitiated) {
                    try store.sectionHTML(id: bookID, chapterHref: href, section: section)
                }.value
                guard token == chapterLoadToken else { return }   // 期间又切节了，丢弃这次结果
                sectionIndex = index
                pendingOffset = max(0, offset)
                pendingAnchor = anchor
                let hasNext = index + 1 < sections.count
                sendChapter(prepared.html, dir: prepared.dir, hasNext: hasNext,
                            hasPrev: index > 0,
                            token: token, previousSection: previous)
            } catch {
                guard token == chapterLoadToken else { return }
                isLoadingChapter = false
                LogBuffer.shared.error("书阅读器：读第 \(index + 1) 节（\(href)）失败 \(error)")
                ToastCenter.shared.show("这一节读不出来：\(error.localizedDescription)")
            }
        }
    }

    /// 把整章正文送进 WebView。
    ///
    /// **不用 `evaluateJavaScript` 拼字符串字面量**：整章可达几百 KB～数 MB，正文里任何一个
    /// 让字面量解析失败的东西（怪异控制符、超长脚本）都会让整次调用失联，症状是「正文空白」。
    /// `callAsyncJavaScript` 由 WebKit 自己序列化参数，不经过 JS 解析器。
    private func sendChapter(_ html: String, dir: String, hasNext: Bool, hasPrev: Bool,
                             token: UUID, previousSection previous: Int) {
        guard let webView else { return }
        let payload: [String: Any] = ["html": html, "dir": dir, "hasNext": hasNext, "hasPrev": hasPrev]
        webView.callAsyncJavaScript("reader.setChapter(html, dir, hasNext, hasPrev);",
                                    arguments: payload, in: nil, in: .page) { [weak self] result in
            Task { @MainActor in
                guard let self, token == self.chapterLoadToken else { return }
                guard case .failure(let error) = result else { return }
                self.isLoadingChapter = false
                // DOM 还停在上一节，状态不能先跑到新节（否则后续进度会记到错的节上）
                self.sectionIndex = previous
                LogBuffer.shared.error("书阅读器：章节送进 WebView 失败 \(error)")
                ToastCenter.shared.show("这一章没能送进阅读器：\(error.localizedDescription)")
            }
        }
    }

    // MARK: - 进度

    func scheduleProgressSave() {
        saveTask?.cancel()
        saveTask = Task { [weak self] in
            do {
                try await Task.sleep(nanoseconds: 2_000_000_000)
            } catch {
                return   // 取消 = 正常退出（翻页太快会连续重置）
            }
            guard !Task.isCancelled else { return }
            self?.capturePosition { [weak self] offset in
                self?.persistProgress(offset: offset)
            }
        }
    }

    private func capturePosition(_ done: @escaping (Int) -> Void) {
        guard let webView else { done(pendingOffset); return }
        webView.evaluateJavaScript("reader.position()") { value, error in
            Task { @MainActor in
                if let error {
                    LogBuffer.shared.warning("书阅读器：取阅读位置失败，沿用已记录的位置 \(error)")
                }
                // 取不到就沿用 pendingOffset —— 绝不能把「不知道」当成 0
                let off = (value as? NSNumber)?.intValue ?? self.pendingOffset
                self.pendingOffset = max(0, off)
                // 这里**不**更新 `currentTOCKey`：`goToSection` 的定位回调早于换节，
                // 会拿旧节的 `sectionIndex` 把刚点中的目录项算回旧条目（高亮闪一下）。
                // 高亮只由 `position` 消息（滚动 200ms 采样）与 `chapterReady` 负责。
                done(off)
            }
        }
    }

    private func persistProgress(offset: Int, sectionIndex explicit: Int? = nil) {
        guard let section = book.section(at: explicit ?? sectionIndex) else { return }
        pendingOffset = offset
        var meta = book
        // 不按 `charCount` 夹：它是去空白字数，JS 的 offset 含空白与换行，夹了会把末尾进度回拉
        meta.progress = BookProgress(chapterIndex: section.chapterIndex,
                                     sectionIndex: section.index,
                                     offset: max(0, offset),
                                     updatedAt: Date())
        meta.lastReadAt = Date()
        // 按节字数加权算全书进度（与书架细进度条同口径）
        let all = book.effectiveSections
        let total = all.reduce(0) { $0 + $1.charCount }
        if total > 0 {
            let before = all.prefix(section.index).reduce(0) { $0 + $1.charCount }
            bookProgress = min(1, Double(before + pendingOffset) / Double(total))
        }
        let store = self.store
        let bookID = book.id
        let progress = meta.progress
        let readAt = meta.lastReadAt
        let fallback = book
        metaWriteQueue.async {
            do {
                // 队列内重读：`refreshStructure` 可能刚写了 sections / parserVersion，
                // 拿内存里的旧快照整份覆盖会把它们抹回 nil（下次打开又得重解析）
                var disk = try store.meta(id: bookID) ?? fallback
                disk.progress = progress
                disk.lastReadAt = readAt
                try store.save(disk)
            } catch {
                NSLog("[BookReader] 保存进度失败：\(error)")
            }
        }
    }

    // MARK: - 排版

    func applyTypo(_ s: BookReadingSettings) {
        evaluate("reader.setTypo(\(bookTypographyJSON(s)))")
    }

    func applyReadingMode(_ s: BookReadingSettings) {
        evaluate("reader.setReadingMode(\(MarkdownWebUtils.jsString(s.mode == .paged ? "paged" : "scroll")))")
    }

    private func bookTypographyJSON(_ s: BookReadingSettings) -> String {
        let t = s.typography
        return "{fs:\(String(format: "%.2f", t.fontSize)),line:\(t.lineHeight),para:\(t.paragraphGap),"
             + "measure:\(String(format: "%.1f", t.measure)),font:\(MarkdownWebUtils.jsString(t.fontFamily))}"
    }

    /// 让 JS 把**本节**的目录锚点换算成拍平偏移（同一节里有多个目录条目时才用得上）
    private func loadAnchorOffsets() {
        anchorOffsets = [:]
        let fragments = book.toc
            .filter { ($0.sectionIndex ?? firstSectionIndex(inChapter: $0.chapterIndex)) == sectionIndex }
            .compactMap { $0.fragment }
        guard !fragments.isEmpty, let webView else {
            updateCurrentTOC(offset: pendingOffset)
            return
        }
        guard let data = try? JSONEncoder().encode(fragments),
              let json = String(data: data, encoding: .utf8) else {
            LogBuffer.shared.error("书阅读器：目录锚点编码失败")
            return
        }
        webView.evaluateJavaScript("reader.collectAnchors(\(MarkdownWebUtils.jsString(json)))") { value, error in
            Task { @MainActor in
                if let error {
                    LogBuffer.shared.warning("书阅读器：目录锚点定位失败 \(error)")
                    return
                }
                guard let raw = value as? String,
                      let payload = raw.data(using: .utf8) else { return }
                do {
                    let rows = try JSONDecoder().decode([AnchorOffset].self, from: payload)
                    // 同一锚点可能被多条目录共用（JS 逐 id 回报，重复 id 会出现多次）：
                    // `uniqueKeysWithValues` 遇重复会直接 trap
                    self.anchorOffsets = Dictionary(rows.map { ($0.id, $0.offset) },
                                                    uniquingKeysWith: { first, _ in first })
                } catch {
                    LogBuffer.shared.warning("书阅读器：目录锚点解析失败 \(error)")
                }
                self.updateCurrentTOC(offset: self.pendingOffset)
            }
        }
    }

    private struct AnchorOffset: Decodable {
        let id: String
        let offset: Int
    }

    /// 当前节对应的目录条目；同一节里有多条（未切分的小章）时按锚点偏移挑
    private func updateCurrentTOC(offset: Int) {
        let items = book.toc.filter {
            ($0.sectionIndex ?? firstSectionIndex(inChapter: $0.chapterIndex)) == sectionIndex
        }
        guard !items.isEmpty else {
            currentTOCKey = nil
            return
        }
        guard items.count > 1, !anchorOffsets.isEmpty else {
            currentTOCKey = items.first?.key
            return
        }
        // 不假设锚点按偏移单调（作者手写 nav 可能乱序、也可能缺锚点）：取「不超过当前位置的最大偏移」
        var best: BookTOCItem?
        var bestAt = Int.min
        for item in items {
            guard let fragment = item.fragment, let at = anchorOffsets[fragment] else { continue }
            if at <= offset + 2, at >= bestAt {
                bestAt = at
                best = item
            }
        }
        currentTOCKey = (best ?? items.first)?.key
    }

    // MARK: - 查找

    func find(_ query: String) {
        activeFindQuery = query
        evaluate("reader.find(\(MarkdownWebUtils.jsString(query)))")
    }
    func findNext() { evaluate("reader.findNext()") }
    func findPrevious() { evaluate("reader.findPrev()") }
    func clearFind() {
        activeFindQuery = ""
        evaluate("reader.clearFind()")
    }

    // MARK: - 批注

    private func loadAnnotations() {
        let store = self.store
        let bookID = book.id
        Task {
            do {
                allAnnotations = try await Task.detached(priority: .userInitiated) {
                    try store.annotations(bookID: bookID)
                }.value
                annotationsLoaded = true
                annotationsFailed = false
                publishAnnotations()
            } catch {
                // 读不出来不能当「没有批注」：标成未加载，后续所有写盘一律拒掉
                // （否则用户新建一条就会把磁盘上的旧批注整份覆盖成只有新的一条）
                annotationsLoaded = false
                annotationsFailed = true
                LogBuffer.shared.error("书阅读器：读批注失败，本次禁止写入 \(error)")
                ToastCenter.shared.show("批注读取失败：本次只浏览，不改动已有批注")
            }
        }
    }

    private func publishAnnotations() {
        annotations = allAnnotations
        guard let data = try? JSONEncoder().encode(currentAnnotations),
              let json = String(data: data, encoding: .utf8) else {
            LogBuffer.shared.error("书阅读器：批注编码失败")
            return
        }
        evaluate("reader.setAnnotations(\(MarkdownWebUtils.jsString(json)))")
    }

    /// 串行队列 + 每次快照当前全量：FIFO 保证「后改的」最后落盘，不会被旧快照盖回去。
    private func saveAnnotations() {
        guard annotationsLoaded else {
            LogBuffer.shared.error("书阅读器：批注未成功载入，拒绝写盘（避免覆盖已有批注）")
            ToastCenter.shared.show("批注未能载入，本次改动不保存")
            return
        }
        let store = self.store
        let bookID = book.id
        let items = allAnnotations
        annotationWriteQueue.async {
            do { try store.saveAnnotations(items, bookID: bookID) } catch {
                NSLog("[BookReader] 保存批注失败：\(error)")
            }
        }
    }

    /// 设计 §6.1 的配额：单条 quote ≤ 500 字、批注 ≤ 2000 字、单本 ≤ 2000 条
    private enum Limit {
        static let quote = 500
        static let note = 2000
        static let perBook = 2000
    }

    private func create(quote: String, prefix: String, suffix: String, note: String) {
        // 盘上那份还没读进来时不要建：建了也只能画在屏上，随后会被载入结果覆盖（等于白丢）
        guard annotationsLoaded else {
            ToastCenter.shared.show("批注没能载入，本次改动不保存")
            return
        }
        guard quote.count <= Limit.quote else {
            ToastCenter.shared.show("选中的内容太长（上限 \(Limit.quote) 字），缩短一点再划线")
            return
        }
        guard allAnnotations.count < Limit.perBook else {
            ToastCenter.shared.show("这本书的划线已达上限（\(Limit.perBook) 条）")
            return
        }
        let note = truncatedNote(note)
        let now = Date()
        let item = BookAnnotation(id: UUID().uuidString, chapterIndex: chapterIndex,
                                  sectionIndex: sectionIndex,
                                  quote: quote, prefix: prefix, suffix: suffix, note: note,
                                  createdAt: now, updatedAt: now)
        allAnnotations.append(item)
        annotations = allAnnotations
        publishAnnotations()
        saveAnnotations()
        ActivityClient.emit(BookActivity.annotationSaved(bookID: book.id, annotationID: item.id))
    }

    /// 超长批注截断并告知（静默截断等于悄悄改用户写的东西）
    private func truncatedNote(_ note: String) -> String {
        guard note.count > Limit.note else { return note }
        ToastCenter.shared.show("批注太长，已截断到 \(Limit.note) 字")
        return String(note.prefix(Limit.note))
    }

    private func update(id: String, note: String) {
        guard let i = allAnnotations.firstIndex(where: { $0.id == id }) else { return }
        let note = truncatedNote(note)
        guard note != allAnnotations[i].note else { return }   // 没改就不写盘、不计数
        allAnnotations[i].note = note
        allAnnotations[i].updatedAt = Date()
        annotations = allAnnotations
        publishAnnotations()
        saveAnnotations()
        ActivityClient.emit(BookActivity.annotationSaved(bookID: book.id, annotationID: id))
    }

    private func remove(id: String) {
        allAnnotations.removeAll { $0.id == id }
        annotations = allAnnotations
        if focusedAnnotationID == id { focusedAnnotationID = nil }
        publishAnnotations()
        saveAnnotations()
        ActivityClient.emit(BookActivity.annotationDeleted(bookID: book.id, annotationID: id))
    }

    // MARK: - 面板动作

    func updateNote(id: String, note: String) {
        update(id: id, note: note)
    }

    func deleteAnnotation(id: String) {
        remove(id: id)
    }

    /// 批注面板点条目 → 正文滚动 + 闪烁
    func scrollToAnnotation(id: String) {
        focusedAnnotationID = id
        let target = allAnnotations.first { $0.id == id }
        guard let target else { return }
        let targetSection = target.sectionIndex ?? firstSectionIndex(inChapter: target.chapterIndex)
        if let targetSection, targetSection != sectionIndex {
            // 跨节跳转先切节，等 chapterReady 再画 + 定位
            goToSection(targetSection)
            return
        }
        evaluate("reader.scrollToAnnotation(\(MarkdownWebUtils.jsString(id)))")
    }

    // MARK: - JS 消息

    func apply(message: [String: Any]) {
        guard let type = message["type"] as? String else { return }
        switch type {
        case "ready":
            handleReady()

        case "chapterReady":
            isLoadingChapter = false
            if let error = message["error"] as? String {
                LogBuffer.shared.error("书阅读器：章节渲染失败 \(error)")
                ToastCenter.shared.show("这一章渲染失败：\(error)")
            }
            publishAnnotations()
            if let anchor = pendingAnchor {
                pendingAnchor = nil
                evaluate("reader.scrollToAnchor(\(MarkdownWebUtils.jsString(anchor)))")
            } else {
                evaluate("reader.restorePosition(\(pendingOffset))")
            }
            loadAnchorOffsets()
            // 只有「这一节里的」定位目标才滚；旧节留下的 id 要清掉，免得同 quote 在新节误定位
            if let id = focusedAnnotationID,
               let target = allAnnotations.first(where: { $0.id == id }),
               (target.sectionIndex ?? firstSectionIndex(inChapter: target.chapterIndex)) == sectionIndex {
                evaluate("reader.scrollToAnnotation(\(MarkdownWebUtils.jsString(id)))")
            } else {
                focusedAnnotationID = nil
            }
            // 切章后把当前关键词在新章重跑一遍，查找栏的命中数才对得上框里的字
            if !activeFindQuery.isEmpty {
                evaluate("reader.find(\(MarkdownWebUtils.jsString(activeFindQuery)))")
            }

        case "scroll":
            scheduleProgressSave()

        case "position":
            if let off = (message["offset"] as? NSNumber)?.intValue {
                pendingOffset = max(0, off)
                updateCurrentTOC(offset: pendingOffset)
            }

        case "create":
            let quote = message["quote"] as? String ?? ""
            guard !quote.isEmpty else { return }
            create(quote: quote,
                   prefix: message["prefix"] as? String ?? "",
                   suffix: message["suffix"] as? String ?? "",
                   note: message["note"] as? String ?? "")

        case "update":
            guard let id = message["id"] as? String else { return }
            update(id: id, note: message["note"] as? String ?? "")

        case "delete":
            guard let id = message["id"] as? String else { return }
            remove(id: id)

        case "focus":
            guard let id = message["id"] as? String, !id.isEmpty else { return }
            focusedAnnotationID = id
            annotOpen = true

        case "copy":
            let text = message["text"] as? String ?? ""
            guard !text.isEmpty else { return }
            let pb = NSPasteboard.general
            pb.clearContents()
            pb.setString(text, forType: .string)
            ToastCenter.shared.show("已复制")

        case "toast":
            let text = message["text"] as? String ?? ""
            if !text.isEmpty { ToastCenter.shared.show(text) }

        case "nextSection":
            nextSection()

        case "prevSection":
            previousSectionFromEnd()

        case "sectionJump":
            jumpSection(message["delta"] as? Int ?? 1)

        case "noteFile":
            handleNoteFile(message)

        case "bookLink":
            if let href = message["href"] as? String { openBookLink(href) }

        case "openExternal":
            if let href = message["href"] as? String { openExternal(href) }

        case "diag":
            // 导航诊断：真机上「按键/滚轮换节不灵」时看这条。
            // 没有 kind=key 的日志 = 事件没进到页面（焦点不在正文）；
            // 有 key 但跟着 advance-skip = 锁或没有相邻节。
            let kind = message["kind"] as? String ?? "?"
            let rest = message
                .filter { $0.key != "type" && $0.key != "kind" }
                .sorted { $0.key < $1.key }
                .map { "\($0.key)=\($0.value)" }
                .joined(separator: " ")
            LogBuffer.shared.info("书阅读器·导航 \(kind) \(rest)")

        case "findResult":
            matchCount = message["count"] as? Int ?? 0
            matchIndex = message["index"] as? Int ?? -1

        case "annotationsMissed":
            // 本地批注理论上都能画出来；画不出说明章节正文变了（书被重新解包），留痕即可
            LogBuffer.shared.warning("书阅读器：\(message["count"] as? Int ?? 0) 条划线未画出")

        case "image":
            // 书内配图点开放大（与文档阅读器同一套浮层）
            guard let src = message["src"] as? String, !src.isEmpty else { return }
            LightboxCenter.shared.present { DocumentImageLightbox(src: src) }

        default:
            LogBuffer.shared.info("书阅读器：未知消息 \(type)")
        }
    }


    // MARK: - 脚注 / 链接

    /// 注释目标不在当前节（同文件其他节 / 另一个文件 / `linear="no"` 的尾注文件）：
    /// 读回整份 XHTML 再让 JS 取 `#frag`，读者不离开当前位置。
    private func handleNoteFile(_ message: [String: Any]) {
        guard let section = book.section(at: sectionIndex),
              let current = book.chapter(at: section.chapterIndex) else { return }
        let raw = (message["href"] as? String) ?? ""
        let frag = (message["frag"] as? String) ?? ""
        guard !frag.isEmpty else { return }

        let resolved: String
        if raw.isEmpty || raw.hasPrefix("#") {
            resolved = current.href              /* 同文件跨节：读当前章整份 */
        } else {
            guard let normalized = EpubArchive.normalize(
                raw, baseDir: EpubArchive.dirName(of: current.href)
            ) else {
                LogBuffer.shared.error("书阅读器：注释路径非法 \(raw)")
                ToastCenter.shared.show("这条注释的路径不合法")
                return
            }
            resolved = normalized
        }
        let ext = (resolved as NSString).pathExtension.lowercased()
        guard ["xhtml", "html", "htm"].contains(ext) else {
            LogBuffer.shared.error("书阅读器：注释文件类型不支持 \(resolved)")
            ToastCenter.shared.show("这条注释的文件类型不支持")
            return
        }

        let fileURL = store.contentDir(book.id).appendingPathComponent(resolved)
        let size: Int
        do {
            let attrs = try FileManager.default.attributesOfItem(atPath: fileURL.path)
            size = (attrs[.size] as? NSNumber)?.intValue ?? 0
        } catch {
            LogBuffer.shared.error("书阅读器：注释文件读不到 \(resolved)：\(error)")
            ToastCenter.shared.show("这条注释读不出来")
            return
        }
        guard size > 0, size <= 5 * 1024 * 1024 else {
            LogBuffer.shared.error("书阅读器：注释文件为空或过大 \(resolved)（\(size) 字节）")
            ToastCenter.shared.show("这条注释读不出来")
            return
        }

        let token = UUID()
        noteRequestToken = token
        let store = self.store
        let bookID = book.id
        let base = fileURL.absoluteString
        Task {
            do {
                let html = try await Task.detached(priority: .userInitiated) {
                    try store.chapterHTML(id: bookID, href: resolved)
                }.value
                guard token == noteRequestToken, let webView else { return }
                webView.callAsyncJavaScript(
                    "reader.showExternalNote(html, base, frag);",
                    arguments: ["html": html, "base": base, "frag": frag],
                    in: nil, in: .page
                ) { result in
                    if case .failure(let error) = result {
                        LogBuffer.shared.error("书阅读器：注释渲染失败 \(error)")
                        ToastCenter.shared.show("这条注释没能显示")
                    }
                }
            } catch {
                guard token == noteRequestToken else { return }
                LogBuffer.shared.error("书阅读器：读注释文件失败 \(resolved)：\(error)")
                ToastCenter.shared.show("这条注释读不出来")
            }
        }
    }

    /// 普通书内链接：按当前章目录归一 → 找章节 → 跳节/滚锚点。
    private func openBookLink(_ raw: String) {
        guard let section = book.section(at: sectionIndex),
              let current = book.chapter(at: section.chapterIndex),
              let normalized = EpubArchive.normalize(
                raw, baseDir: EpubArchive.dirName(of: current.href)
              ),
              let target = book.chapters.first(where: { $0.href == normalized }) else {
            LogBuffer.shared.warning("书阅读器：书内链接目标不在阅读顺序里 \(raw)")
            ToastCenter.shared.show("这个链接不在书里")
            return
        }
        let fragment = EpubArchive.fragment(of: raw)
        if let item = book.toc.first(where: { $0.chapterIndex == target.index && $0.fragment == fragment }) {
            goToTOC(item)
            return
        }
        guard let targetSection = firstSectionIndex(inChapter: target.index) else { return }
        if targetSection == sectionIndex, let fragment {
            evaluate("reader.scrollToAnchor(\(MarkdownWebUtils.jsString(fragment)))")
            return
        }
        goToSection(targetSection, offset: 0, anchor: fragment)
    }

    /// 外链只放行这三种 scheme；其余（file:/javascript:/data:）一律不交给系统。
    private func openExternal(_ raw: String) {
        guard let url = URL(string: raw),
              let scheme = url.scheme?.lowercased(),
              ["http", "https", "mailto"].contains(scheme) else {
            LogBuffer.shared.warning("书阅读器：外链 scheme 不允许 \(raw.prefix(32))")
            ToastCenter.shared.show("这个链接不能打开")
            return
        }
        if NSWorkspace.shared.open(url) {
            LogBuffer.shared.info("书阅读器：打开外链 \(scheme)://\(url.host ?? "")")
        } else {
            LogBuffer.shared.error("书阅读器：打开外链失败 \(scheme)://\(url.host ?? "")")
            ToastCenter.shared.show("没能打开链接")
        }
    }

    private func evaluate(_ js: String, onError: ((Error) -> Void)? = nil) {
        guard let webView else { return }
        webView.evaluateJavaScript(js) { _, error in
            guard let error else { return }
            LogBuffer.shared.error("书阅读器 JS 执行失败：\(error.localizedDescription)")
            if let onError {
                Task { @MainActor in onError(error) }
            }
        }
    }
}
#endif
