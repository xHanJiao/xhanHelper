#if os(macOS)
import AppKit
import Combine
import Foundation
import UniformTypeIdentifiers

/// 书架状态：本地书列表、导入队列、失败卡。数据全在本机（`Books/`），不经服务端。
///
/// 文件操作全部丢后台（Epub 内核是 `nonisolated`，见批 1）；本类只持 UI 状态。
@MainActor
final class BookViewModel: ObservableObject {

    /// 全 app 共用一份：书架是本地单例状态，经视图树层层透传会逼着每个中间层
    /// 都背一个 macOS-only 参数（`BrowsePanelView`/`RightSidebarView` 是双目标编译）。
    static let shared = BookViewModel()

    init() {}

    /// 正在导入的一本（网格前插的「导入中」卡）
    struct ImportJob: Identifiable, Equatable {
        let id: String
        var title: String
        var step: EpubImportStep
        var sourceURL: URL
    }

    /// 导入失败卡（DRM / 固定版式 / 损坏 → 徽章 + 一行原因 + tooltip 详释）
    struct ImportFailure: Identifiable, Equatable {
        let id: String
        var title: String
        var error: BookImportError
        /// 重试用；源文件已被用户移走时为 nil
        var sourceURL: URL?
    }

    @Published private(set) var books: [BookMeta] = []
    @Published private(set) var jobs: [ImportJob] = []
    @Published private(set) var failures: [ImportFailure] = []
    @Published private(set) var isLoading = false
    /// 书架读不出来（目录权限、整盘挂载问题）：不能显示成「空书架」
    @Published private(set) var loadError: String?
    /// 有 `meta.json` 读不出来的目录数（损坏的书不显示，但也不静默消失）
    @Published private(set) var brokenBookCount = 0

    /// 打开着的书（nil = 书架网格）。阅读页在批 3 消费。
    @Published var openBook: BookMeta?

    var isImporting: Bool { !jobs.isEmpty }

    private let store = BookStore(root: LocalPaths.booksDir)

    func coverURL(for book: BookMeta) -> URL? {
        book.coverFile.map { store.bookDir(book.id).appendingPathComponent($0) }
    }

    func bookDirectory(for book: BookMeta) -> URL {
        store.bookDir(book.id)
    }

    // MARK: - 列表

    func load() async {
        // 不做「正在加载就跳过」的守卫：页头刷新常常落在启动那次扫描的窗口里，
        // 跳过就等于按钮没反应（`listBooks` 是本地毫秒级扫描，重复跑无害）。
        isLoading = true
        defer { isLoading = false }
        let store = self.store
        do {
            let listing = try await Task.detached(priority: .userInitiated) {
                try store.listBooks()
            }.value
            books = listing.books
            brokenBookCount = listing.failures.count
            loadError = nil
            // 读不出来的书：书架继续可用，但原因要留痕（别让书「莫名消失」）
            for failure in listing.failures {
                LogBuffer.shared.warning("书架：读不出书目录 \(failure.id)：\(failure.message)")
            }
        } catch {
            loadError = "书架读取失败：\(error.localizedDescription)"
            LogBuffer.shared.error(loadError ?? "书架读取失败")
        }
    }

    // MARK: - 导入

    /// 页头导入按钮：`NSOpenPanel` 多选（拖拽走 `BookshelfView` 的 `onDrop`，同一条管线）
    func presentImportPanel() {
        let panel = NSOpenPanel()
        panel.allowsMultipleSelection = true
        panel.canChooseDirectories = false
        panel.canChooseFiles = true
        if let epub = UTType(filenameExtension: "epub") {
            panel.allowedContentTypes = [epub]
        }
        panel.prompt = "导入"
        guard panel.runModal() == .OK else { return }
        importFiles(panel.urls)
    }

    /// 同一批里重复 + 已在导入的同一文件直接忽略（查重靠 SHA256，但两个并发导入会同时通过）
    func importFiles(_ urls: [URL]) {
        var seen = Set(jobs.map { $0.sourceURL.standardizedFileURL })
        var rejected: [String] = []
        for url in urls {
            let std = url.standardizedFileURL
            guard std.pathExtension.lowercased() == "epub" else {
                LogBuffer.shared.warning("书架：忽略非 .epub 文件 \(std.path)")
                rejected.append(std.lastPathComponent)
                continue
            }
            guard seen.insert(std).inserted else {
                ToastCenter.shared.show("《\(std.deletingPathExtension().lastPathComponent)》正在导入")
                continue
            }
            startImport(std)
        }
        if !rejected.isEmpty {
            ToastCenter.shared.show("只支持 .epub 文件：\(rejected.prefix(3).joined(separator: "、"))")
        }
    }

