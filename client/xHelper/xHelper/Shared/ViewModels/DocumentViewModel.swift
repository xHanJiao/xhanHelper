import Combine
import Foundation
import SwiftUI

enum DocumentBrowseMode: Hashable {
    case list
    case cloud
    /// 回收站：已软删的文档，7 天内可恢复
    case trash
}

/// 文档列表/词云的读者层级开关：仅成品（默认）/ 仅底稿 / 全部。单按钮点击循环。
/// 开关状态**不持久化**——下次进面板回默认，否则默认值形同虚设。
enum DocumentAudienceView: String, CaseIterable, Hashable {
    case finished
    /// `internal` 是 Swift 关键字，case 名用 drafts，rawValue 仍是后端的 "internal"。
    case drafts = "internal"
    case all

    var label: String {
        switch self {
        case .finished: return "成品"
        case .drafts: return "底稿"
        case .all: return "全部"
        }
    }

    /// 循环序：成品 → 底稿 → 全部 → 成品。
    var next: DocumentAudienceView {
        switch self {
        case .finished: return .drafts
        case .drafts: return .all
        case .all: return .finished
        }
    }
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
    @Published var tags: [CloudTag] = []
    /// 回收站列表（.trash 模式）。
    @Published var trashDocuments: [Document] = []
    @Published var isLoading = false
    @Published var isLoadingMore = false
    @Published var isTrashLoading = false
    @Published var isLoadingMoreTrash = false
    @Published var errorMessage: String?
    @Published var keyword = ""
    /// 检索目标字段，默认标题+正文（等价于历史行为）。
    @Published var searchFields: Set<DocumentSearchField> = [.title, .content]
    /// 列表模式的后端交集筛选标签（空=不过滤）。
    @Published var activeTags: [String] = []
    /// 读者层级视图：默认只看成品。不持久化（下次进面板回默认）。
    @Published var audienceView: DocumentAudienceView = .finished
    /// 当前档命中的总数（三态按钮显示当前档数量用）。
    @Published var listTotal: Int?

    /// 三态按钮标题：当前档 + 数量（数量未知时只显示档名）。
    var audienceLabel: String {
        guard let n = listTotal else { return audienceView.label }
        return "\(audienceView.label) · \(n)"
    }
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
    /// 列表模式多选下载：进入选择态 + 已勾选文档 id（纯 UI 状态）。
    @Published var isSelectingDocuments = false
    @Published var selectedDocumentIDs: Set<String> = []

    let apiClient: APIClient
    private var offset = 0
    private var hasMore = true
    private var trashOffset = 0
    private var trashHasMore = true
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

    // MARK: - 列表多选下载

    /// 进入多选下载态（清空上次勾选）。
    func enterDocumentSelection() {
        selectedDocumentIDs.removeAll()
        isSelectingDocuments = true
    }

    /// 退出多选下载态并清空勾选。
    func exitDocumentSelection() {
        isSelectingDocuments = false
        selectedDocumentIDs.removeAll()
    }

