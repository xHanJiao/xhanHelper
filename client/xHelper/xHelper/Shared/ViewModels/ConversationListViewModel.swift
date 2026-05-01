import Combine
import Foundation

@MainActor
class ConversationListViewModel: ObservableObject {
    @Published var conversations: [Conversation] = []
    @Published var isLoading = false
    @Published var isUnauthorized = false

    private let apiClient: APIClient

    init(apiClient: APIClient) {
        self.apiClient = apiClient
    }

    func load() async {
        isLoading = true
        defer { isLoading = false }
        do {
            conversations = try await apiClient.listConversations()
        } catch {
            let nsErr = error as NSError
            if nsErr.code == 401 {
                isUnauthorized = true
            }
        }
    }

    func rename(id: String, title: String) async {
        do {
            try await apiClient.renameConversation(id: id, title: title)
            if let idx = conversations.firstIndex(where: { $0.id == id }) {
                conversations[idx] = Conversation(
                    id: conversations[idx].id,
                    deviceId: conversations[idx].deviceId,
                    title: title,
                    createdAt: conversations[idx].createdAt,
                    updatedAt: conversations[idx].updatedAt
                )
            }
        } catch {
            // 静默失败
        }
    }

    func delete(id: String) async {
        do {
            try await apiClient.deleteConversation(id: id)
            conversations.removeAll { $0.id == id }
        } catch {
            // 静默失败
        }
    }
}
