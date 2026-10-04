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

    /// 数据根目录名（dev/prod 分环境）。**为什么在这里又一份**：本文件属于 SwiftPM 的
    /// LocalArtifactsCore target，不能再引 `LocalPaths`（跨 target 重复文件）。
    /// app 启动时 `LocalBackendManager` 会校验三者一致（不符 `fatalError`）。
    static var dataRootName: String {
        #if DEBUG
        "xhanHelper-dev"
        #else
        "xhanHelper"
        #endif
    }

    init(baseURL: URL? = nil) {
        if let baseURL {
            self.baseURL = baseURL
        } else {
            let appSupport = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask).first!
            self.baseURL = appSupport.appendingPathComponent(Self.dataRootName + "/local_results", isDirectory: true)
        }
    }

    private func fileURL(for convID: String) -> URL {
        baseURL.appendingPathComponent("\(convID).json")
    }

    func entries(for convID: String, from: String, to: String) throws -> [Entry] {
        let all = try loadAll(for: convID)
        // 按解析后的时间比，不按字符串：本地卡片是 UTC `Z`，服务端可能是 `+08:00`，
        // 字符串比较会差 8 小时把卡片误剔（详见 LocalArtifactTime）。
        return all.filter { LocalArtifactTime.isWithin($0.createdAt, from: from, to: to) }
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

    /// 删除某会话的本地产物（内存 + 磁盘）。用于清理历史遗留的空会话键文件等。
    func removeConversation(_ convID: String) throws {
        cache[convID] = nil
        let url = fileURL(for: convID)
        if FileManager.default.fileExists(atPath: url.path) {
            try FileManager.default.removeItem(at: url)
        }
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
