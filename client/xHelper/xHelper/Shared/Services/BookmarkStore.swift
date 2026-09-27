import Foundation
import Combine

/// 浏览器收藏夹（持久化）：扁平列表，支持 Netscape HTML 书签导入/导出（Chrome/Safari 通用）。
/// 文件夹：url 为空的条目即文件夹，成员关系靠 folderID（仅一级嵌套，需要多级时给 folderID 加 parentID）。
@MainActor
final class BookmarkStore: ObservableObject {
    static let shared = BookmarkStore()

    struct Bookmark: Identifiable, Codable {
        let id: UUID
        var title: String
        var url: String
        var host: String
        var createdAt: Date
        var folderID: UUID? // nil = 顶层；可选字段，旧 bookmarks.json 缺 key 解码为 nil，天然兼容

        /// 文件夹判别：add() 校验过书签必有 http(s) URL，空 url 只可能是文件夹。
        var isFolder: Bool { url.isEmpty }
    }

    @Published private(set) var bookmarks: [Bookmark] = []

    private let storeURL: URL
    private var saveTask: Task<Void, Never>?
    private let maxBookmarks = 500

    private init() {
        let dir = LocalPaths.dataRoot
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        storeURL = dir.appendingPathComponent("bookmarks.json")
        load()
    }

    private func load() {
        guard let data = try? Data(contentsOf: storeURL),
              let decoded = try? JSONDecoder().decode([Bookmark].self, from: data) else { return }
        bookmarks = decoded
    }

    private func scheduleSave() {
        saveTask?.cancel()
        saveTask = Task { @MainActor [weak self] in
            try? await Task.sleep(for: .milliseconds(500))
            self?.save()
        }
    }

    private func save() {
        guard let data = try? JSONEncoder().encode(bookmarks) else { return }
        try? data.write(to: storeURL, options: .atomic)
    }

    /// 是否已收藏（URL lowercased 去重）
    func isBookmarked(url: String) -> Bool {
        bookmarks.contains { $0.url.lowercased() == url.trimmingCharacters(in: .whitespacesAndNewlines).lowercased() }
    }

    @discardableResult
    func add(title: String, url: String) -> Bool {
        let trimmed = url.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let u = URL(string: trimmed),
              let scheme = u.scheme?.lowercased(),
              scheme == "http" || scheme == "https"
        else { return false }
        if isBookmarked(url: trimmed) { return false }
        let host = u.host ?? trimmed
        let t = title.trimmingCharacters(in: .whitespacesAndNewlines)
        bookmarks.insert(Bookmark(id: UUID(), title: t.isEmpty ? host : t, url: trimmed, host: host, createdAt: Date()), at: 0)
        trim()
        scheduleSave()
        #if os(macOS)
        ActivityClient.emit(ActivityBrowserState.bookmarkAdded(url: trimmed, titleLength: t.count))
        #endif
        return true
    }

    func remove(id: UUID) {
        // 目标是文件夹时级联删除内容（Chrome 语义），所有调用点统一安全
        if let f = bookmarks.first(where: { $0.id == id }), f.isFolder {
            deleteFolder(id: id)
            return
        }
        bookmarks.removeAll { $0.id == id }
        scheduleSave()
    }

    /// 新建文件夹（默认名「新建文件夹」），返回新文件夹 id。
    @discardableResult
    func createFolder(title: String = "新建文件夹") -> UUID {
        let id = UUID()
        let t = title.trimmingCharacters(in: .whitespacesAndNewlines)
        bookmarks.insert(Bookmark(id: id, title: t.isEmpty ? "新建文件夹" : t, url: "", host: "", createdAt: Date(), folderID: nil), at: 0)
        scheduleSave()
        return id
    }

    /// 文件夹内的收藏（保持数组顺序）。
    func children(of folderID: UUID) -> [Bookmark] {
        bookmarks.filter { $0.folderID == folderID }
    }

    /// 删除文件夹及其全部内容（对标 Chrome）。
    func deleteFolder(id: UUID) {
        bookmarks.removeAll { $0.id == id || $0.folderID == id }
        scheduleSave()
    }

    /// 移动收藏进/出文件夹。仅一级：文件夹不可入文件夹。
    func moveToFolder(id: UUID, folderID: UUID?) {
        guard let i = bookmarks.firstIndex(where: { $0.id == id }), !bookmarks[i].isFolder else { return }
        if let target = folderID,
           !bookmarks.contains(where: { $0.id == target && $0.isFolder }) { return }
        bookmarks[i].folderID = folderID
        scheduleSave()
    }

    /// 拖出文件夹到指定位置：归入顶层后重排（落到顶层条目上时用）。
    func moveToTopLevel(id: UUID, toIndex target: Int) {
        guard let i = bookmarks.firstIndex(where: { $0.id == id }), !bookmarks[i].isFolder else { return }
        bookmarks[i].folderID = nil
        move(id: id, toIndex: target)
    }

