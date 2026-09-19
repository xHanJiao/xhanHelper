import Foundation

/// 本地产物（chart / 文档卡片…）的按会话持久化缓存。
///
/// 纯 Foundation：不依赖 SwiftUI/Message，便于 SwiftPM 单测（`init(baseURL:)` 注入临时目录）。
/// 所有 I/O 错误**向上抛出**，由 app 层调用方记录一次——不在这里静默吞。
actor LocalToolResultStore {
    static let shared = LocalToolResultStore()

    private var cache: [String: [Entry]] = [:]
    private let baseURL: URL

    struct Entry: Codable {
        let id: String
        let createdAt: String
        let type: LocalToolResultType
        let payload: Data
    }

    init(baseURL: URL? = nil) {
        if let baseURL {
            self.baseURL = baseURL
        } else {
            let appSupport = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask).first!
            self.baseURL = appSupport.appendingPathComponent("xhanHelper/local_results", isDirectory: true)
        }
    }

    private func fileURL(for convID: String) -> URL {
        baseURL.appendingPathComponent("\(convID).json")
    }

    func entries(for convID: String, from: String, to: String) throws -> [Entry] {
        let all = try loadAll(for: convID)
        return all.filter { $0.createdAt >= from && $0.createdAt <= to }
    }

    func upsert(type: LocalToolResultType, payload: Data, id: String, createdAt: String, conversationId: String) throws {
        var entries = try loadAll(for: conversationId)
        let entry = Entry(id: id, createdAt: createdAt, type: type, payload: payload)
        if let idx = entries.firstIndex(where: { $0.id == id }) {
            entries[idx] = entry
        } else {
            entries.append(entry)
        }
        cache[conversationId] = entries
        try persist(convID: conversationId, entries: entries)
    }

    func clearAll() throws {
        cache.removeAll()
        try FileManager.default.removeItem(at: baseURL)
        try FileManager.default.createDirectory(at: baseURL, withIntermediateDirectories: true)
    }

    private func loadAll(for convID: String) throws -> [Entry] {
        if let cached = cache[convID] { return cached }
        let url = fileURL(for: convID)
        guard FileManager.default.fileExists(atPath: url.path) else { return [] }
        let data = try Data(contentsOf: url)
        let entries = try JSONDecoder().decode([Entry].self, from: data)
        cache[convID] = entries
        return entries
    }

    private func persist(convID: String, entries: [Entry]) throws {
        try FileManager.default.createDirectory(at: baseURL, withIntermediateDirectories: true)
        let data = try JSONEncoder().encode(entries)
        try data.write(to: fileURL(for: convID), options: .atomic)
    }
}
