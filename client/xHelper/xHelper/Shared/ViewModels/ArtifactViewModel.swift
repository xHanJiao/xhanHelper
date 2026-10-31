import Combine
import Foundation

/// 灵阅的两种浏览模式：列表（卡片网格）/ 词云（标签）。
enum ArtifactBrowseMode: Hashable {
    case list
    case cloud
}

/// 右栏「灵阅」：聊天里生成过的 HTML 产物列表（按时间倒序，30/页）。
///
/// 只拿标题/摘要/标签/时间，正文点开时由 `ArtifactOpenCoordinator` 按 id 拉
/// （见 docs/design/html-artifact §9.5）。
///
/// 词云状态（mode / selectedTags / cooccurrence / resultCount…）与 `DocumentViewModel`
/// 是两份同口径实现：共享的是 UI 组件（`TagCloudView` / `TagChip`），状态层再抽要转发
/// `@Published`，风险大于收益。改动词云交互时两边一起对。
@MainActor
final class ArtifactViewModel: ObservableObject {
    @Published private(set) var items: [ArtifactSummary] = []
    @Published var isLoading = false
    @Published var errorMessage: String?
    /// 多选态（入口与工具条在 `BrowsePanelView` 的页头，状态得放这里才读得到）
    @Published var isSelecting = false
    @Published var selectedIDs: Set<String> = []
    /// 批量删除在飞：防同一批重复提交（与 `ImageViewModel.isDeleting` 同口径）
    @Published private(set) var isDeleting = false

    // MARK: - 词云

    @Published var mode: ArtifactBrowseMode = .list
    @Published var tags: [CloudTag] = []
    @Published var minTagCount = 1
    /// 词云本地高亮的搜索词（纯 UI 状态，不参与后端检索）。
    @Published var cloudQuery = ""
    /// 词云 ⌘ 多选的标签集合（纯 UI 状态）。
    @Published var selectedTags: Set<String> = []
    /// 标签共现邻接表：tag → (邻标签 → 共现次数)，来自 /artifacts/tags 的 edges。
    @Published var cooccurrence: [String: [String: Int]] = [:]
    /// 当前多选交集的页数（服务端 limit=0 只出 total）。
    @Published var resultCount: Int?
    /// 列表模式的后端交集筛选标签（空=不过滤）。
    @Published var activeTags: [String] = []
    /// 列表模式的后端关键词（搜标题/摘要，与 `activeTags` 取交集）。
    @Published var keyword = ""

    private static let pageSize = 30 // 服务端默认也是 30（server/.../controller/artifact.go 的 DefaultQuery）
    /// 与后端 `?tags=` 的上限一致（超过 10 个 → 400）。
    private static let maxSelectedTags = 10

    private let apiClient: APIClient
    /// 列表总数（侧栏节名「· N 篇」用；`loadMore` 的边界也靠它）
    @Published private(set) var total = 0
    private var isLoadingMore = false
    /// 列表代际：`load()` 自增；`loadMore` 发现代际变了就丢弃旧响应
    /// （否则旧关键词的第二页会拼进新一次的搜索结果）。
    private var generation = 0
    private var countTask: Task<Void, Never>?

    init(apiClient: APIClient) {
        self.apiClient = apiClient
    }

    func load() async {
        generation += 1
        let gen = generation
        isLoading = true
        errorMessage = nil
        do {
            let resp = try await apiClient.listArtifacts(
                tags: activeTags,
                keyword: keyword.isEmpty ? nil : keyword,
                limit: Self.pageSize
            )
            // 更晚的一次 load 已经接管：这次响应作废，别把旧结果盖回去
            // （也不动 isLoading，转圈归最新那次管）
            guard gen == generation else { return }
            items = resp.items
            total = resp.total
            // 重载后可能有条目已被别处删掉：勾选里不能留幽灵 id
            selectedIDs.formIntersection(Set(items.map(\.id)))
        } catch where Self.isCancellation(error) {
            // 切 tab / 关面板会让 `.task` 被取消。取消不是加载失败：别把「已取消」写进
            // 共享的 errorMessage（灵阅面板会据此显示「加载失败 + 重试」）。
            guard gen == generation else { return }
        } catch {
            // 旧一次请求的失败不能污染新一次列表（错误页会整块替换内容）
            guard gen == generation else { return }
            LogBuffer.shared.error("ArtifactViewModel.load: \(error)")
            errorMessage = error.localizedDescription
        }
        // 收尾也只归最新那次：旧的取消不能把还在飞的转圈提前撤掉
        guard gen == generation else { return }
        // 列表已经好了就先撤转圈；词云慢不该拖着列表一直转
        isLoading = false
        // 词云不是列表的必要条件：单独失败只记日志、保留上一次的标签，别把整页拖进错误态
        await loadTags()
    }

    private func loadTags() async {
        do {
            let tagResp = try await apiClient.listArtifactTags()
            tags = tagResp.tags
            buildGraph(tagResp.edges)
            minTagCount = min(minTagCount, max(1, tags.first?.count ?? 1))
        } catch where Self.isCancellation(error) {
            // 同上：取消是正常的
        } catch {
            LogBuffer.shared.error("ArtifactViewModel.loadTags: \(error)")
        }
    }

    /// 列表搜索：标题/摘要关键词（与标签筛选叠加）。空串 = 取消搜索。
    func search(_ kw: String) async {
        keyword = kw.trimmingCharacters(in: .whitespacesAndNewlines)
        await load()
    }

