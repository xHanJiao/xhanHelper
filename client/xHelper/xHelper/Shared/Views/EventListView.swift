import SwiftUI

struct EventListView: View {
    @StateObject var viewModel: EventViewModel
    @State private var selectedEvent: AppEventThread?

    var body: some View {
        PageContainer(
            title: "事件",
            icon: "square.stack.3d.up",
            isLoading: viewModel.isLoading,
            errorMessage: viewModel.errorMessage,
            isEmpty: viewModel.events.isEmpty && !viewModel.isLoading,
            emptyMessage: "还没有事件",
            onRetry: { Task { await viewModel.load() } }
        ) {
            CardGridView(
                items: viewModel.events,
                maxColumns: 3,
                minCardWidth: 260,
                onLoadMore: { await viewModel.loadMore() }
            ) { event in
                CardConfig(
                    title: event.name,
                    subtitle: event.summary,
                    tags: [TagBadge(text: "\(event.memoryCount) 条记忆", color: Color("appAccent"))],
                    footerText: event.lastMemoryAt.map { "最近记忆 \(formatConversationDate($0))" },
                    style: .event,
                    onTap: { selectedEvent = event }
                )
            }
        }
        .task { await viewModel.load() }
        .overlay {
            if let event = selectedEvent {
                EventDetailOverlay(event: event, viewModel: viewModel, onClose: { selectedEvent = nil })
            }
        }
    }
}
