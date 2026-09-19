import Combine
import Foundation
import SwiftUI

enum DocumentBrowseMode: Hashable {
    case list
    case cloud
}

/// 文档检索目标字段（输入框内多选；至少保留一个）。
/// tag 走精确边界匹配，title/content 走子串匹配（与后端 fields 参数一一对应）。
enum DocumentSearchField: String, CaseIterable, Hashable {
    case title
    case tag
    case content

    var label: String { rawValue }
}

@MainActor
class DocumentViewModel: ObservableObject {
    @Published var documents: [Document] = []
    @Published var tags: [DocumentTag] = []
    @Published var isLoading = false
    @Published var isLoadingMore = false
    @Published var errorMessage: String?
    @Published var keyword = ""
    /// 检索目标字段，默认标题+正文（等价于历史行为）。
    @Published var searchFields: Set<DocumentSearchField> = [.title, .content]
    /// 列表模式的后端交集筛选标签（空=不过滤）。
    @Published var activeTags: [String] = []
    @Published var mode: DocumentBrowseMode = .list
    @Published var minTagCount = 1
    /// 词云本地高亮的搜索词（纯 UI 状态，不参与后端检索）。
    @Published var cloudQuery = ""
    /// 词云 ⌘ 多选的标签集合（纯 UI 状态）。
    @Published var selectedTags: Set<String> = []
    /// 标签共现邻接表：tag → (邻标签 → 共现次数)，来自 /documents/tags 的 edges。
    @Published var cooccurrence: [String: [String: Int]] = [:]
    /// 当前多选交集命中的文档数（服务端 limit=0 只出 total）。
    @Published var resultCount: Int?
    @Published var openDocument: Document?
    @Published var isDetailLoading = false

    let apiClient: APIClient
    private var offset = 0
    private var hasMore = true
    private var countTask: Task<Void, Never>?

    init(apiClient: APIClient) {
        self.apiClient = apiClient
    }

    func search(_ kw: String) async {
        keyword = kw
        await load()
    }

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

    /// 直接共现邻居（1 跳）**交集**：与**全部**已选标签都同篇出现过的标签，剔除已选项。
    /// 与筛选的「越选越少」一致——每个候选再选下去都还能继续收敛。
    /// ponytail: 邻接交集是成对共现的近似（两两共现 ≠ 同篇全共现），作候选提示够用，真实篇数以服务端交集查询为准。
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
                let n = try await self.apiClient.documentCount(tags: tags)
                if !Task.isCancelled { self.resultCount = n }
            } catch {
                LogBuffer.shared.error("DocumentViewModel.resultCount: \(error)")
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

    func load() async {
        isLoading = true
        errorMessage = nil
        offset = 0
        hasMore = true
        do {
            let resp = try await apiClient.listDocuments(
                tags: activeTags,
                keyword: keyword.isEmpty ? nil : keyword,
                fields: searchFields
            )
            documents = resp.documents
            hasMore = resp.documents.count >= 24
            let tagResp = try await apiClient.listDocumentTags()
            tags = tagResp.tags
            buildGraph(tagResp.edges)
            minTagCount = min(minTagCount, max(1, tags.first?.count ?? 1))
        } catch {
            LogBuffer.shared.error("DocumentViewModel.load: \(error)")
            errorMessage = error.localizedDescription
        }
        isLoading = false
    }

    /// 刷新当前打开的详情文档（仅当详情页在前台时调用，见 BrowsePanelView）。
    func reloadOpenDocument() async {
        guard let id = openDocument?.id else { return }
        isDetailLoading = true
        defer { isDetailLoading = false }
        do {
            openDocument = try await apiClient.getDocument(id: id)
        } catch {
            LogBuffer.shared.error("DocumentViewModel.reloadOpenDocument: \(error)")
            guard (error as NSError).code == 404 else {
                ToastCenter.shared.show("刷新失败：\(error.localizedDescription)")
                return
            }
            ToastCenter.shared.show("文档不存在或已删除")
            openDocument = nil
            await load()
        }
    }

    /// 相邻文档是否可切：前一方向仅看已加载列表，后一方向还有未拉取的分页也算可切。
    var canGoPrevious: Bool { neighbor(-1) != nil }
    var canGoNext: Bool { neighbor(1) != nil || hasMore }

    /// offset：-1 上一篇 / +1 下一篇，按当前列表序（服务端 updated_at DESC, id DESC）。
    func openNeighbor(_ offset: Int) async {
        guard let id = openDocument?.id,
              let i = documents.firstIndex(where: { $0.id == id }) else { return }
        let j = i + offset
        if !documents.indices.contains(j) {
            guard offset > 0, hasMore else { return }
            await loadMore()
        }
        if documents.indices.contains(j) { openDocument = documents[j] }
    }

    private func neighbor(_ offset: Int) -> Document? {
        guard let id = openDocument?.id,
              let i = documents.firstIndex(where: { $0.id == id }) else { return nil }
        let j = i + offset
        return documents.indices.contains(j) ? documents[j] : nil
    }

    func loadMore() async {
        guard hasMore, !isLoadingMore else { return }
        isLoadingMore = true
        offset += 24
        do {
            let resp = try await apiClient.listDocuments(
                tags: activeTags,
                keyword: keyword.isEmpty ? nil : keyword,
                fields: searchFields,
                offset: offset
            )
            documents.append(contentsOf: resp.documents)
            hasMore = resp.documents.count >= 24
        } catch {
            LogBuffer.shared.error("DocumentViewModel.loadMore: \(error)")
            errorMessage = error.localizedDescription
        }
        isLoadingMore = false
    }
}
