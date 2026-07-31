import SwiftUI

struct CardGridView<Item: Identifiable, Card: View>: View {
    let items: [Item]
    let maxColumns: Int
    let minCardWidth: CGFloat
    let spacing: CGFloat
    let onLoadMore: (() async -> Void)?
    @ViewBuilder let cardContent: (Item) -> Card

    @State private var availableWidth: CGFloat = 800

    init(
        items: [Item],
        maxColumns: Int = 3,
        minCardWidth: CGFloat = 240,
        spacing: CGFloat = 12,
        onLoadMore: (() async -> Void)? = nil,
        @ViewBuilder cardContent: @escaping (Item) -> Card
    ) {
        self.items = items
        self.maxColumns = maxColumns
        self.minCardWidth = minCardWidth
        self.spacing = spacing
        self.onLoadMore = onLoadMore
        self.cardContent = cardContent
    }

    private var columnCount: Int {
        let count = Int(availableWidth / minCardWidth)
        return min(max(count, 1), maxColumns)
    }

    private var columns: [GridItem] {
        Array(repeating: GridItem(.flexible(), spacing: spacing), count: columnCount)
    }

    var body: some View {
        GeometryReader { geo in
            ScrollView {
                LazyVGrid(columns: columns, spacing: spacing) {
                    ForEach(items) { item in
                        cardContent(item)
                            .onAppear {
                                if item.id == items.last?.id {
                                    Task { await onLoadMore?() }
                                }
                            }
                    }
                }
                .padding(spacing)
            }
            .onAppear {
                availableWidth = max(geo.size.width - spacing * 2, 0)
            }
            .onChange(of: geo.size.width) { _, new in
                availableWidth = max(new - spacing * 2, 0)
            }
        }
    }
}

extension CardGridView where Card == CardView {
    init(
        items: [Item],
        maxColumns: Int = 3,
        minCardWidth: CGFloat = 240,
        spacing: CGFloat = 12,
        onLoadMore: (() async -> Void)? = nil,
        cardConfig: @escaping (Item) -> CardConfig
    ) {
        self.items = items
        self.maxColumns = maxColumns
        self.minCardWidth = minCardWidth
        self.spacing = spacing
        self.onLoadMore = onLoadMore
        self.cardContent = { CardView(config: cardConfig($0)) }
    }
}
