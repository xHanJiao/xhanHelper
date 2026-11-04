import Combine
import Foundation
import SwiftUI

@MainActor
class EventViewModel: ObservableObject {
    @Published var events: [AppEventThread] = []
    @Published var isLoading = false
    @Published var isLoadingMore = false
    @Published var errorMessage: String?

    @Published var detail: AppEventThreadDetail?
    @Published var isLoadingDetail = false

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
            let resp = try await apiClient.listEventThreads()
            events = resp.items
            hasMore = resp.items.count >= 50
        } catch {
            LogBuffer.shared.error("EventViewModel.load: \(error)")
            errorMessage = error.localizedDescription
        }
        isLoading = false
    }

    func loadMore() async {
        guard hasMore, !isLoadingMore else { return }
        isLoadingMore = true
        offset += 50
        do {
            let resp = try await apiClient.listEventThreads(offset: offset)
            events.append(contentsOf: resp.items)
            hasMore = resp.items.count >= 50
        } catch {
            LogBuffer.shared.error("EventViewModel.loadMore: \(error)")
            errorMessage = error.localizedDescription
        }
        isLoadingMore = false
    }

    func loadDetail(id: String) async {
        detail = nil
        isLoadingDetail = true
        do {
            detail = try await apiClient.getEventThread(id: id)
        } catch {
            LogBuffer.shared.error("EventViewModel.loadDetail: \(error)")
            errorMessage = error.localizedDescription
        }
        isLoadingDetail = false
    }
}
