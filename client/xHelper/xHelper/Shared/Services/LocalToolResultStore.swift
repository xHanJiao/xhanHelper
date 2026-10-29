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
        /// 产生这张卡的那一轮 user 消息 id —— 归位用的**位置锚点**，不再靠时间排（见 LocalArtifactMerge）。
        /// 旧数据没有这个键 → nil，加载时回退「按时间排序 + 位置归位」。
        let anchorUserID: String?
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

    /// 会话的全部本地产物。**不按时间窗口过滤**：卡片的 `createdAt` 是客户端时钟，
    /// 消息时间是服务端时钟（本机实测差 ~44s），拿上界去比会把卡片误剔（刷新即消失，见 docs/bugs/040）。
    /// 「哪些该显示」交给加载方按锚点/下界判定。
    func allEntries(for convID: String) throws -> [Entry] {
        try loadAll(for: convID)
    }

    func upsert(
        type: LocalToolResultType,
        payload: Data,
        id: String,
        createdAt: String,
        conversationId: String,
        anchorUserID: String? = nil
    ) throws {
        var entries = try loadAll(for: conversationId)
        let entry = Entry(id: id, createdAt: createdAt, type: type, payload: payload, anchorUserID: anchorUserID)
        if let idx = entries.firstIndex(where: { $0.id == id }) {
            entries[idx] = entry
        } else {
            entries.append(entry)
        }
        cache[conversationId] = entries
        try persist(convID: conversationId, entries: entries)
    }

    /// 一轮结束时 user 消息 id 从本地 UUID 换成服务端 id：把本轮卡片的锚点一起改写。
    /// 不改写的话重载后锚点永远匹配不上，只能退回按时间归位（会被时钟偏移带偏）。
    func reattachAnchor(conversationId: String, from old: String, to new: String) throws {
        var entries = try loadAll(for: conversationId)
        guard entries.contains(where: { $0.anchorUserID == old }) else { return }
        entries = entries.map {
            guard $0.anchorUserID == old else { return $0 }
            return Entry(id: $0.id, createdAt: $0.createdAt, type: $0.type,
                         payload: $0.payload, anchorUserID: new)
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
