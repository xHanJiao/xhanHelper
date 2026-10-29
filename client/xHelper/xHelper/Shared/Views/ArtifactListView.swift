import SwiftUI

/// 右栏「灵阅」：聊天里生成过的 HTML 产物（标题 + 摘要 + 日期），点一条在右栏开 tab。
///
/// 观感与文档列表同款（`PageContainer` + `CardGridView`），但数据源是 artifacts 表、
/// 跟消息无关：会话归档了这里照样能翻（见 docs/design/html-artifact §9.5）。
struct ArtifactListView: View {
    @ObservedObject var viewModel: ArtifactViewModel

    /// 待确认删除的那条（产物一次性、删了没有回收站，必须过一道确认）
    @State private var pendingDelete: ArtifactSummary?

    var body: some View {
        PageContainer(
            title: "灵阅",
            icon: "doc.richtext",
            // 只有首屏空列表才整页转圈：翻页别把已显示的卡换成 spinner
            isLoading: viewModel.isLoading && viewModel.items.isEmpty,
            errorMessage: viewModel.errorMessage,
            isEmpty: viewModel.items.isEmpty && !viewModel.isLoading,
            emptyMessage: "灵阅还是空的——在聊天里让我做一张手账或对比图试试",
            onRetry: { Task { await viewModel.load() } }
        ) {
            CardGridView(
                items: viewModel.items,
                maxColumns: 3,
                minCardWidth: 260,
                onLoadMore: { await viewModel.loadMore() }
            ) { item in
                CardConfig(
                    title: item.title.isEmpty ? "未命名页面" : item.title,
                    subtitle: item.summary,
                    footerText: String(item.createdAt.prefix(10)),
                    style: .document,
                    actions: [
                        CardAction(label: "删除", icon: "trash", role: .destructive) {
                            pendingDelete = item
                        }
                    ],
                    onTap: {
                        ArtifactOpenCoordinator.shared.open(id: item.id, title: item.title)
                    }
                )
            }
        }
        .task { await viewModel.load() }
        .alert(
            "删除这一页？",
            isPresented: Binding(get: { pendingDelete != nil }, set: { if !$0 { pendingDelete = nil } }),
            presenting: pendingDelete
        ) { item in
            Button("删除", role: .destructive) {
                Task { await viewModel.delete(id: item.id) }
            }
            Button("取消", role: .cancel) {}
        } message: { item in
            Text("《\(item.title.isEmpty ? "未命名页面" : item.title)》将被删除，不能恢复；已分享的链接会立即失效。")
        }
    }
}
