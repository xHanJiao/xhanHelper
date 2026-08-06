import SwiftUI

/// 收藏项拖拽重排修饰器（收藏夹栏 / 收藏夹面板共用）。
/// 拖到目标项沿主轴的前半段插入其前、后半段插入其后；拖拽悬停时在插入位置显示指示线。
struct BookmarkReorderModifier: ViewModifier {
    /// 布局主轴方向：.horizontal 横向（收藏夹栏），.vertical 纵向（收藏夹面板）
    enum Axis { case horizontal, vertical }

    let bookmark: BookmarkStore.Bookmark
    let axis: Axis

    @ObservedObject private var store = BookmarkStore.shared
    @State private var size: CGFloat = 40
    @State private var hoverOffset: CGFloat?
    @State private var isDropTarget = false

    private var showsLeading: Bool {
        guard isDropTarget, let h = hoverOffset else { return false }
        return h < size / 2
    }

    private var showsTrailing: Bool {
        guard isDropTarget, let h = hoverOffset else { return false }
        return h >= size / 2
    }

    func body(content: Content) -> some View {
        content
            .draggable(bookmark.id.uuidString)
            .dropDestination(for: String.self) { ids, location in
                guard let s = ids.first, let dragged = UUID(uuidString: s) else { return false }
                if dragged != bookmark.id,
                   let idx = store.bookmarks.firstIndex(where: { $0.id == bookmark.id }) {
                    let along = axis == .horizontal ? location.x : location.y
                    let target = along < size / 2 ? idx : idx + 1
                    Task { @MainActor in store.move(id: dragged, toIndex: target) }
                }
                return true
            } isTargeted: { isDropTarget = $0 }
            .onGeometryChange(for: CGFloat.self) { proxy in
                axis == .horizontal ? proxy.size.width : proxy.size.height
            } action: { size = $0 }
            .onContinuousHover { phase in
                if case .active(let location) = phase {
                    hoverOffset = axis == .horizontal ? location.x : location.y
                } else {
                    hoverOffset = nil
                }
            }
            .overlay {
                if axis == .horizontal {
                    HStack(spacing: 0) {
                        indicator(edge: .leading)
                        Spacer(minLength: 0)
                        indicator(edge: .trailing)
                    }
                } else {
                    VStack(spacing: 0) {
                        indicator(edge: .leading)
                        Spacer(minLength: 0)
                        indicator(edge: .trailing)
                    }
                }
            }
    }

    @ViewBuilder
    private func indicator(edge: Edge) -> some View {
        Group {
            if axis == .horizontal {
                Rectangle().fill(Color.accentColor).frame(width: 2)
            } else {
                Rectangle().fill(Color.accentColor).frame(height: 2)
            }
        }
        .opacity((edge == .leading ? showsLeading : showsTrailing) ? 1 : 0)
    }
}

extension View {
    func bookmarkReorderable(_ bookmark: BookmarkStore.Bookmark, axis: BookmarkReorderModifier.Axis) -> some View {
        modifier(BookmarkReorderModifier(bookmark: bookmark, axis: axis))
    }
}
