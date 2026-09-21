import SwiftUI

/// 事实列表：卡片形态 B（subject 作标题、content 作副标题）。
/// 只读 —— 没有删除/编辑入口，事实的增删改由后台决策段负责。
struct FactListView: View {
    @ObservedObject var viewModel: FactViewModel

    var body: some View {
        PageContainer(
            title: "事实",
            icon: "lightbulb",
            isLoading: viewModel.isLoading,
            errorMessage: viewModel.errorMessage,
            isEmpty: viewModel.facts.isEmpty && !viewModel.isLoading,
            emptyMessage: "还没有事实，多聊几轮后由后台自动积累",
            onRetry: { Task { await viewModel.load() } }
        ) {
            CardGridView(
                items: viewModel.facts,
                maxColumns: 3,
                minCardWidth: 260,
                onLoadMore: { await viewModel.loadMore() }
            ) { fact in
                CardConfig(
                    title: fact.subject,
                    subtitle: fact.content,
                    tags: [kindBadge(fact.kind)],
                    footerText: formatConversationDate(fact.updatedAt),
                    style: .memory
                )
            }
        }
        .task { await viewModel.load() }
    }

    /// kind 徽章：state → 状态 / event → 事件（后端只产出这两种，其余原样显示）。
    private func kindBadge(_ kind: String) -> TagBadge {
        let label: String
        switch kind {
        case "state": label = "状态"
        case "event": label = "事件"
        default: label = kind
        }
        return TagBadge(text: label, color: Color("accentSecondary"))
    }
}