    private func startImport(_ url: URL) {
        let jobID = UUID().uuidString
        let title = url.deletingPathExtension().lastPathComponent
        let trace = BookActivity.ImportTrace(fileURL: url)
        jobs.append(ImportJob(id: jobID, title: title, step: .hashing, sourceURL: url))
        ActivityClient.emit(trace.started())

        let store = self.store
        Task.detached(priority: .userInitiated) {
            let result: Result<BookMeta, BookImportError>
            do {
                let meta = try store.importBook(source: url) { step in
                    // 回调在后台线程，回主线程推进「导入中」卡的步骤
                    Task { @MainActor [weak self] in
                        self?.advance(jobID, to: step)
                    }
                }
                result = .success(meta)
            } catch {
                result = .failure(Self.mapped(error))
            }
            await MainActor.run {
                self.finishImport(jobID, sourceURL: url, title: title, trace: trace, result: result)
            }
        }
    }

    /// 未知错误只可能是文件内容/解析问题（磁盘错误在 Epub 内核里已折成 `.writeFailed`）
    private nonisolated static func mapped(_ error: Error) -> BookImportError {
        (error as? BookImportError) ?? .invalidFile(error.localizedDescription)
    }

    private func advance(_ jobID: String, to step: EpubImportStep) {
        guard let index = jobs.firstIndex(where: { $0.id == jobID }) else { return }
        jobs[index].step = step
    }

    private func finishImport(_ jobID: String, sourceURL: URL, title: String,
                              trace: BookActivity.ImportTrace,
                              result: Result<BookMeta, BookImportError>) {
        jobs.removeAll { $0.id == jobID }
        switch result {
        case .success(let meta):
            ActivityClient.emit(trace.ended(outcome: "ok", error: nil, bookID: meta.id,
                                            chapters: meta.chapterCount))
            // 直接插入而不是重扫磁盘：`importBook` 写的就是这一份 meta，重扫还会和 `load()`
            // 的 `isLoading` 守卫抢（正在刷新时那次重扫会被吞掉，新书不出现）。
            books.removeAll { $0.id == meta.id }
            books.append(meta)
            books.sort { ($0.lastReadAt ?? $0.importedAt) > ($1.lastReadAt ?? $1.importedAt) }
        case .failure(let error):
            ActivityClient.emit(trace.ended(outcome: "failed", error: error, bookID: nil, chapters: 0))
            LogBuffer.shared.error("书架：导入《\(title)》失败 \(error.diagnostic)")
            if case .duplicate = error {
                // 查重命中只提示，不建失败卡（书已经在架上了）
                ToastCenter.shared.show(error.reason)
            } else {
                failures.insert(ImportFailure(id: UUID().uuidString, title: title,
                                              error: error, sourceURL: sourceURL), at: 0)
            }
        }
    }

    // MARK: - 失败卡

    func retry(_ failure: ImportFailure) {
        guard let url = failure.sourceURL else {
            ToastCenter.shared.show("原文件已不在，请重新导入")
            return
        }
        guard FileManager.default.fileExists(atPath: url.path) else {
            ToastCenter.shared.show("原文件已不在，请重新导入")
            return
        }
        failures.removeAll { $0.id == failure.id }
        importFiles([url])
    }

    func dismiss(_ failure: ImportFailure) {
        failures.removeAll { $0.id == failure.id }
    }

    // MARK: - 删除

    /// 删目录（含批注）。不可恢复，调用方负责二次确认。
    func delete(_ book: BookMeta) async {
        let store = self.store
        let id = book.id
        do {
            try await Task.detached(priority: .userInitiated) { try store.delete(id: id) }.value
            books.removeAll { $0.id == id }
            if openBook?.id == id { openBook = nil }
        } catch {
            LogBuffer.shared.error("书架：删除《\(book.title)》失败 \(error)")
            ToastCenter.shared.show("删除失败：\(error.localizedDescription)")
        }
    }

    func revealInFinder(_ book: BookMeta) {
        NSWorkspace.shared.activateFileViewerSelecting([bookDirectory(for: book)])
    }
}
#endif
