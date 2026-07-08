import Combine
import Foundation
import SwiftUI

@MainActor
class MemoryViewModel: ObservableObject {
    @Published var memories: [UserMemory] = []
    @Published var isLoading = false
    @Published var errorMessage: String?
    @Published var isEditMode = false
    @Published var selectedIDs: Set<String> = []

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
            let resp = try await apiClient.listMemories(keyword: searchKeyword)
            memories = resp.memories
            hasMore = resp.total >= 50
        } catch {
            LogBuffer.shared.error("MemoryViewModel.load: \(error)")
            errorMessage = error.localizedDescription
        }
        isLoading = false
    }

    func loadMore() async {
        guard hasMore, !isLoading else { return }
        isLoading = true
        currentOffset += 50
        do {
            let resp = try await apiClient.listMemories(offset: currentOffset, keyword: searchKeyword)
            memories.append(contentsOf: resp.memories)
            hasMore = resp.total >= 50
        } catch {
            LogBuffer.shared.error("MemoryViewModel.loadMore: \(error)")
            errorMessage = error.localizedDescription
        }
        isLoading = false
    }

    func delete(id: String) async {
        guard let removed = memories.first(where: { $0.id == id }) else { return }
        withAnimation { memories.removeAll { $0.id == id } }
        do {
            try await apiClient.deleteMemory(id: id)
        } catch {
            LogBuffer.shared.error("MemoryViewModel.delete: \(error)")
            withAnimation { memories.append(removed) }
            errorMessage = error.localizedDescription
        }
    }

    func toggleSelection(_ id: String) {
        if selectedIDs.contains(id) {
            selectedIDs.remove(id)
        } else {
            selectedIDs.insert(id)
        }
    }

    func batchDelete() async {
        let ids = Array(selectedIDs)
        guard !ids.isEmpty else { return }
        withAnimation { memories.removeAll { selectedIDs.contains($0.id) } }
        isEditMode = false
        selectedIDs = []
        do {
            try await apiClient.batchDeleteMemories(ids: ids)
        } catch {
            LogBuffer.shared.error("MemoryViewModel.batchDelete: \(error)")
            errorMessage = error.localizedDescription
            await load()
        }
    }
}
