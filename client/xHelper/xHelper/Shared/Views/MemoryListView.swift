import SwiftUI

struct MemoryListView: View {
    @StateObject var viewModel: MemoryViewModel
    @State private var selectedMemory: UserMemory?

    var body: some View {
        PageContainer(
            title: "记忆",
            icon: "brain.head.profile",
            isLoading: viewModel.isLoading,
            errorMessage: viewModel.errorMessage,
            isEmpty: viewModel.memories.isEmpty && !viewModel.isLoading,
            emptyMessage: "还没有记忆，去聊天页面和 AI 聊聊吧",
            onRetry: { Task { await viewModel.load() } }
        ) {
            CardGridView(
                items: viewModel.memories,
                maxColumns: 3,
                minCardWidth: 260,
                onLoadMore: { await viewModel.loadMore() }
            ) { memory in
                CardConfig(
                    title: memory.topic ?? memory.source,
                    subtitle: memory.memoryValue,
                    tags: parseTags(memory.tags),
                    style: .memory,
                    onTap: { selectedMemory = memory }
                )
            }
        }
        .task { await viewModel.load() }
        .overlay {
            if let memory = selectedMemory {
                MemoryDetailOverlay(memory: memory, onClose: { selectedMemory = nil })
            }
        }
    }

    private func parseTags(_ tags: String?) -> [TagBadge] {
        guard let tags, !tags.isEmpty else { return [] }
        return tags.split(separator: ",").map {
            TagBadge(text: String($0).trimmingCharacters(in: .whitespaces), color: Color("appAccent"))
        }
    }
}