    /// 勾选 / 取消勾选一篇文档。
    func toggleDocumentSelection(_ id: String) {
        if selectedDocumentIDs.contains(id) {
            selectedDocumentIDs.remove(id)
        } else {
            selectedDocumentIDs.insert(id)
        }
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
                let n = try await self.apiClient.documentCount(tags: tags, audience: self.audienceView.rawValue)
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

    /// 按当前模式加载（进入面板 / 刷新时用）。
    func loadCurrentMode() async {
        if mode == .trash {
            await loadTrash()
        } else {
            await load()
        }
    }

    func loadTrash() async {
        isTrashLoading = true
        errorMessage = nil
        trashOffset = 0
        trashHasMore = true
        do {
            let resp = try await apiClient.listTrashDocuments()
            trashDocuments = resp.documents
            trashHasMore = resp.documents.count >= 24
        } catch {
            LogBuffer.shared.error("DocumentViewModel.loadTrash: \(error)")
            errorMessage = error.localizedDescription
        }
        isTrashLoading = false
    }

    func loadMoreTrash() async {
        guard trashHasMore, !isLoadingMoreTrash else { return }
        isLoadingMoreTrash = true
        trashOffset += 24
        do {
            let resp = try await apiClient.listTrashDocuments(offset: trashOffset)
            trashDocuments.append(contentsOf: resp.documents)
            trashHasMore = resp.documents.count >= 24
        } catch {
            LogBuffer.shared.error("DocumentViewModel.loadMoreTrash: \(error)")
            errorMessage = error.localizedDescription
        }
        isLoadingMoreTrash = false
    }

    /// 从回收站恢复：成功后就地移出回收站列表。
    func restore(_ doc: Document) async {
        do {
            try await apiClient.restoreDocument(id: doc.id)
        } catch {
            LogBuffer.shared.error("DocumentViewModel.restore: \(error)")
            ToastCenter.shared.show("恢复失败：\(error.localizedDescription)")
            return
        }
        trashDocuments.removeAll { $0.id == doc.id }
        ToastCenter.shared.show("已恢复")
    }

    /// 全部恢复回收站文档。
    func restoreAll() async {
        do {
            try await apiClient.restoreAllTrashDocuments()
        } catch {
            LogBuffer.shared.error("DocumentViewModel.restoreAll: \(error)")
            ToastCenter.shared.show("全部恢复失败：\(error.localizedDescription)")
            return
        }
        trashDocuments.removeAll()
        ToastCenter.shared.show("已全部恢复")
    }

    /// 永久删除回收站全部文档（不可恢复）。
    func purgeAll() async {
        do {
            try await apiClient.purgeAllTrashDocuments()
        } catch {
            LogBuffer.shared.error("DocumentViewModel.purgeAll: \(error)")
            ToastCenter.shared.show("清空失败：\(error.localizedDescription)")
            return
        }
        trashDocuments.removeAll()
        ToastCenter.shared.show("回收站已清空")
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
                fields: searchFields,
                audience: audienceView.rawValue
            )
            documents = resp.documents
            listTotal = resp.total
            hasMore = resp.documents.count >= 24
            let tagResp = try await apiClient.listDocumentTags(audience: audienceView.rawValue)
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

    /// 删除文档：软删（后端移入回收站，7 天内可恢复）。列表就地移除、不重置分页与滚动。
    /// ponytail: 标签词云计数不就地扣减，下次 load() 时刷新。
    func delete(_ doc: Document) async {
        do {
            try await apiClient.deleteDocument(id: doc.id)
        } catch {
            guard (error as NSError).code == 404 else {
                LogBuffer.shared.error("DocumentViewModel.delete: \(error)")
                ToastCenter.shared.show("删除失败：\(error.localizedDescription)")
                return
            }
        }
        documents.removeAll { $0.id == doc.id }
        selectedDocumentIDs.remove(doc.id)
        if openDocument?.id == doc.id { openDocument = nil }
        ToastCenter.shared.show("已移入回收站")
    }

    /// 批量删除所选文档：循环单篇接口（个人规模够用，量大再上批量端点）。
    /// 404 视为已删；非 404 失败的服务端没删掉，留在列表里（别假装成功）。
    /// 全成功弹一次「已移入回收站 N 篇」，否则「已移入回收站 x/N 篇，其余失败」。
    func deleteSelected() async {
        let ids = selectedDocumentIDs
        guard !ids.isEmpty else { return }
        var deleted = Set<String>()
        for id in ids {
            do {
                try await apiClient.deleteDocument(id: id)
                deleted.insert(id)
            } catch {
                if (error as NSError).code == 404 {
                    deleted.insert(id) // 已不存在 = 删成功
                } else {
                    LogBuffer.shared.error("DocumentViewModel.deleteSelected(\(id)): \(error)")
                }
            }
        }
        documents.removeAll { deleted.contains($0.id) }
        if let openID = openDocument?.id, deleted.contains(openID) { openDocument = nil }
        selectedDocumentIDs.removeAll()
        isSelectingDocuments = false
        ToastCenter.shared.show(deleted.count == ids.count
            ? "已移入回收站 \(deleted.count) 篇"
            : "已移入回收站 \(deleted.count)/\(ids.count) 篇，其余失败")
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
                audience: audienceView.rawValue,
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
