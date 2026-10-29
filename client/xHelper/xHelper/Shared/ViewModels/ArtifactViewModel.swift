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
    private var total = 0
    private var isLoadingMore = false

    init(apiClient: APIClient) {
        self.apiClient = apiClient
    }

    func load() async {
        isLoading = true
        errorMessage = nil
        do {
            let resp = try await apiClient.listArtifacts(limit: Self.pageSize)
            items = resp.items
            total = resp.total
        } catch {
            LogBuffer.shared.error("ArtifactViewModel.load: \(error)")
            errorMessage = error.localizedDescription
        }
        isLoading = false
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
        } catch {
            LogBuffer.shared.error("ArtifactViewModel.loadMore: \(error)")
            ToastCenter.shared.show("加载更多失败：\(error.localizedDescription)")
        }
    }

    /// 删除一条（软删，无回收站）。成功后就地移除，不重拉整页——列表会跳动。
    /// 分页不会因此错位：下次 `loadMore` 用 `items.count` 当 offset，本地少一条服务端也少一条。
    func delete(id: String) async {
        do {
            try await apiClient.deleteArtifact(id: id)
            items.removeAll { $0.id == id }
            total = max(0, total - 1)
            // 该页若正开在右栏 tab 里，一并关掉，别留一个内容还在的僵尸 tab
            ArtifactOpenCoordinator.shared.closeTabIfOpen(id: id)
        } catch {
            LogBuffer.shared.error("ArtifactViewModel.delete: \(error)")
            ToastCenter.shared.show("删除失败：\(error.localizedDescription)")
        }
    }
}
