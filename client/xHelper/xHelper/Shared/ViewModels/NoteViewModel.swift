import Combine
import Foundation
import SwiftUI

@MainActor
class NoteViewModel: ObservableObject {
    @Published var notes: [Note] = []
    @Published var isLoading = false
    @Published var isLoadingMore = false
    @Published var errorMessage: String?
    @Published var searchKeyword = ""

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
            let resp = try await apiClient.listNotes(keyword: searchKeyword)
            notes = resp.notes
            hasMore = resp.notes.count >= 50
        } catch {
            LogBuffer.shared.error("NoteViewModel.load: \(error)")
            errorMessage = error.localizedDescription
        }
        isLoading = false
    }

    func loadMore() async {
        guard hasMore, !isLoadingMore else { return }
        isLoadingMore = true
        offset += 50
        do {
            let resp = try await apiClient.listNotes(offset: offset, keyword: searchKeyword)
            notes.append(contentsOf: resp.notes)
            hasMore = resp.notes.count >= 50
        } catch {
            LogBuffer.shared.error("NoteViewModel.loadMore: \(error)")
            errorMessage = error.localizedDescription
        }
        isLoadingMore = false
    }
}
