import Foundation

/// 缩略图行溢出折叠的排布算术。
///
/// 单独抽出来是因为这层只有数字、没有视图：可以脱离 SwiftUI 用 `swift test` 覆盖
/// （见 `Tests/ThumbRowLayoutTests`）。视图那边只负责照着算出来的格子数去画。
///
/// 「行尾附加格」指折叠行尾部那些和缩略图同尺寸的方格：托盘的「+」和「清空」共 2 格，
/// 消息图片条没有（0 格）。它们按格数传进来，宽度由缩略图边长推出来。
enum ThumbRowLayout {
    /// 行尾附加格占的总宽（格与格之间的间距也算在内）
    static func trailingWidth(cells: Int, thumb: CGFloat, spacing: CGFloat) -> CGFloat {
        guard cells > 0 else { return 0 }
        return CGFloat(cells) * thumb + CGFloat(cells - 1) * spacing
    }

    /// 折叠态不放「+N」时一行能放几张。
    /// 有附加格时它是最后一段、和缩略图之间有一个间距；没有时最后一个缩略图没有尾间距。
    static func maxPerRow(available: CGFloat, thumb: CGFloat, spacing: CGFloat, trailingCells: Int) -> Int {
        let trailing = trailingWidth(cells: trailingCells, thumb: thumb, spacing: spacing)
        let usable = trailing > 0 ? available - trailing : available + spacing
        return max(0, Int(usable / (thumb + spacing)))
    }

    /// 展开态每行几张（此时「+N」与行尾附加格都参与排布，不预留）。
    /// 必须与 `GridItem(.adaptive(minimum:maximum:))` 的算法一致，否则行数算错、滚动区高度对不上。
    static func expandedPerRow(available: CGFloat, thumb: CGFloat, spacing: CGFloat) -> Int {
        max(1, Int((available + spacing) / (thumb + spacing)))
    }

    /// 折叠态每行实际怎么排：露出几张、末尾要不要放「+N」。
    ///
    /// `maxVisible` 是可选的固定上限（消息图片条固定露 3 张）：即使一行塞得下更多也提前折叠。
    /// 它是上限不是承诺——窄到让不出「+N」那一格时只能少露几张，排布绝不能溢出。
    /// 传 nil 就是纯按宽度折叠（草稿托盘）。
    static func collapsed(
        available: CGFloat,
        thumb: CGFloat,
        spacing: CGFloat,
        trailingCells: Int,
        count: Int,
        maxVisible: Int? = nil
    ) -> (visible: Int, overflow: Bool) {
        // 宽度还没测出来时先不折叠，免得用兜底值算出个假「+N」再跳一下
        guard available > 0 else { return (count, false) }
        let per = maxPerRow(
            available: available, thumb: thumb, spacing: spacing, trailingCells: trailingCells
        )
        // 溢出要让出一格给「+N」，所以露出的张数先砍到 per-1，再套上限
        let cap = min(maxVisible ?? Int.max, max(0, per - 1))
        guard count > per || count > (maxVisible ?? Int.max) else { return (count, false) }
        return (cap, true)
    }

    /// 折叠态一行占多宽（测试用它断言"塞得进可用宽"，也就是界面不溢出）
    static func collapsedWidth(
        available: CGFloat,
        thumb: CGFloat,
        spacing: CGFloat,
        trailingCells: Int,
        count: Int,
        maxVisible: Int? = nil
    ) -> CGFloat {
        let (visible, overflow) = collapsed(
            available: available, thumb: thumb, spacing: spacing,
            trailingCells: trailingCells, count: count, maxVisible: maxVisible
        )
        let cells = visible + (overflow ? 1 : 0)
        var width = CGFloat(cells) * thumb
        if cells > 1 { width += CGFloat(cells - 1) * spacing }
        if cells > 0, trailingCells > 0 {
            width += spacing
            width += trailingWidth(cells: trailingCells, thumb: thumb, spacing: spacing)
        }
        return width
    }

    /// 展开态总格数（含「收起」格与行尾附加格）
    static func expandedCellCount(count: Int, trailingCells: Int) -> Int {
        count + 1 + max(0, trailingCells)
    }

    /// 展开态行数（未封顶）
    static func expandedRows(
        available: CGFloat,
        thumb: CGFloat,
        spacing: CGFloat,
        trailingCells: Int,
        count: Int
    ) -> Int {
        let per = expandedPerRow(available: available, thumb: thumb, spacing: spacing)
        let cells = expandedCellCount(count: count, trailingCells: trailingCells)
        return max(1, (cells + per - 1) / per)
    }

    /// 整行的高度：折叠 1 行；展开按行数算、封顶 `maxRows`（超出靠内部滚动）。
    /// GeometryReader 纵向也是贪婪的，视图必须用这个值把高度钉住。
    static func height(
        available: CGFloat,
        thumb: CGFloat,
        spacing: CGFloat,
        trailingCells: Int,
        maxRows: Int,
        count: Int,
        expanded: Bool
    ) -> CGFloat {
        let rows = expanded
            ? min(expandedRows(
                available: available, thumb: thumb, spacing: spacing,
                trailingCells: trailingCells, count: count
            ), maxRows)
            : 1
        return CGFloat(rows) * thumb + CGFloat(rows - 1) * spacing
    }
}
