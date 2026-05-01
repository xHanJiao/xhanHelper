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
}
