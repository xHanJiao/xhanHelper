import SwiftUI
#if os(macOS)
import AppKit
#endif

enum ColumnWidth {
    case fixed(CGFloat)
    case flexible
}

struct AnyColumn<Item> {
    let id: String
    let title: String
    let alignment: HorizontalAlignment
    let initialWidth: ColumnWidth
    let minWidth: CGFloat
    let maxWidth: CGFloat
    let cell: (Item) -> AnyView
    let tooltip: ((Item) -> String)?
    let headerAccessory: ((CGFloat) -> AnyView)?

    init<Content: View>(
        _ title: String,
        id: String = UUID().uuidString,
        width: ColumnWidth = .flexible,
        alignment: HorizontalAlignment = .leading,
        minWidth: CGFloat = 40,
        maxWidth: CGFloat = .infinity,
        tooltip: ((Item) -> String)? = nil,
        headerAccessory: ((CGFloat) -> AnyView)? = nil,
        @ViewBuilder cell: @escaping (Item) -> Content
    ) {
        self.id = id
        self.title = title
        self.alignment = alignment
        self.initialWidth = width
        self.minWidth = minWidth
        self.maxWidth = maxWidth
        self.cell = { AnyView(cell($0)) }
        self.tooltip = tooltip
        self.headerAccessory = headerAccessory
    }
}

func tableColumn<Item, Content: View>(
    _ title: String,
    width: ColumnWidth = .flexible,
    alignment: HorizontalAlignment = .leading,
    tooltip: ((Item) -> String)? = nil,
    headerAccessory: ((CGFloat) -> AnyView)? = nil,
    @ViewBuilder cell: @escaping (Item) -> Content
) -> AnyColumn<Item> {
    AnyColumn(title, width: width, alignment: alignment, tooltip: tooltip, headerAccessory: headerAccessory, cell: cell)
}

private struct CellFrameKey: PreferenceKey {
    static var defaultValue: [String: CGRect] = [:]
    static func reduce(value: inout [String: CGRect], nextValue: () -> [String: CGRect]) {
        value.merge(nextValue()) { _, new in new }
    }
}

private struct CellOverflowKey: PreferenceKey {
    static var defaultValue: Set<String> = []
    static func reduce(value: inout Set<String>, nextValue: () -> Set<String>) {
        value.formUnion(nextValue())
    }
}

struct DataTable<Item: Identifiable>: View {
    let items: [Item]
    let columns: [AnyColumn<Item>]
    let hasMore: Bool
    let onLoadMore: (() async -> Void)?
    let onRowTap: ((Item) -> Void)?
    let filterContent: (() -> AnyView)?

    @State private var columnWidths: [String: CGFloat] = [:]
    @State private var dragStartWidths: [String: CGFloat] = [:]

    @State private var cellFrames: [String: CGRect] = [:]
    @State private var overflowKeys: Set<String> = []
    @State private var tooltipText: String?
    @State private var tooltipFrame: CGRect = .zero
    @State private var showTooltip = false
    @State private var hoverTask: Task<Void, Never>?

    init(
        items: [Item],
        hasMore: Bool = false,
        onLoadMore: (() async -> Void)? = nil,
        onRowTap: ((Item) -> Void)? = nil,
        filterContent: (() -> AnyView)? = nil,
        @ArrayBuilder<AnyColumn<Item>> columns: () -> [AnyColumn<Item>]
    ) {
        self.items = items
        self.hasMore = hasMore
        self.columns = columns()
        self.onLoadMore = onLoadMore
        self.onRowTap = onRowTap
        self.filterContent = filterContent
    }

    var body: some View {
        GeometryReader { geo in
            let totalWidth = geo.size.width
            ZStack(alignment: .topLeading) {
                VStack(spacing: 0) {
                    headerRow(totalWidth: totalWidth)
                    Divider()
                    if let filter = filterContent {
                        filter()
                        Divider()
                    }
                    bodyScroll(totalWidth: totalWidth)
                }

                if showTooltip, let text = tooltipText {
                    tooltipBubble(text)
                        .position(x: tooltipFrame.midX, y: tooltipFrame.minY - 16)
                }
            }
            .coordinateSpace(name: "dataTable")
            .onPreferenceChange(CellFrameKey.self) { frames in
                cellFrames = frames
            }
            .onPreferenceChange(CellOverflowKey.self) { keys in
                overflowKeys = keys
            }
        }
    }

    private func headerRow(totalWidth: CGFloat) -> some View {
        HStack(spacing: 0) {
            ForEach(columns, id: \.id) { col in
                headerCell(col, totalWidth: totalWidth)
            }
        }
        .frame(height: 32)
        .background(.ultraThinMaterial)
    }

    private func headerCell(_ col: AnyColumn<Item>, totalWidth: CGFloat) -> some View {
        let w = currentWidth(for: col, totalWidth: totalWidth)
        return HStack(spacing: 2) {
            if let accessory = col.headerAccessory {
                accessory(w)
            }
            Text(col.title)
                .font(.caption.weight(.medium))
                .foregroundColor(.secondary)
                .lineLimit(1)
                .frame(maxWidth: .infinity, alignment: .leading)
        }
        .frame(width: w)
        .padding(.horizontal, 12)
        .overlay(alignment: .trailing) {
            Color.clear
                .frame(width: 6)
                .contentShape(Rectangle())
                .highPriorityGesture(
                    DragGesture(minimumDistance: 2)
                        .onChanged { value in
                            if dragStartWidths[col.id] == nil {
                                dragStartWidths[col.id] = columnWidths[col.id] ?? widthValue(col.initialWidth, total: totalWidth)
                            }
                            let base = dragStartWidths[col.id] ?? 0
                            let newWidth = max(col.minWidth, min(col.maxWidth, base + value.translation.width))
                            columnWidths[col.id] = newWidth
                        }
                        .onEnded { _ in
                            dragStartWidths[col.id] = nil
                        }
                )
        }
    }

