import Combine
import Foundation
import SwiftUI

@MainActor
class MemoryViewModel: ObservableObject {
    @Published var memories: [UserMemory] = []
    @Published var isLoading = false
    @Published var errorMessage: String?

    private let apiClient: APIClient
    private var currentOffset = 0
    private var hasMore = true
    var searchKeyword = ""

    init(apiClient: APIClient) {
        self.apiClient = apiClient
    }

    func load() async {
        isLoading = true
        currentOffset = 0
        hasMore = true
        do {
            memories = try await apiClient.listMemories(keyword: searchKeyword)
            hasMore = memories.count >= 50
        } catch {
            errorMessage = error.localizedDescription
        }
        isLoading = false
    }

    func loadMore() async {
        guard hasMore, !isLoading else { return }
        isLoading = true
        currentOffset += 50
        do {
            let more = try await apiClient.listMemories(offset: currentOffset, keyword: searchKeyword)
            memories.append(contentsOf: more)
            hasMore = more.count >= 50
        } catch {
            errorMessage = error.localizedDescription
        }
        isLoading = false
    }

    func delete(id: String) async {
        do {
            try await apiClient.deleteMemory(id: id)
            memories.removeAll { $0.id == id }
        } catch {
            errorMessage = error.localizedDescription
        }
    }
}
