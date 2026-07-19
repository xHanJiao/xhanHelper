import Foundation

actor ChartStore {
    static let shared = ChartStore()

    private var charts: [String: [ChartMessageEntry]] = [:]
    private let baseURL: URL

    struct ChartMessageEntry: Codable {
        let id: String
        let createdAt: String
        let chartData: ChartData
        let afterMessageID: String?

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

    private init() {
        let appSupport = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask).first!
        baseURL = appSupport.appendingPathComponent("xhanHelper/charts", isDirectory: true)
        try? FileManager.default.createDirectory(at: baseURL, withIntermediateDirectories: true)
    }

    private func fileURL(for conversationID: String) -> URL {
        baseURL.appendingPathComponent("\(conversationID).json")
    }

    func entries(for conversationID: String) -> [ChartMessageEntry] {
        if let cached = charts[conversationID] {
            return cached
        }
        guard let data = try? Data(contentsOf: fileURL(for: conversationID)),
              let entries = try? JSONDecoder().decode([ChartMessageEntry].self, from: data) else {
            return []
        }
        charts[conversationID] = entries
        return entries
    }

    func save(chartData: ChartData, message: Message, afterMessageID: String?) {
        let entry = ChartMessageEntry(id: message.id, createdAt: message.createdAt, chartData: chartData, afterMessageID: afterMessageID)
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

