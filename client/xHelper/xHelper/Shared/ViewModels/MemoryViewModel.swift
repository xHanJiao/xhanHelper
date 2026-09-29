import Combine
import Foundation
import SwiftUI

@MainActor
class MemoryViewModel: ObservableObject {
    @Published var memories: [UserMemory] = []
    @Published var isLoading = false
    @Published var errorMessage: String?
    @Published var isEditMode = false
    @Published var selectedIDs: Set<String> = []

    /// 记忆页「列表 / 词云」开关（纯 UI 状态，仅 macOS 使用）。
    @Published var showCloudMode = false
    /// 词云用：所有 topic 及出现次数（按次数倒序）。
    @Published var cloudTopics: [MemoryTopic] = []
    /// 词云「≥ N 条」过滤阈值。
    @Published var minTopicCount = 1
    /// 当前按 topic 筛选（nil = 不筛）。点词云里的 topic 后切回列表生效。
    @Published var activeTopic: String?

    private let apiClient: APIClient
    private var currentOffset = 0
    private var hasMore = true

    init(apiClient: APIClient) {
        self.apiClient = apiClient
    }

    func load() async {
        isLoading = true
        errorMessage = nil
        currentOffset = 0
        hasMore = true
        do {
            let resp = try await apiClient.listMemories(topic: activeTopic ?? "")
            memories = resp.memories
            hasMore = resp.total >= 50
        } catch {
            LogBuffer.shared.error("MemoryViewModel.load: \(error)")
            errorMessage = error.localizedDescription
        }
        isLoading = false
    }

    func loadMore() async {
        guard hasMore, !isLoading else { return }
        isLoading = true
        currentOffset += 50
        do {
            let resp = try await apiClient.listMemories(offset: currentOffset, topic: activeTopic ?? "")
            memories.append(contentsOf: resp.memories)
            hasMore = resp.total >= 50
        } catch {
            LogBuffer.shared.error("MemoryViewModel.loadMore: \(error)")
            errorMessage = error.localizedDescription
        }
        isLoading = false
    }

    /// 拉词云 topic 列表（进入词云模式时调用）。
    func loadTopics() async {
        isLoading = true
        errorMessage = nil
        do {
            cloudTopics = try await apiClient.listMemoryTopics().topics
            minTopicCount = min(minTopicCount, max(1, cloudTopics.first?.count ?? 1))
        } catch {
            LogBuffer.shared.error("MemoryViewModel.loadTopics: \(error)")
            errorMessage = error.localizedDescription
        }
        isLoading = false
    }

    /// 点击词云里的 topic：按该 topic 筛选并切回列表（列表加载由 showCloudMode 变化的 onChange 触发）。
    func selectTopic(_ topic: String) {
        activeTopic = topic
        showCloudMode = false
    }

    /// 清除 topic 筛选：回词云（同上，topic 加载由 onChange 触发）。
    func clearTopic() {
        activeTopic = nil
        showCloudMode = true
    }

    /// 赞/踩互斥、再点取消（最终值由调用方算好）：乐观更新，失败回滚。
    func setRating(id: String, rating: Int) async {
        guard let idx = memories.firstIndex(where: { $0.id == id }) else { return }
        let old = memories[idx].rating
        guard old != rating else { return }
        withAnimation { memories[idx].rating = rating }
        do {
            try await apiClient.setMemoryRating(id: id, rating: rating)
        } catch {
            LogBuffer.shared.error("MemoryViewModel.setRating: \(error)")
            // ponytail: 同一卡片连点会起多个 Task，PATCH 响应可能乱序；仅当值仍是本任务写入的才回滚，
            // 避免用过期旧值覆盖新状态（单用户低概率）。要严格串行需按 id 维护 in-flight 表。
            if let i = memories.firstIndex(where: { $0.id == id }), memories[i].rating == rating {
                withAnimation { memories[i].rating = old }
            }
            errorMessage = error.localizedDescription
        }
    }

    func delete(id: String) async {
        guard let removed = memories.first(where: { $0.id == id }) else { return }
        withAnimation { memories.removeAll { $0.id == id } }
        do {
            try await apiClient.deleteMemory(id: id)
        } catch {
            LogBuffer.shared.error("MemoryViewModel.delete: \(error)")
            withAnimation { memories.append(removed) }
            errorMessage = error.localizedDescription
        }
    }

    func toggleSelection(_ id: String) {
        if selectedIDs.contains(id) {
            selectedIDs.remove(id)
        } else {
            selectedIDs.insert(id)
        }
    }

    func batchDelete() async {
        let ids = Array(selectedIDs)
        guard !ids.isEmpty else { return }
        withAnimation { memories.removeAll { selectedIDs.contains($0.id) } }
        isEditMode = false
        selectedIDs = []
        do {
            try await apiClient.batchDeleteMemories(ids: ids)
        } catch {
            LogBuffer.shared.error("MemoryViewModel.batchDelete: \(error)")
            errorMessage = error.localizedDescription
            await load()
        }
    }
}
