import Foundation

actor ChartStore {
    static let shared = ChartStore()

    private var charts: [String: [ChartMessageEntry]] = [:]
    private let baseURL: URL

    private struct ChartMessageEntry: Codable {
        let id: String
        let createdAt: String
        let chartData: ChartData
    }

    private init() {
        let appSupport = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask).first!
        baseURL = appSupport.appendingPathComponent("xhanHelper/charts", isDirectory: true)
        try? FileManager.default.createDirectory(at: baseURL, withIntermediateDirectories: true)
    }

    private func fileURL(for conversationID: String) -> URL {
        baseURL.appendingPathComponent("\(conversationID).json")
    }

    func load(for conversationID: String) -> [Message] {
        if let cached = charts[conversationID] {
            return cached.map { $0.asMessage(conversationID: conversationID) }
        }
        guard let data = try? Data(contentsOf: fileURL(for: conversationID)),
              let entries = try? JSONDecoder().decode([ChartMessageEntry].self, from: data) else {
            return []
        }
        charts[conversationID] = entries
        return entries.map { $0.asMessage(conversationID: conversationID) }
    }

    func save(chartData: ChartData, message: Message) {
        let entry = ChartMessageEntry(id: message.id, createdAt: message.createdAt, chartData: chartData)
        var entries = charts[message.conversationId] ?? []
        entries.append(entry)
        charts[message.conversationId] = entries
        persist(conversationID: message.conversationId, entries: entries)
    }

    private func persist(conversationID: String, entries: [ChartMessageEntry]) {
        guard let data = try? JSONEncoder().encode(entries) else { return }
        try? data.write(to: fileURL(for: conversationID), options: .atomic)
    }

    func clearAll() {
        charts.removeAll()
        let rootURL = baseURL.deletingLastPathComponent()
        try? FileManager.default.removeItem(at: rootURL)
        try? FileManager.default.createDirectory(at: baseURL, withIntermediateDirectories: true)
    }
}

private extension ChartStore.ChartMessageEntry {
    func asMessage(conversationID: String) -> Message {
        var msg = Message(
            id: id,
            conversationId: conversationID,
            role: "chart",
            content: "",
            toolCallsJSON: nil,
            createdAt: createdAt
        )
        msg.chartData = chartData
        return msg
    }
}
