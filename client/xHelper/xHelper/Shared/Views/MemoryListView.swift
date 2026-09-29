import SwiftUI

struct MemoryListView: View {
    @StateObject var viewModel: MemoryViewModel
    @State private var selectedMemory: UserMemory?

    var body: some View {
        Group {
            #if os(macOS)
            if viewModel.showCloudMode {
                cloudContent
            } else {
                listContent
            }
            #else
            listContent
            #endif
        }
        .task {
            if viewModel.showCloudMode {
                await viewModel.loadTopics()
            } else {
                await viewModel.load()
            }
        }
        .onChange(of: viewModel.showCloudMode) { _, show in
            // 进词云拉 topic（聚合轻量），回列表按当前 topic 拉记忆；避免依赖调用方各自记得加载
            Task { show ? await viewModel.loadTopics() : await viewModel.load() }
        }
        .overlay {
            if let memory = selectedMemory {
                MemoryDetailOverlay(memory: memory, onClose: { selectedMemory = nil })
            }
        }
    }

    private var listContent: some View {
        VStack(spacing: 0) {
            if viewModel.activeTopic != nil {
                activeTopicBar
            }
            listPage
        }
    }

    private var listPage: some View {
        PageContainer(
            title: "记忆",
            icon: "brain.head.profile",
            isLoading: viewModel.isLoading,
            errorMessage: viewModel.errorMessage,
            isEmpty: viewModel.memories.isEmpty && !viewModel.isLoading,
            emptyMessage: viewModel.activeTopic == nil ? "还没有记忆，去聊天页面和 AI 聊聊吧" : "该主题下没有记忆",
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
                    tags: [sourceBadge(memory.source)] + parseTags(memory.tags),
                    style: .memory,
                    actions: feedbackActions(memory),
                    onTap: { selectedMemory = memory }
                )
            }
        }
    }

    /// 列表模式的 topic 筛选条（点词云里的主题后出现）。
    private var activeTopicBar: some View {
        HStack(spacing: 6) {
            Image(systemName: "tag.fill")
                .font(.system(size: 11))
            Text(viewModel.activeTopic ?? "")
                .font(.system(size: 12, weight: .medium))
                .padding(.horizontal, 7)
                .padding(.vertical, 1)
                .background(Capsule().fill(Color("appAccent").opacity(0.16)))
            Spacer()
            Button {
                viewModel.clearTopic()
            } label: {
                Image(systemName: "xmark.circle.fill")
                    .font(.system(size: 12))
            }
            .buttonStyle(.plain)
            .help("清除主题筛选")
        }
        .foregroundColor(Color("appAccent"))
        .padding(.horizontal, 16)
        .padding(.vertical, 8)
        .background(Color("appAccent").opacity(0.1))
    }

    #if os(macOS)
    private var cloudContent: some View {
        let visible = viewModel.cloudTopics.filter { $0.count >= viewModel.minTopicCount }
        return PageContainer(
            title: "主题词云",
            icon: "brain.head.profile",
            isLoading: viewModel.isLoading,
            errorMessage: viewModel.errorMessage,
            isEmpty: visible.isEmpty && !viewModel.isLoading,
            emptyMessage: viewModel.cloudTopics.isEmpty ? "还没有主题" : "没有达到 \(viewModel.minTopicCount) 条的主题",
            onRetry: { Task { await viewModel.loadTopics() } }
        ) {
            TagCloudView(tags: visible.map(\.cloudTag), onTap: { topic in
                viewModel.selectTopic(topic)
            }, onCmdTap: { topic in
                // 记忆无多选：⌘ 点击也按单击筛选，避免点了没反应
                viewModel.selectTopic(topic)
            }, helpLabel: "条记忆")
        }
    }
    #endif

    /// 卡片反馈按钮：👍/👎 互斥，再点一次取消（回到未评）。
    private func feedbackActions(_ memory: UserMemory) -> [CardAction] {
        let rating = memory.rating ?? 0
        return [
            CardAction(
                label: "",
                icon: rating == 1 ? "hand.thumbsup.fill" : "hand.thumbsup",
                role: .normal,
                isActive: rating == 1
            ) { rate(memory, to: rating == 1 ? 0 : 1) },
            CardAction(
                label: "",
                icon: rating == -1 ? "hand.thumbsdown.fill" : "hand.thumbsdown",
                role: rating == -1 ? .destructive : .normal,
                isActive: rating == -1
            ) { rate(memory, to: rating == -1 ? 0 : -1) },
        ]
    }

    private func rate(_ memory: UserMemory, to rating: Int) {
        Task { await viewModel.setRating(id: memory.id, rating: rating) }
    }

    private func sourceBadge(_ source: String) -> TagBadge {
        TagBadge(text: source, color: .primary)
    }

    private func parseTags(_ tags: String?) -> [TagBadge] {
        guard let tags, !tags.isEmpty else { return [] }
        return tags.split(separator: ",").map {
            TagBadge(text: String($0).trimmingCharacters(in: .whitespaces), color: Color("appAccent"))
        }
    }
}
