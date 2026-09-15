import SwiftUI

/// 已归档会话列表（设置页入口）。超期无活动或手动删除的会话都在这里，可恢复回「最近会话」。
struct ArchivedConversationListView: View {
    let apiClient: APIClient

    @State private var conversations: [Conversation] = []
    @State private var isLoading = false
    @State private var isLoadingMore = false
    @State private var hasMore = false
    @State private var errorMessage: String?
    @State private var offset = 0
    private let pageSize = 50

    var body: some View {
        PageContainer(
            title: "已归档会话",
            icon: "archivebox",
            isLoading: isLoading && conversations.isEmpty,
            errorMessage: errorMessage,
            isEmpty: conversations.isEmpty && !isLoading,
            emptyMessage: "没有已归档的会话",
            onRetry: { Task { await load() } }
        ) {
            ScrollView {
                LazyVStack(spacing: 0) {
                    ForEach(conversations) { conv in
                        row(conv)
                        Divider()
                    }
                    if hasMore {
                        ProgressView()
                            .controlSize(.small)
                            .frame(maxWidth: .infinity)
                            .padding(.vertical, 10)
                            .onAppear { Task { await loadMore() } }
                    }
                }
            }
        }
        .task { await load() }
    }

    private func row(_ conv: Conversation) -> some View {
        HStack(spacing: 12) {
            VStack(alignment: .leading, spacing: 3) {
                Text(conv.title.isEmpty ? "无标题会话" : conv.title)
                    .font(.body)
                    .lineLimit(1)
                Text(formatConversationDate(conv.updatedAt))
                    .font(.caption)
                    .foregroundColor(.secondary)
            }
            Spacer()
            Button("恢复") { Task { await restore(conv.id) } }
                .buttonStyle(.bordered)
                .controlSize(.small)
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 8)
        .frame(maxWidth: .infinity, alignment: .leading)
        .contentShape(Rectangle())
    }

    private func load() async {
        isLoading = true
        errorMessage = nil
        defer { isLoading = false }
        do {
            let resp = try await apiClient.listConversations(limit: pageSize, offset: 0, archived: true)
            conversations = resp.conversations
            offset = resp.conversations.count
            hasMore = offset < resp.total
        } catch {
            errorMessage = "加载失败"
            LogBuffer.shared.error("ArchivedConversationListView.load: \(error)")
        }
    }

    private func loadMore() async {
        guard !isLoadingMore, hasMore else { return }
        isLoadingMore = true
        defer { isLoadingMore = false }
        do {
            let resp = try await apiClient.listConversations(limit: pageSize, offset: offset, archived: true)
            conversations.append(contentsOf: resp.conversations)
            offset += resp.conversations.count
            hasMore = offset < resp.total
        } catch {
            LogBuffer.shared.error("ArchivedConversationListView.loadMore: \(error)")
        }
    }

    private func restore(_ id: String) async {
        do {
            try await apiClient.restoreConversation(id: id)
            conversations.removeAll { $0.id == id }
            NotificationCenter.default.post(name: .conversationListNeedsRefresh, object: nil)
        } catch {
            LogBuffer.shared.error("ArchivedConversationListView.restore: \(error)")
        }
    }
}
