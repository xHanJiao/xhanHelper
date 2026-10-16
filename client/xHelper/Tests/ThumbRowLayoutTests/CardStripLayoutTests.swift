import XCTest
@testable import ThumbRowLayout

/// 卡片条（ArtifactCardStrip）的折叠排布：格子（卡片）统一宽、**尾格（「＋N」）可以更窄**，
/// 而且**至少露一张卡**（卡片 240pt 比缩略图大得多，按「塞不下就 0 张」算会在窄窗口退化成
/// 只剩一个「＋N」格、一张卡都没有）。这里钉住三条：不溢出、不白藏、至少一张。
///
/// 尾格宽度**用返回值里那个**，不在这里复刻钳制公式——否则改了实现忘了改测试，两边一起错也能绿。
final class CardStripLayoutTests: XCTestCase {
    private let spacing: CGFloat = 10
    private let card: CGFloat = 240
    private let tile: CGFloat = 72
    /// 恒不溢出的下界：比「一张卡 + 间距」还窄就无从谈排布
    private let narrowest: CGFloat = 250

    /// 一行的实际占宽（尾格用实现算出来的那个宽度）
    private func rowWidth(visible: Int, trailing: CGFloat) -> CGFloat {
        guard visible > 0 else { return trailing }
        return CGFloat(visible) * card + CGFloat(visible - 1) * spacing + spacing + trailing
    }

    private func layout(_ available: CGFloat, _ count: Int, trailingCell: CGFloat = 72) -> (visible: Int, overflow: Bool, trailing: CGFloat) {
        ThumbRowLayout.collapsed(
            available: available, cell: card, spacing: spacing,
            trailingCell: trailingCell, count: count
        )
    }

    func testNeverOverflows() {
        for trailingCell in [card, tile] {
            for available in stride(from: narrowest, through: 1200, by: 1) {
                for count in 2...20 {
                    let r = layout(available, count, trailingCell: trailingCell)
                    guard r.overflow else { continue }
                    let width = rowWidth(visible: r.visible, trailing: r.trailing)
                    XCTAssertLessThanOrEqual(
                        width, available + 0.001,
                        "尾格=\(trailingCell) 溢出：count=\(count) available=\(available) visible=\(r.visible) 占宽=\(width)"
                    )
                }
            }
        }
    }

    /// 卡片比缩略图大得多：**任何宽度下都必须至少露一张卡**（否则只剩一个「＋N」格）
    func testAlwaysShowsAtLeastOneCard() {
        for available in stride(from: CGFloat(120), through: 1200, by: 1) {
            for count in 2...20 {
                let r = layout(available, count)
                XCTAssertGreaterThanOrEqual(
                    r.visible, 1, "available=\(available) count=\(count) 一张卡都没露"
                )
            }
        }
    }

    /// 只要还能多露一张就该露：溢出且 visible < count-1 时，再加一张必须超宽
    func testDoesNotHideCardsThatFit() {
        for trailingCell in [card, tile] {
            for available in stride(from: narrowest, through: 1200, by: 1) {
                for count in 3...20 {
                    let r = layout(available, count, trailingCell: trailingCell)
                    guard r.overflow, r.visible < count - 1 else { continue }
                    let wider = rowWidth(visible: r.visible + 1, trailing: r.trailing)
                    XCTAssertGreaterThan(
                        wider, available,
                        "尾格=\(trailingCell) 白藏了：count=\(count) available=\(available) visible=\(r.visible)"
                    )
                }
            }
        }
    }

    /// 尾格宽度不能超过给的宽、也不能是负的；放得下时就是原宽
    func testTrailingWidthShape() {
        for available in stride(from: narrowest, through: 1200, by: 1) {
            let r = layout(available, 8)
            XCTAssertGreaterThanOrEqual(r.trailing, 0, "available=\(available)")
            XCTAssertLessThanOrEqual(r.trailing, tile, "available=\(available)")
        }
        XCTAssertEqual(layout(620, 3).trailing, tile)
    }

    /// 全部塞得下时不出尾格
    func testNoTrailingCellWhenEverythingFits() {
        let r = layout(620, 2)
        XCTAssertEqual(r.visible, 2)
        XCTAssertFalse(r.overflow)
        XCTAssertEqual(r.trailing, 0)
    }

    /// 单张 / 宽度未测出：不折叠（交给调用方按原样渲染）
    func testNoCollapseForSingleOrUnmeasured() {
        for available in [CGFloat(0), 620] {
            let r = layout(available, 1)
            XCTAssertEqual(r.visible, 1)
            XCTAssertFalse(r.overflow)
        }
    }

    /// 窄尾格不该比等宽尾格露得少
    func testNarrowTrailingCellNeverShowsFewer() {
        for available in stride(from: narrowest, through: 1200, by: 7) {
            for count in 2...20 {
                XCTAssertGreaterThanOrEqual(
                    layout(available, count).visible,
                    layout(available, count, trailingCell: card).visible,
                    "available=\(available) count=\(count)"
                )
            }
        }
    }

    /// 与同宽的旧重载（缩略图行那套）在两者都退化为 ≥1 张的区间里结果一致
    func testMatchesUniformOverloadWhenBothShowCards() {
        for available in stride(from: CGFloat(2 * card + spacing), through: 1200, by: 1) {
            for count in 2...20 {
                let narrow = layout(available, count, trailingCell: card)
                let uniform = ThumbRowLayout.collapsed(
                    available: available, thumb: card, spacing: spacing,
                    trailingCells: 0, count: count
                )
                XCTAssertEqual(narrow.visible, uniform.visible, "available=\(available) count=\(count)")
                XCTAssertEqual(narrow.overflow, uniform.overflow, "available=\(available) count=\(count)")
            }
        }
    }
}
