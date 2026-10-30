import Combine
import Foundation

/// 右栏「灵阅」：聊天里生成过的 HTML 产物列表（按时间倒序，30/页）。
///
/// 只拿标题/摘要/时间，正文点开时由 `ArtifactOpenCoordinator` 按 id 拉
/// （见 docs/design/html-artifact §9.5）。
@MainActor
final class ArtifactViewModel: ObservableObject {
    @Published private(set) var items: [ArtifactSummary] = []
    @Published var isLoading = false
    @Published var errorMessage: String?

    private static let pageSize = 30 // 服务端默认也是 30（server/.../controller/artifact.go 的 DefaultQuery）

    private let apiClient: APIClient
    /// 列表总数（侧栏节名「· N 篇」用；`loadMore` 的边界也靠它）
    @Published private(set) var total = 0
    private var isLoadingMore = false

    init(apiClient: APIClient) {
        self.apiClient = apiClient
    }

    func load() async {
        isLoading = true
        errorMessage = nil
        defer { isLoading = false }
        do {
            let resp = try await apiClient.listArtifacts(limit: Self.pageSize)
            items = resp.items
            total = resp.total
        } catch where Self.isCancellation(error) {
            // 切 tab / 关面板会让 `.task` 被取消。取消不是加载失败：别把「已取消」写进
            // 共享的 errorMessage（灵阅面板会据此显示「加载失败 + 重试」）。
        } catch {
            LogBuffer.shared.error("ArtifactViewModel.load: \(error)")
            errorMessage = error.localizedDescription
        }
    }

    /// 滚到底自动加载下一页；失败只记日志 + toast，已显示的列表不动。
    func loadMore() async {
        guard !isLoadingMore, !isLoading, items.count < total else { return }
        isLoadingMore = true
        defer { isLoadingMore = false }
        do {
            let resp = try await apiClient.listArtifacts(limit: Self.pageSize, offset: items.count)
            // offset 分页 + 翻页期间又生成了一条 → 第 2 页会重复第 1 页的末条，按 id 去重
            // （重复 id 会让 SwiftUI 报 "ID occurs multiple times" 并可能错行）
            let known = Set(items.map(\.id))
            items.append(contentsOf: resp.items.filter { !known.contains($0.id) })
            total = resp.total
        } catch where Self.isCancellation(error) {
            // 同上：切走导致的取消不 toast、不记错误
        } catch {
            LogBuffer.shared.error("ArtifactViewModel.loadMore: \(error)")
            ToastCenter.shared.show("加载更多失败：\(error.localizedDescription)")
        }
    }

    /// 取消（切 tab / 关面板）与真失败必须分开：取消不能污染共享列表状态。
    private static func isCancellation(_ error: Error) -> Bool {
        error is CancellationError || (error as? URLError)?.code == .cancelled
    }

    /// 删除一条（软删，无回收站）。成功后就地移除，不重拉整页——列表会跳动。
    /// 分页不会因此错位：下次 `loadMore` 用 `items.count` 当 offset，本地少一条服务端也少一条。
    func delete(id: String) async {
        do {
            try await apiClient.deleteArtifact(id: id)
            removeFromList([id])
        } catch {
            LogBuffer.shared.error("ArtifactViewModel.delete: \(error)")
            ToastCenter.shared.show("删除失败：\(error.localizedDescription)")
        }
    }

    /// 批量删除（灵阅多选）。成功就地移除，不重拉整页。返回是否全部成功。
    func deleteMany(ids: [String]) async -> Bool {
        guard !ids.isEmpty else { return true }
        // 服务端单批 ≤100；列表能滚很多页，超了就分块（否则整批 400，一条都删不掉）
        let chunks = stride(from: 0, to: ids.count, by: 100).map {
            Array(ids[$0..<min($0 + 100, ids.count)])
        }
        var deleted: [String] = []
        for chunk in chunks {
            do {
                try await apiClient.deleteArtifacts(ids: chunk)
                deleted.append(contentsOf: chunk)
            } catch {
                LogBuffer.shared.error("ArtifactViewModel.deleteMany: \(error)")
                ToastCenter.shared.show("删除失败：\(error.localizedDescription)")
                // 前面的分块已经删掉了：照常从列表移除，别让它们留在界面上
                removeFromList(deleted)
                return false
            }
        }
        removeFromList(deleted)
        return true
    }

    /// 就地移除 + 同步总数 + 关掉对应的右栏 tab（被删的页不该留僵尸 tab）。
    private func removeFromList(_ ids: [String]) {
        guard !ids.isEmpty else { return }
        // 正文缓存跟着一起清：否则删掉的页还能从 Caches 里被翻出来
        ArtifactHTMLCache.shared.remove(ids: ids)
        let set = Set(ids)
        let removed = items.filter { set.contains($0.id) }.count
        items.removeAll { set.contains($0.id) }
        // 只减真正在列表里的条数：ids 可能含已被删掉的
        total = max(0, total - removed)
        for id in ids {
            ArtifactOpenCoordinator.shared.closeTabIfOpen(id: id)
        }
    }
}