    /// 滚到底自动加载下一页；失败只记日志 + toast，已显示的列表不动。
    func loadMore() async {
        guard !isLoadingMore, !isLoading, items.count < total else { return }
        isLoadingMore = true
        defer { isLoadingMore = false }
        let gen = generation
        do {
            let resp = try await apiClient.listArtifacts(
                tags: activeTags,
                keyword: keyword.isEmpty ? nil : keyword,
                limit: Self.pageSize,
                offset: items.count
            )
            // 翻页期间换了关键词/标签（新的 load 已接管）：旧关键词的第二页属于上一份列表，丢掉
            guard gen == generation else { return }
            // offset 分页 + 翻页期间又生成了一条 → 第 2 页会重复第 1 页的末条，按 id 去重
            // （重复 id 会让 SwiftUI 报 "ID occurs multiple times" 并可能错行）
            let known = Set(items.map(\.id))
            items.append(contentsOf: resp.items.filter { !known.contains($0.id) })
            total = resp.total
        } catch where Self.isCancellation(error) {
            // 同上：切走导致的取消不 toast、不记错误
        } catch {
            // 旧筛选的第二页失败，不该给当前列表弹错误
            guard gen == generation else { return }
            LogBuffer.shared.error("ArtifactViewModel.loadMore: \(error)")
            ToastCenter.shared.show("加载更多失败：\(error.localizedDescription)")
        }
    }

    // MARK: - 词云交互（与 DocumentViewModel 同口径）

    /// 单击标签：按该标签筛选 → 切列表（清掉词云多选）。
    func selectTag(_ tag: String) async {
        clearSelection()
        activeTags = [tag]
        mode = .list
        await load()
    }

    /// ⌘ 点击标签：切换多选（留在词云），并刷新交集篇数。
    func toggleSelect(_ tag: String) {
        if selectedTags.contains(tag) {
            selectedTags.remove(tag)
        } else {
            // 与 service 的 tags 上限一致（controller 超过 10 个直接 400）
            guard selectedTags.count < Self.maxSelectedTags else {
                ToastCenter.shared.show("最多同时选 \(Self.maxSelectedTags) 个标签")
                return
            }
            selectedTags.insert(tag)
        }
        scheduleResultCount()
    }

    /// 清空词云多选（不动列表筛选）。
    func clearSelection() {
        countTask?.cancel()
        selectedTags.removeAll()
        resultCount = nil
    }

    /// 查看多选交集：切列表模式按所选标签交集筛选。
    func filterBySelection() async {
        guard !selectedTags.isEmpty else { return }
        activeTags = selectedTags.sorted()
        mode = .list
        await load()
    }

    /// 清除列表标签筛选：回词云并清空多选。
    func clearFilter() async {
        clearSelection()
        activeTags = []
        mode = .cloud
        await load()
    }

    /// 直接共现邻居（1 跳）**交集**：与**全部**已选标签都同页出现过的标签，剔除已选项。
    /// ponytail: 两两共现是近似（共现 ≠ 同页全共现），作候选提示够用，真实篇数以服务端交集查询为准。
    func neighbors(of selected: Set<String>) -> Set<String> {
        guard let first = selected.first else { return [] }
        var out = Set((cooccurrence[first] ?? [:]).keys)
        for tag in selected.dropFirst() {
            out.formIntersection((cooccurrence[tag] ?? [:]).keys)
        }
        out.subtract(selected)
        return out
    }

    private func scheduleResultCount() {
        countTask?.cancel()
        guard !selectedTags.isEmpty else {
            resultCount = nil
            return
        }
        let tags = selectedTags.sorted()
        countTask = Task { [weak self] in
            guard let self else { return }
            do {
                let n = try await self.apiClient.artifactCount(tags: tags)
                if !Task.isCancelled { self.resultCount = n }
            } catch where Self.isCancellation(error) {
                // 取消是正常的：用户又点了别的标签
            } catch {
                LogBuffer.shared.error("ArtifactViewModel.resultCount: \(error)")
                if !Task.isCancelled { self.resultCount = nil }
            }
        }
    }

    private func buildGraph(_ edges: [TagEdge]) {
        var adj: [String: [String: Int]] = [:]
        for e in edges {
            adj[e.a, default: [:]][e.b] = e.count
            adj[e.b, default: [:]][e.a] = e.count
        }
        cooccurrence = adj
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

    // MARK: - 多选

    func enterSelection() {
        selectedIDs.removeAll()
        isSelecting = true
    }

    func exitSelection() {
        isSelecting = false
        selectedIDs.removeAll()
    }

    func toggleSelection(_ id: String) {
        if selectedIDs.contains(id) {
            selectedIDs.remove(id)
        } else {
            selectedIDs.insert(id)
        }
    }

    func selectAll() {
        selectedIDs = Set(items.map(\.id))
    }

    /// 删除所选：全成功才退出选择态；部分失败留在选择态，但先把已删的从勾选里剔掉
    /// （否则「已选 N」虚高，重试还会把幽灵 id 再发一遍）。
    func deleteSelected() async {
        guard !selectedIDs.isEmpty, !isDeleting else { return }
        let ids = Array(selectedIDs)
        isDeleting = true
        defer { isDeleting = false }
        if await deleteMany(ids: ids) {
            exitSelection()
            ToastCenter.shared.show("已删除 \(ids.count) 页")
        } else {
            selectedIDs.formIntersection(Set(items.map(\.id)))
        }
    }
}
