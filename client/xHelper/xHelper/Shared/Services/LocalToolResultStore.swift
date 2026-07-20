import Foundation

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

    private init() {
        let appSupport = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask).first!
        baseURL = appSupport.appendingPathComponent("xhanHelper/local_results", isDirectory: true)
        try? FileManager.default.createDirectory(at: baseURL, withIntermediateDirectories: true)
    }

    private func fileURL(for convID: String) -> URL {
        baseURL.appendingPathComponent("\(convID).json")
    }

    func entries(for convID: String, from: String, to: String) -> [Entry] {
        let all = loadAll(for: convID)
        return all.filter { $0.createdAt >= from && $0.createdAt <= to }
    }

    func upsert(type: LocalToolResultType, payload: Data, message: Message) {
        var entries = loadAll(for: message.conversationId)
        if let idx = entries.firstIndex(where: { $0.id == message.id }) {
            entries[idx] = Entry(id: message.id, createdAt: message.createdAt, type: type, payload: payload)
        } else {
            entries.append(Entry(id: message.id, createdAt: message.createdAt, type: type, payload: payload))
        }
        cache[message.conversationId] = entries
        persist(convID: message.conversationId, entries: entries)
    }

    func clearAll() {
        cache.removeAll()
        try? FileManager.default.removeItem(at: baseURL)
        try? FileManager.default.createDirectory(at: baseURL, withIntermediateDirectories: true)
    }

    private func loadAll(for convID: String) -> [Entry] {
        if let cached = cache[convID] { return cached }
        guard let data = try? Data(contentsOf: fileURL(for: convID)),
              let entries = try? JSONDecoder().decode([Entry].self, from: data) else { return [] }
        cache[convID] = entries
        return entries
    }

    private func persist(convID: String, entries: [Entry]) {
        guard let data = try? JSONEncoder().encode(entries) else { return }
        try? data.write(to: fileURL(for: convID), options: .atomic)
    }
}
