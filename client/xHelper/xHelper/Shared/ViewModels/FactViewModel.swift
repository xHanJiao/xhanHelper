import Combine
import Foundation
import SwiftUI

/// 事实（facts）列表。只读：后端决策段写入，这里只负责分页拉取。
@MainActor
class FactViewModel: ObservableObject {
    @Published var facts: [AppFact] = []
    @Published var isLoading = false
    @Published var isLoadingMore = false
    @Published var errorMessage: String?

    private let apiClient: APIClient
    private var offset = 0
    private var hasMore = true

    init(apiClient: APIClient) {
        self.apiClient = apiClient
    }

    func load() async {
        isLoading = true
        offset = 0
        hasMore = true
        do {
            let resp = try await apiClient.listFacts()
            facts = resp.items
            hasMore = resp.items.count >= 50
        } catch {
            LogBuffer.shared.error("FactViewModel.load: \(error)")
            errorMessage = error.localizedDescription
        }
        isLoading = false
    }

    func loadMore() async {
        guard hasMore, !isLoadingMore else { return }
        isLoadingMore = true
        offset += 50
        do {
            let resp = try await apiClient.listFacts(offset: offset)
            facts.append(contentsOf: resp.items)
            hasMore = resp.items.count >= 50
        } catch {
            LogBuffer.shared.error("FactViewModel.loadMore: \(error)")
            errorMessage = error.localizedDescription
        }
        isLoadingMore = false
    }
}
