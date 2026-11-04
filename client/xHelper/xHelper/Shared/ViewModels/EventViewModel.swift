import Combine
import Foundation
import SwiftUI

@MainActor
class EventViewModel: ObservableObject {
    @Published var events: [AppEventThread] = []
    @Published var isLoading = false
    @Published var isLoadingMore = false
    /// 只承载列表错误：PageContainer 会据此整页换成错误态，分页/详情失败不能写这里。
    @Published var errorMessage: String?

    @Published var detail: AppEventThreadDetail?
    @Published var isLoadingDetail = false
    @Published var detailErrorMessage: String?

    private let apiClient: APIClient
    private var hasMore = true

    init(apiClient: APIClient) {
        self.apiClient = apiClient
    }

    func load() async {
        isLoading = true
        errorMessage = nil
        hasMore = true
        do {
            let resp = try await apiClient.listEventThreads()
            events = resp.items
            hasMore = resp.items.count >= 50
        } catch {
            if Task.isCancelled { isLoading = false; return }
            LogBuffer.shared.error("EventViewModel.load: \(error)")
            errorMessage = error.localizedDescription
        }
        isLoading = false
    }

    func loadMore() async {
        guard hasMore, !isLoadingMore else { return }
        isLoadingMore = true
        defer { isLoadingMore = false }
        do {
            // 用当前条数当 offset：失败不推进，避免下次跳过一整页。
            let resp = try await apiClient.listEventThreads(offset: events.count)
            events.append(contentsOf: resp.items)
            hasMore = resp.items.count >= 50
        } catch {
            if Task.isCancelled { return }
            // 分页失败只记日志，不把已经显示的列表整页打掉。
            LogBuffer.shared.error("EventViewModel.loadMore: \(error)")
        }
    }

    func loadDetail(id: String) async {
        detail = nil
        detailErrorMessage = nil
        isLoadingDetail = true
        defer { isLoadingDetail = false }
        do {
            detail = try await apiClient.getEventThread(id: id)
        } catch {
            if Task.isCancelled { return }
            LogBuffer.shared.error("EventViewModel.loadDetail: \(error)")
            detailErrorMessage = error.localizedDescription
        }
    }
}
