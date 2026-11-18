import Combine
import Foundation
import SwiftUI

@MainActor
class BillViewModel: ObservableObject {
    @Published var bills: [Bill] = []
    @Published var isLoading = false
    @Published var isLoadingMore = false
    @Published var errorMessage: String?
    @Published var filterCategoryList: [String] = []
    @Published var filterStartDate = ""
    @Published var filterEndDate = ""
    @Published var keyword = ""
    @Published var filterOwner = ""
    @Published var ownerOptions: [String] = []
    @Published var categoryOptions: [String] = []
    @Published var billStats: BillStats?
    @Published var isLoadingStats = false

    private let apiClient: APIClient
    private var offset = 0
    @Published var hasMore = true
    /// 最近一次成功拉列表的时间：面板入口 30s 内重进不再打接口。筛选/搜索/刷新直走 `load()`，不受影响。
    private var loadedAt: Date?
    private static let cacheTTL: TimeInterval = 30

    init(apiClient: APIClient) {
        self.apiClient = apiClient
    }

    func search(_ kw: String) async {
        keyword = kw
        await load()
    }

    func loadOwners() async {
        do {
            ownerOptions = try await apiClient.fetchBillOwners().filter { !$0.isEmpty }
        } catch {
            LogBuffer.shared.error("BillViewModel.loadOwners: \(error)")
        }
    }

    func loadCategories() async {
        do {
            categoryOptions = try await apiClient.fetchBillCategories().filter { !$0.isEmpty }.sorted()
        } catch {
            LogBuffer.shared.error("BillViewModel.loadCategories: \(error)")
        }
    }

    func toggleCategory(_ cat: String) {
        if let idx = filterCategoryList.firstIndex(of: cat) {
            filterCategoryList.remove(at: idx)
        } else {
            filterCategoryList.append(cat)
        }
    }

    func loadStats() async {
        isLoadingStats = true
        defer { isLoadingStats = false }
        do {
            billStats = try await apiClient.fetchBillStats(
                categorys: filterCategoryList,
                startDate: filterStartDate,
                endDate: filterEndDate,
                keyword: keyword,
                owner: filterOwner
            )
        } catch {
            LogBuffer.shared.error("BillViewModel.loadStats: \(error)")
            billStats = nil
        }
    }

    /// 缓存还新鲜（30s 内成功拉过、且没有错误态）：面板入口直接放行、连闸门都不用等。
    var isFresh: Bool {
        guard errorMessage == nil, let loadedAt else { return false }
        return Date().timeIntervalSince(loadedAt) < Self.cacheTTL
    }

    /// 面板入口用：30s 内重进直接吃上一次的结果。失败/被取消不写时间戳，下次进来立刻重试。
    func loadIfStale() async {
        if isFresh { return }
        await load()
    }

    func load() async {
        isLoading = true
        errorMessage = nil
        offset = 0
        hasMore = true
        do {
            let resp = try await apiClient.getBills(
                categorys: filterCategoryList,
                startDate: filterStartDate,
                endDate: filterEndDate,
                keyword: keyword,
                owner: filterOwner
            )
            bills = resp.bills
            hasMore = resp.bills.count >= 50
            // 被取消的那次不算成功，别给它打 30s 缓存戳（否则重进面板拿到的还是空列表）
            if !Task.isCancelled { loadedAt = Date() }
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
                categorys: filterCategoryList,
                startDate: filterStartDate,
                endDate: filterEndDate,
                keyword: keyword,
                owner: filterOwner
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
                categorys: filterCategoryList,
                keyword: keyword,
                owner: filterOwner
            )
        } catch {
            LogBuffer.shared.error("BillViewModel.exportCSV: \(error)")
            ToastCenter.shared.show("导出失败：\(error.localizedDescription)")
            return nil
        }
    }
}
