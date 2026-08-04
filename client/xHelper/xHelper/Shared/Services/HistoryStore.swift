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
