import Combine
import Foundation
import SwiftUI

@MainActor
class DocumentViewModel: ObservableObject {
    @Published var documents: [Document] = []
    @Published var isLoading = false
    @Published var isLoadingMore = false
    @Published var errorMessage: String?
    @Published var keyword = ""
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

    func load() async {
        isLoading = true
        errorMessage = nil
        offset = 0
        hasMore = true
        do {
            let resp = try await apiClient.listDocuments(keyword: keyword.isEmpty ? nil : keyword)
            documents = resp.documents
            hasMore = resp.documents.count >= 24
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

    func loadMore() async {
        guard hasMore, !isLoadingMore else { return }
        isLoadingMore = true
        offset += 24
        do {
            let resp = try await apiClient.listDocuments(keyword: keyword.isEmpty ? nil : keyword, offset: offset)
            documents.append(contentsOf: resp.documents)
            hasMore = resp.documents.count >= 24
        } catch {
            LogBuffer.shared.error("DocumentViewModel.loadMore: \(error)")
            errorMessage = error.localizedDescription
        }
        isLoadingMore = false
    }
}
