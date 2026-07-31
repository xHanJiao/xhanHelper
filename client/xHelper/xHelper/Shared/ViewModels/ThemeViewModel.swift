import Combine
import Foundation
import SwiftUI

@MainActor
class ThemeViewModel: ObservableObject {
    @Published var themes: [AppTheme] = []
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
            let resp = try await apiClient.listThemes()
            themes = resp.items
            hasMore = resp.items.count >= 50
        } catch {
            LogBuffer.shared.error("ThemeViewModel.load: \(error)")
            errorMessage = error.localizedDescription
        }
        isLoading = false
    }

    func loadMore() async {
        guard hasMore, !isLoadingMore else { return }
        isLoadingMore = true
        offset += 50
        do {
            let resp = try await apiClient.listThemes(offset: offset)
            themes.append(contentsOf: resp.items)
            hasMore = resp.items.count >= 50
        } catch {
            LogBuffer.shared.error("ThemeViewModel.loadMore: \(error)")
            errorMessage = error.localizedDescription
        }
        isLoadingMore = false
    }
}
