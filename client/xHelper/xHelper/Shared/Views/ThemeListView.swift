import SwiftUI

struct ThemeListView: View {
    @StateObject var viewModel: ThemeViewModel
    @State private var selectedTheme: AppTheme?

    var body: some View {
        PageContainer(
            title: "主题",
            icon: "tag",
            isLoading: viewModel.isLoading,
            errorMessage: viewModel.errorMessage,
            isEmpty: viewModel.themes.isEmpty && !viewModel.isLoading,
            emptyMessage: "还没有主题",
            onRetry: { Task { await viewModel.load() } }
        ) {
            CardGridView(
                items: viewModel.themes,
                maxColumns: 3,
                minCardWidth: 260,
                onLoadMore: { await viewModel.loadMore() }
            ) { theme in
                CardConfig(
                    title: theme.name,
                    subtitle: theme.description,
                    tags: [TagBadge(text: theme.status, color: statusColor(for: theme.status))],
                    style: .theme,
                    onTap: { selectedTheme = theme }
                )
            }
        }
        .task { await viewModel.load() }
        .overlay {
            if let theme = selectedTheme {
                ThemeDetailOverlay(theme: theme, onClose: { selectedTheme = nil })
            }
        }
    }

    private func statusColor(for status: String) -> Color {
        switch status {
        case "active": return .green
        case "closed": return .secondary
        default: return .mint
        }
    }
}
