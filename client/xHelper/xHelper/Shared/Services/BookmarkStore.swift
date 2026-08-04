import Foundation
import Combine

/// 浏览器收藏夹（持久化）：扁平列表，支持 Netscape HTML 书签导入/导出（Chrome/Safari 通用）。
@MainActor
final class BookmarkStore: ObservableObject {
    static let shared = BookmarkStore()

    struct Bookmark: Identifiable, Codable {
        let id: UUID
        var title: String
        var url: String
        var host: String
        var createdAt: Date
    }

    @Published private(set) var bookmarks: [Bookmark] = []

    private let storeURL: URL
    private var saveTask: Task<Void, Never>?
    private let maxBookmarks = 500

    private init() {
        let dir = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
            .appendingPathComponent("xhanHelper", isDirectory: true)
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
        return true
    }

    func remove(id: UUID) {
        bookmarks.removeAll { $0.id == id }
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

    func search(_ query: String) -> [Bookmark] {
        let q = query.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        guard !q.isEmpty else { return bookmarks }
        return bookmarks.filter {
            $0.title.lowercased().contains(q) || $0.url.lowercased().contains(q)
        }
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
        for b in bookmarks {
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
        guard bookmarks.count > maxBookmarks else { return }
        bookmarks.removeLast(bookmarks.count - maxBookmarks)
    }
}