    /// 重命名收藏（允许清空：空名称 = 无名字，展示时只显示图标）。
    func rename(id: UUID, title: String) {
        guard let i = bookmarks.firstIndex(where: { $0.id == id }) else { return }
        bookmarks[i].title = title.trimmingCharacters(in: .whitespacesAndNewlines)
        scheduleSave()
    }

    /// 拖拽重排：把 id 对应收藏移到指定下标（target 为移动后的最终下标）。
    func move(id: UUID, toIndex target: Int) {
        guard let from = bookmarks.firstIndex(where: { $0.id == id }) else { return }
        let target = min(max(target, 0), bookmarks.count - 1)
        guard from != target else { return }
        let b = bookmarks.remove(at: from)
        bookmarks.insert(b, at: target > from ? target - 1 : target)
        scheduleSave()
    }

    func remove(url: String) {
        let key = url.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        bookmarks.removeAll { $0.url.lowercased() == key }
        scheduleSave()
    }

    func toggle(title: String, url: String) {
        if isBookmarked(url: url) {
            remove(url: url)
        } else {
            add(title: title, url: url)
        }
    }

    /// 地址栏建议：按输入前缀匹配收藏 URL（兼容有无协议头），归一化去重后按收藏时间排序。
    func suggest(query: String, maxResults: Int = 6) -> [Bookmark] {
        let normalized = HistoryStore.normalizeURL(query)
        guard !normalized.isEmpty else { return [] }
        let raw = query.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        var seen: Set<String> = []
        var results: [Bookmark] = []
        for b in bookmarks {
            let key = b.url.lowercased()
            if seen.contains(key) { continue }
            let matches = key.hasPrefix(raw) || HistoryStore.normalizeURL(b.url).hasPrefix(normalized)
            guard matches else { continue }
            seen.insert(key)
            results.append(b)
            if results.count >= maxResults { break }
        }
        return results
    }

    /// 导入 Netscape HTML 书签（Chrome/Edge/Safari「导出书签」格式），返回新增数。
    @discardableResult
    func importFromHTML(data: Data) -> Int {
        guard let html = String(data: data, encoding: .utf8) else { return 0 }
        let pattern = #"<a[^>]+href\s*=\s*["']([^"']+)["'][^>]*>(.*?)</a>"#
        guard let regex = try? NSRegularExpression(pattern: pattern, options: [.caseInsensitive, .dotMatchesLineSeparators]) else { return 0 }
        let ns = html as NSString
        let matches = regex.matches(in: html, options: [], range: NSRange(location: 0, length: ns.length))
        var imported = 0
        for m in matches {
            guard m.numberOfRanges >= 3 else { continue }
            let urlStr = ns.substring(with: m.range(at: 1)).trimmingCharacters(in: .whitespacesAndNewlines)
            var title = ns.substring(with: m.range(at: 2))
            title = title.replacingOccurrences(of: "<[^>]+>", with: "", options: .regularExpression)
                .trimmingCharacters(in: .whitespacesAndNewlines)
            guard let u = URL(string: urlStr),
                  let scheme = u.scheme?.lowercased(),
                  scheme == "http" || scheme == "https" else { continue }
            if add(title: title, url: urlStr) {
                imported += 1
            }
        }
        return imported
    }

    /// 导出 Netscape HTML 书签（可再导入）。
    func exportHTML() -> String {
        var html = """
        <!DOCTYPE NETSCAPE-Bookmark-file-1>
        <META HTTP-EQUIV="Content-Type" CONTENT="text/html; charset=UTF-8">
        <TITLE>Bookmarks</TITLE>
        <H1>Bookmarks</H1>
        <DL><p>

        """
        for b in bookmarks where !b.isFolder {
            let safeURL = b.url.replacingOccurrences(of: "&", with: "&amp;")
                .replacingOccurrences(of: "\"", with: "&quot;")
            let safeTitle = b.title.replacingOccurrences(of: "&", with: "&amp;")
                .replacingOccurrences(of: "<", with: "&lt;").replacingOccurrences(of: ">", with: "&gt;")
            html += "<DT><A HREF=\"\(safeURL)\" ADD_DATE=\"\(Int(b.createdAt.timeIntervalSince1970))\">\(safeTitle)</A>\n"
        }
        html += "</DL><p>\n"
        return html
    }

    private func trim() {
        // 超限从尾部裁剪；裁到文件夹时级联删除其子项，避免孤儿数据（ponytail: 最坏 O(n²)，500 上限下无所谓）
        while bookmarks.count > maxBookmarks {
            let b = bookmarks.removeLast()
            if b.isFolder {
                bookmarks.removeAll { $0.folderID == b.id }
            }
        }
    }
}
