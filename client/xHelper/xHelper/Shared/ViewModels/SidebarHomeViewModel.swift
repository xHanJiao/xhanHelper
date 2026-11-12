import Combine
import Foundation

/// 右栏首页六个入口卡片 + 今日行 + 最近页脚的数据。
///
/// 全部走**已有接口**（各 `limit=1/3`），一次并发六路；不新增后端契约。
/// 见 `docs/design/right-sidebar-home/design.md` §6。
@MainActor
final class SidebarHomeViewModel: ObservableObject {

    /// 最近页脚的一条（文档 / 灵阅混排）。
    struct RecentItem: Identifiable, Equatable {
        /// 点开时走哪条链路（不要用展示文案判断，改文案会静默点错）
        enum Kind: Equatable {
            case document
            case artifact

            var label: String {
                switch self {
                case .document: return "文档"
                case .artifact: return "灵阅"
                }
            }
        }

        let id: String
        /// 原始对象 id（点开时用）
        let targetID: String
        let title: String
        let kind: Kind
        /// 已格式化的相对时间
        let time: String
    }

    struct Stats: Equatable {
        var pendingReminders: Int?
        var events: Int?
        var eventsUpdatedText: String?
        var monthSpend: Double?
        var monthBillCount: Int?
        var memories: Int?
        var memoriesCreatedText: String?
        var documents: Int?
        var documentsUpdatedText: String?
        var artifacts: Int?
        var artifactsCreatedText: String?
        var recent: [RecentItem] = []
    }

    @Published private(set) var stats = Stats()
    @Published private(set) var isLoading = false

    /// 缓存时间戳：60 秒内不重复打接口（首页会被反复进出）。
    private var loadedAt: Date?
    private static let cacheTTL: TimeInterval = 60

    /// 六路并发拉取。`force` 用于用户显式刷新（当前首页没有刷新按钮，留给以后）。
    func refresh(baseURL: String, token: String, force: Bool = false) async {
        guard !baseURL.isEmpty, !token.isEmpty else { return }
        guard !isLoading else { return }
        if !force, let loadedAt, Date().timeIntervalSince(loadedAt) < Self.cacheTTL { return }

        isLoading = true
        defer { isLoading = false }

        let api = APIClient(baseURL: baseURL, token: token)
        let now = Date()
        let start = SidebarHomeFormat.dateParam(SidebarHomeFormat.monthStart(of: now))
        let end = SidebarHomeFormat.dateParam(now)

        async let reminders = attempt("reminders") {
            try await api.listReminders(status: "pending", limit: 1, offset: 0)
        }
        async let events = attempt("events") {
            try await api.listEventThreads(limit: 1, offset: 0)
        }
        async let bills = attempt("bills") {
            try await api.fetchBillStats(startDate: start, endDate: end)
        }
        async let memories = attempt("memories") {
            try await api.listMemories(limit: 1, offset: 0)
        }
        async let documents = attempt("documents") {
            try await api.listDocuments(limit: 3, offset: 0)
        }
        async let artifacts = attempt("artifacts") {
            try await api.listArtifacts(limit: 3, offset: 0)
        }

        let (r, e, b, m, d, a) = await (reminders, events, bills, memories, documents, artifacts)
        // 视图被摘除（切去面板/关右栏）会取消本次 .task：保留上一次的数字，别用 nil 把它刷成一片「—」。
        guard !Task.isCancelled else { return }

        var next = Stats()
        next.pendingReminders = r?.total
        next.events = e?.total
        next.eventsUpdatedText = e?.items.first.map {
            SidebarHomeFormat.relativeTime(LocalArtifactTime.parse($0.updatedAt) ?? now)
        }
        next.monthSpend = b?.sum
        next.monthBillCount = b?.count
        next.memories = m?.total
        next.memoriesCreatedText = m?.memories.first.flatMap {
            LocalArtifactTime.parse($0.createdAt).map { SidebarHomeFormat.relativeTime($0) }
        }
        next.documents = d?.total
        next.documentsUpdatedText = d?.documents.first.flatMap {
            LocalArtifactTime.parse($0.updatedAt).map { SidebarHomeFormat.relativeTime($0) }
        }
        next.artifacts = a?.total
        next.artifactsCreatedText = a?.items.first.flatMap {
            LocalArtifactTime.parse($0.createdAt).map { SidebarHomeFormat.relativeTime($0) }
        }
        next.recent = Self.mergeRecent(documents: d?.documents ?? [], artifacts: a?.items ?? [])

        stats = next
        // 六路全挂（多半是没登录/断网）不写入缓存，下次进来立刻重试。
        if r != nil || e != nil || b != nil || m != nil || d != nil || a != nil {
            loadedAt = Date()
        }
    }

    /// 文档（updated_at 倒序）与灵阅（created_at 倒序）合并取最近三条。
    private static func mergeRecent(documents: [Document], artifacts: [ArtifactSummary]) -> [RecentItem] {
        struct Entry {
            let item: RecentItem
            let date: Date
        }
        var entries: [Entry] = []
        for doc in documents {
            guard let date = LocalArtifactTime.parse(doc.updatedAt) else { continue }
            entries.append(Entry(item: RecentItem(
                id: "doc:" + doc.id,
                targetID: doc.id,
                title: doc.title,
                kind: .document,
                time: SidebarHomeFormat.relativeTime(date)
            ), date: date))
        }
        for artifact in artifacts {
            guard let date = LocalArtifactTime.parse(artifact.createdAt) else { continue }
            entries.append(Entry(item: RecentItem(
                id: "artifact:" + artifact.id,
                targetID: artifact.id,
                title: artifact.title,
                kind: .artifact,
                time: SidebarHomeFormat.relativeTime(date)
            ), date: date))
        }
        return entries.sorted { $0.date > $1.date }.prefix(3).map(\.item)
    }

    /// 单路请求失败只影响对应数字（显示 `—`），记一条诊断日志，不打扰用户。
    private func attempt<T>(_ label: String, _ work: () async throws -> T) async -> T? {
        do {
            return try await work()
        } catch {
            // 取消（视图被摘除）不是失败，不记日志
            if Task.isCancelled || error is CancellationError { return nil }
            LogBuffer.shared.diag("sidebar home \(label) failed: \(error.localizedDescription)")
            return nil
        }
    }
}
