import Combine
import Foundation
import SwiftUI

@MainActor
class BillViewModel: ObservableObject {
    @Published var bills: [Bill] = []
    @Published var isLoading = false
    @Published var isLoadingMore = false
    @Published var errorMessage: String?
    @Published var filterCategory = ""
    @Published var filterStartDate = ""
    @Published var filterEndDate = ""
    @Published var keyword = ""

    private let apiClient: APIClient
    private var offset = 0
    @Published var hasMore = true

    init(apiClient: APIClient) {
        self.apiClient = apiClient
    }

    func search(_ kw: String) async {
        keyword = kw
        await load()
    }

    func load() async {
        isLoading = true
        errorMessage = nil
        offset = 0
        hasMore = true
        do {
            let resp = try await apiClient.getBills(
                category: filterCategory,
                startDate: filterStartDate,
                endDate: filterEndDate,
                keyword: keyword
            )
            bills = resp.bills
            hasMore = resp.bills.count >= 50
        } catch {
            LogBuffer.shared.error("BillViewModel.load: \(error)")
            errorMessage = error.localizedDescription
        }
        isLoading = false
    }

    func loadMore() async {
        guard hasMore, !isLoadingMore else { return }
        isLoadingMore = true
        offset += 50
        do {
            let resp = try await apiClient.getBills(
                offset: offset,
                category: filterCategory,
                startDate: filterStartDate,
                endDate: filterEndDate,
                keyword: keyword
            )
            bills.append(contentsOf: resp.bills)
            hasMore = resp.bills.count >= 50
        } catch {
            LogBuffer.shared.error("BillViewModel.loadMore: \(error)")
            errorMessage = error.localizedDescription
        }
        isLoadingMore = false
    }

    func exportCSV(baseURL: String, token: String) async -> String? {
        let api = APIClient(baseURL: baseURL, token: token)
        do {
            return try await api.exportBillsCSV(
                startDate: filterStartDate,
                endDate: filterEndDate,
                category: filterCategory,
                keyword: keyword
            )
        } catch {
            LogBuffer.shared.error("BillViewModel.exportCSV: \(error)")
            return nil
        }
    }
}
