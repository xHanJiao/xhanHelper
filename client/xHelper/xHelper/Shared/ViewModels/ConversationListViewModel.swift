import Combine
import Foundation

@MainActor
class ConversationListViewModel: ObservableObject {
    @Published var conversations: [Conversation] = []
    @Published var isLoading = false
    @Published var isLoadingMore = false
    @Published var hasMore = true
    @Published var isUnauthorized = false
    private(set) var hasLoadedOnce = false

    private let apiClient: APIClient
    private var offset = 0
    private let pageSize = 20
    private var cancellables: Set<AnyCancellable> = []

    init(apiClient: APIClient) {
        self.apiClient = apiClient
        NotificationCenter.default.publisher(for: .conversationListNeedsRefresh)
            .receive(on: DispatchQueue.main)
            .sink { [weak self] _ in
                Task { @MainActor in
                    await self?.reload()
                }
            }
            .store(in: &cancellables)

        // 单个会话在服务端消失（如无痕销毁）：本地移除，保留分页
        NotificationCenter.default.publisher(for: .conversationRemoved)
            .receive(on: DispatchQueue.main)
            .sink { [weak self] note in
                guard let id = note.userInfo?["id"] as? String, !id.isEmpty else { return }
                Task { @MainActor in
                    self?.removeLocal(id: id)
                }
            }
            .store(in: &cancellables)
    }

    /// 本地移除指定会话（服务端已销毁时用；保留分页与其余条目）。
    func removeLocal(id: String) {
        conversations.removeAll { $0.id == id }
    }

    // MARK: - 前台周期刷新

    /// 前台自动刷新间隔：销毁窗口是 30 分钟，60s 延迟无感。
    static let autoRefreshInterval: TimeInterval = 60
    private var autoRefreshTask: Task<Void, Never>?

    /// 应用激活时启动：周期全量重载列表，清掉服务端已销毁（如无痕）的会话。
    /// 重复调用无副作用（已有任务时直接返回）。
    func startAutoRefresh() {
        guard autoRefreshTask == nil else { return }
        autoRefreshTask = Task { @MainActor [weak self] in
            while !Task.isCancelled {
                try? await Task.sleep(nanoseconds: UInt64(Self.autoRefreshInterval * 1_000_000_000))
                if Task.isCancelled { break }
                guard let self else { return }
                if self.isUnauthorized { continue }
                await self.reload()
            }
        }
    }

    /// 应用离开激活态时停止，避免后台空转。
    func stopAutoRefresh() {
        autoRefreshTask?.cancel()
        autoRefreshTask = nil
    }

    func load() async {
        isLoading = true
        defer { isLoading = false }
        do {
            let resp = try await apiClient.listConversations(limit: pageSize, offset: 0)
            let liveIDs = Set(resp.conversations.map { $0.id })
            // 无痕会话只会被服务端销毁：本地曾见、现不在首页 → 已销毁。
            // 只判无痕：load 只拉第 1 页，用全量差集会把已翻页的条目误判为已删。
            let gone = conversations
                .filter { $0.ephemeral == true && !liveIDs.contains($0.id) }
                .map { $0.id }
            conversations = resp.conversations
            offset = resp.conversations.count
            hasMore = offset < resp.total
            hasLoadedOnce = true
            if !gone.isEmpty {
                NotificationCenter.default.post(name: .conversationsGone, object: nil, userInfo: ["ids": gone])
            }
        } catch {
            let nsErr = error as NSError
            if nsErr.code == 401 {
                isUnauthorized = true
            }
            LogBuffer.shared.error("ConversationListViewModel.load: \(error)")
        }
    }

    func reload() async {
        await load()
    }

    func loadMore() async {
        guard !isLoadingMore, hasMore, !isLoading else { return }
        isLoadingMore = true
        defer { isLoadingMore = false }
        do {
            let resp = try await apiClient.listConversations(limit: pageSize, offset: offset)
            conversations.append(contentsOf: resp.conversations)
            offset += resp.conversations.count
            hasMore = offset < resp.total
        } catch {
            LogBuffer.shared.error("ConversationListViewModel.loadMore: \(error)")
        }
    }

    func rename(id: String, title: String) async {
        do {
            try await apiClient.renameConversation(id: id, title: title)
            if let idx = conversations.firstIndex(where: { $0.id == id }) {
                let old = conversations[idx]
                conversations[idx] = Conversation(
                    id: old.id,
                    userId: old.userId,
                    title: title,
                    agentId: old.agentId,
                    agentName: old.agentName,
                    mode: old.mode,
                    ephemeral: old.ephemeral,
                    pinned: old.pinned,
                    createdAt: old.createdAt,
                    updatedAt: old.updatedAt
                )
            }
        } catch {
            LogBuffer.shared.error("ConversationListViewModel.rename: \(error)")
        }
    }

    /// 置顶/取消置顶。服务端按 pinned_at 排序，成功后整页重载以对齐顺序。
    func setPinned(id: String, pinned: Bool) async {
        do {
            try await apiClient.setConversationPinned(id: id, pinned: pinned)
            await reload()
        } catch {
            LogBuffer.shared.error("ConversationListViewModel.setPinned: \(error)")
        }
    }

    func delete(id: String) async {
        do {
            try await apiClient.deleteConversation(id: id)
            conversations.removeAll { $0.id == id }
        } catch {
            LogBuffer.shared.error("ConversationListViewModel.delete: \(error)")
        }
    }
}

/// 会话时间戳 → 相对时间（中文）。归档/文档列表与顶栏会话下拉共用。
func formatConversationDate(_ iso: String) -> String {
    let fmt = ISO8601DateFormatter()
    fmt.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
    guard let date = fmt.date(from: iso) ?? ISO8601DateFormatter().date(from: iso) else {
        return iso
    }
    let relative = RelativeDateTimeFormatter()
    relative.locale = Locale(identifier: "zh_CN")
    return relative.localizedString(for: date, relativeTo: Date())
}