    private func bodyScroll(totalWidth: CGFloat) -> some View {
        ScrollView {
            LazyVStack(spacing: 0) {
                ForEach(Array(items.enumerated()), id: \.element.id) { index, item in
                    rowView(item: item, index: index, totalWidth: totalWidth)
                    Divider()
                }

                if hasMore {
                    ProgressView()
                        .padding()
                        .onAppear { Task { await onLoadMore?() } }
                }
            }
        }
    }

    private func rowView(item: Item, index: Int, totalWidth: CGFloat) -> some View {
        HStack(spacing: 0) {
            ForEach(columns, id: \.id) { col in
                let w = currentWidth(for: col, totalWidth: totalWidth)
                let cellKey = key(item: item, column: col)

                col.cell(item)
                    .frame(width: w, alignment: alignmentToSwiftUI(col.alignment))
                    .padding(.horizontal, 12)
                    .background(
                        GeometryReader { geo in
                            let frame = geo.frame(in: .named("dataTable"))
                            let tipText = col.tooltip?(item) ?? ""
                            let isOverflow = !tipText.isEmpty && measureOverflow(tipText, in: w)
                            Color.clear
                                .preference(key: CellFrameKey.self, value: [cellKey: frame])
                                .preference(key: CellOverflowKey.self, value: isOverflow ? [cellKey] : [])
                        }
                    )
                    .onHover { hovering in
                        handleHover(hovering, item: item, column: col, key: cellKey)
                    }
            }
        }
        .padding(.vertical, 6)
        .background(rowBackground(index: index))
        .contentShape(Rectangle())
        .onTapGesture { onRowTap?(item) }
    }

    private func measureOverflow(_ text: String, in width: CGFloat) -> Bool {
#if os(macOS)
        let font = NSFont.systemFont(ofSize: NSFont.systemFontSize)
        let attrs: [NSAttributedString.Key: Any] = [.font: font]
        let size = (text as NSString).size(withAttributes: attrs)
        return size.width > width
#else
        return text.count > Int(width / 7)
#endif
    }

    private func key(item: Item, column: AnyColumn<Item>) -> String {
        "\(item.id)-\(column.id)"
    }

    private func handleHover(_ hovering: Bool, item: Item, column: AnyColumn<Item>, key: String) {
        hoverTask?.cancel()
        if hovering {
            let tipText = column.tooltip?(item) ?? ""
            guard !tipText.isEmpty, overflowKeys.contains(key) else { return }
            hoverTask = Task {
                try? await Task.sleep(nanoseconds: 200_000_000)
                guard !Task.isCancelled else { return }
                await MainActor.run {
                    guard let frame = cellFrames[key] else { return }
                    tooltipText = tipText
                    tooltipFrame = frame
                    showTooltip = true
                }
            }
        } else {
            tooltipText = nil
            showTooltip = false
        }
    }

    private func tooltipBubble(_ text: String) -> some View {
        Text(text)
            .font(.caption2)
            .foregroundColor(.white)
            .padding(.horizontal, 10)
            .padding(.vertical, 6)
            .background(.black.opacity(0.75), in: RoundedRectangle(cornerRadius: 6))
            .allowsHitTesting(false)
    }

    private func rowBackground(index: Int) -> Color {
        index % 2 == 0 ? .clear : Color.primary.opacity(0.04)
    }

    private func currentWidth(for col: AnyColumn<Item>, totalWidth: CGFloat) -> CGFloat {
        if let stored = columnWidths[col.id] {
            return stored
        }
        return widthValue(col.initialWidth, total: totalWidth)
    }

    private func widthValue(_ width: ColumnWidth, total: CGFloat) -> CGFloat {
        switch width {
        case .fixed(let w): return w
        case .flexible: return flexibleWidth(total: total)
        }
    }

    private func flexibleWidth(total: CGFloat) -> CGFloat {
        let fixedTotal = columns.reduce(0) { acc, col in
            if case .fixed(let w) = col.initialWidth { return acc + w + 24 }
            return acc
        }
        let flexibleCount = columns.filter {
            if case .flexible = $0.initialWidth { return true }
            return false
        }.count
        guard flexibleCount > 0 else { return total }
        return max((total - fixedTotal) / CGFloat(flexibleCount) - 24, 40)
    }

    private func alignmentToSwiftUI(_ alignment: HorizontalAlignment) -> Alignment {
        switch alignment {
        case .leading: return .leading
        case .trailing: return .trailing
        default: return .center
        }
    }
}

@resultBuilder
enum ArrayBuilder<T> {
    static func buildBlock(_ components: T...) -> [T] {
        components
    }
    static func buildIf(_ component: T?) -> T? {
        component
    }
    static func buildEither(first component: T) -> T {
        component
    }
    static func buildEither(second component: T) -> T {
        component
    }
}
