import Combine
import Foundation
import SwiftUI

enum DocumentBrowseMode: Hashable {
    case list
    case cloud
}

@MainActor
class DocumentViewModel: ObservableObject {
    @Published var documents: [Document] = []
    @Published var tags: [DocumentTag] = []
    @Published var isLoading = false
    @Published var isLoadingMore = false
    @Published var errorMessage: String?
    @Published var keyword = ""
    @Published var selectedTag: String?
    @Published var mode: DocumentBrowseMode = .list
    @Published var minTagCount = 1
    @Published var openDocument: Document?
    @Published var isDetailLoading = false

    private let apiClient: APIClient
    private var offset = 0
    private var hasMore = true

    init(apiClient: APIClient) {
        self.apiClient = apiClient
    }

    func search(_ kw: String) async {
        keyword = kw
        await load()
    }

    func selectTag(_ tag: String) async {
        selectedTag = tag
        mode = .list
        await load()
    }

    func clearTag() async {
        selectedTag = nil
        mode = .cloud
        await load()
    }

    func load() async {
        isLoading = true
        errorMessage = nil
        offset = 0
        hasMore = true
        do {
            let resp = try await apiClient.listDocuments(
                tag: selectedTag,
                keyword: keyword.isEmpty ? nil : keyword
            )
            documents = resp.documents
            hasMore = resp.documents.count >= 24
            tags = try await apiClient.listDocumentTags()
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
                tag: selectedTag,
                keyword: keyword.isEmpty ? nil : keyword,
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
