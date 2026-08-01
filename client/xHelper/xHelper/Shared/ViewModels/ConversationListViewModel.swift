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
    /// 默认会话不可删除，由 ContentView 注入
    var defaultConversationID: String?

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
    }

    func load() async {
        isLoading = true
        defer { isLoading = false }
        do {
            let resp = try await apiClient.listConversations(limit: pageSize, offset: 0)
            conversations = resp.conversations
            offset = resp.conversations.count
            hasMore = offset < resp.total
            hasLoadedOnce = true
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
                    createdAt: old.createdAt,
                    updatedAt: old.updatedAt
                )
            }
        } catch {
            LogBuffer.shared.error("ConversationListViewModel.rename: \(error)")
        }
    }

    func delete(id: String) async {
        guard id != defaultConversationID else {
            LogBuffer.shared.warning("ConversationListViewModel.delete: default conversation is protected, id=\(id)")
            return
        }
        do {
            try await apiClient.deleteConversation(id: id)
            conversations.removeAll { $0.id == id }
        } catch {
            LogBuffer.shared.error("ConversationListViewModel.delete: \(error)")
        }
    }
}
