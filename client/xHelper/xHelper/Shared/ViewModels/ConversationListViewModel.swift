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
            let resp = try await apiClient.listConversations()
            conversations = resp.conversations
        } catch {
            let nsErr = error as NSError
            if nsErr.code == 401 {
                isUnauthorized = true
            }
            LogBuffer.shared.error("ConversationListViewModel.load: \(error)")
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
        do {
            try await apiClient.deleteConversation(id: id)
            conversations.removeAll { $0.id == id }
        } catch {
            LogBuffer.shared.error("ConversationListViewModel.delete: \(error)")
        }
    }
}
