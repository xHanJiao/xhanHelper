import Combine
import Foundation
import SwiftUI

@MainActor
class DocumentViewModel: ObservableObject {
    @Published var documents: [Document] = []
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
            let resp = try await apiClient.listDocuments()
            documents = resp.documents
            hasMore = resp.documents.count >= 24
        } catch {
            LogBuffer.shared.error("DocumentViewModel.load: \(error)")
            errorMessage = error.localizedDescription
        }
        isLoading = false
    }

    func loadMore() async {
        guard hasMore, !isLoadingMore else { return }
        isLoadingMore = true
        offset += 24
        do {
            let resp = try await apiClient.listDocuments(offset: offset)
            documents.append(contentsOf: resp.documents)
            hasMore = resp.documents.count >= 24
        } catch {
            LogBuffer.shared.error("DocumentViewModel.loadMore: \(error)")
            errorMessage = error.localizedDescription
        }
        isLoadingMore = false
    }
}
