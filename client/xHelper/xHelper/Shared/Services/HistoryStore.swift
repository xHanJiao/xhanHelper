import Foundation
import Combine

/// 浏览器浏览历史（持久化）：自动记录访问过的 http/https 页面，同 URL 去重置顶。
@MainActor
final class HistoryStore: ObservableObject {
    static let shared = HistoryStore()

    struct HistoryEntry: Identifiable, Codable {
        let id: UUID
        var title: String
        var url: String
        var host: String
        var visitedAt: Date
    }

    @Published private(set) var entries: [HistoryEntry] = []

    private let storeURL: URL
    private var saveTask: Task<Void, Never>?
    private let maxEntries = 1000

    private init() {
        let dir = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
            .appendingPathComponent("xhanHelper", isDirectory: true)
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        storeURL = dir.appendingPathComponent("history.json")
        load()
    }

    private func load() {
        guard let data = try? Data(contentsOf: storeURL),
              let decoded = try? JSONDecoder().decode([HistoryEntry].self, from: data) else { return }
        entries = decoded
    }

    private func scheduleSave() {
        saveTask?.cancel()
        saveTask = Task { @MainActor [weak self] in
            try? await Task.sleep(for: .milliseconds(500))
            self?.save()
        }
    }

    private func save() {
        guard let data = try? JSONEncoder().encode(entries) else { return }
        try? data.write(to: storeURL, options: .atomic)
    }

    /// 记录一次访问：仅 http/https；同 URL 已存在则移除置顶（更新时间）。
    func record(title: String, url: String, host: String) {
        let trimmed = url.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let u = URL(string: trimmed),
              let scheme = u.scheme?.lowercased(),
              scheme == "http" || scheme == "https"
        else { return }
        let key = trimmed.lowercased()
        if let i = entries.firstIndex(where: { $0.url.lowercased() == key }) {
            entries.remove(at: i)
        }
        let h = host.isEmpty ? (u.host ?? trimmed) : host
        let t = title.trimmingCharacters(in: .whitespacesAndNewlines)
        entries.insert(HistoryEntry(id: UUID(), title: t.isEmpty ? h : t, url: trimmed, host: h, visitedAt: Date()), at: 0)
        trim()
        scheduleSave()
    }

    /// 地址栏建议：按输入前缀匹配历史 URL（兼容有无协议头），去重置顶、按最近访问排序。
    /// - Parameter query: 用户输入（可能含 http:// / https://，也可能没有）
    /// - Parameter maxResults: 最多返回条数
    func suggest(query: String, maxResults: Int = 6) -> [HistoryEntry] {
        let normalized = Self.normalizeURL(query)
        guard !normalized.isEmpty else { return [] }
        let raw = query.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        var seen: Set<String> = []
        var results: [HistoryEntry] = []
        for entry in entries {
            let key = entry.url.lowercased()
            if seen.contains(key) { continue }
            // 匹配完整 URL（用户输入了协议头），或归一化 URL（无协议头/带协议头均兼容）
            let matches = key.hasPrefix(raw) || Self.normalizeURL(entry.url).hasPrefix(normalized)
            guard matches else { continue }
            seen.insert(key)
            results.append(entry)
            if results.count >= maxResults { break }
        }
        return results
    }

    /// 去掉 http:// / https:// 协议头、www. 前缀与末尾斜杠，供建议匹配统一比较。
    static func normalizeURL(_ url: String) -> String {
        var u = url.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        if u.hasPrefix("https://") {
            u = String(u.dropFirst(8))
        } else if u.hasPrefix("http://") {
            u = String(u.dropFirst(7))
        }
        if u.hasPrefix("www.") {
            u = String(u.dropFirst(4))
        }
        while u.hasSuffix("/") {
            u = String(u.dropLast())
        }
        return u
    }

    func remove(id: UUID) {
        entries.removeAll { $0.id == id }
        scheduleSave()
    }

    func clear() {
        entries.removeAll()
        scheduleSave()
    }

    private func trim() {
        guard entries.count > maxEntries else { return }
        entries.removeLast(entries.count - maxEntries)
    }
}
