import XCTest
@testable import ThumbRowLayout

/// 缩略图行折叠排布：两个真实配置 ——
/// 草稿托盘（缩略图 56pt，行尾「+」+「清空」共 2 格）与消息图片条（88pt，无行尾）。
/// 断言四件事：折叠态塞得进可用宽、展开态列数塞得进可用宽、折叠态没有白白藏起放得下的图、
/// 钉住的高度与行数一致。
final class ThumbRowLayoutTests: XCTestCase {
    private let spacing: CGFloat = 8
    /// 本 App 输入框托盘最窄时的可用宽：窗口最小 380 → 中栏最小 340 → 减去两侧 24 内边距
    private let narrowest: CGFloat = 292

    private let tray = (thumb: CGFloat(56), trailingCells: 2, name: "托盘")
    private let strip = (thumb: CGFloat(88), trailingCells: 0, name: "图片条")
    private var configs: [(thumb: CGFloat, trailingCells: Int, name: String)] { [tray, strip] }

    func testCollapsedRowNeverOverflows() {
        for config in configs {
            for available in stride(from: narrowest, through: 760, by: 1) {
                for count in 1...20 {
                    let width = ThumbRowLayout.collapsedWidth(
                        available: available, thumb: config.thumb, spacing: spacing,
                        trailingCells: config.trailingCells, count: count
                    )
                    XCTAssertLessThanOrEqual(
                        width, available + 0.001,
                        "\(config.name) 溢出：count=\(count) available=\(available) 占宽=\(width)"
                    )
                }
            }
        }
    }

    func testExpandedColumnCountFitsWidth() {
        for config in configs {
            for available in stride(from: narrowest, through: 760, by: 1) {
                let cols = ThumbRowLayout.expandedPerRow(
                    available: available, thumb: config.thumb, spacing: spacing
                )
                let width = CGFloat(cols) * config.thumb + CGFloat(cols - 1) * spacing
                XCTAssertLessThanOrEqual(
                    width, available + 0.001,
                    "\(config.name) 展开列数超宽：cols=\(cols) available=\(available)"
                )
            }
        }
    }

    /// 折叠态必须尽量多露图：藏起来的图，再露一张（不加「+N」）一定放不下
    func testCollapsedRowHidesNothingThatWouldFit() {
        for config in configs {
            for available in stride(from: narrowest, through: 760, by: 1) {
                for count in 1...20 {
                    let layout = ThumbRowLayout.collapsed(
                        available: available, thumb: config.thumb, spacing: spacing,
                        trailingCells: config.trailingCells, count: count
                    )
                    guard layout.visible < count else { continue }

                    let visible = layout.visible + 1
                    let overflow = count > visible
                    let cells = visible + (overflow ? 1 : 0)
                    var width = CGFloat(cells) * config.thumb
                    if cells > 1 { width += CGFloat(cells - 1) * spacing }
                    if cells > 0, config.trailingCells > 0 {
                        width += spacing
                        width += ThumbRowLayout.trailingWidth(
                            cells: config.trailingCells, thumb: config.thumb, spacing: spacing
                        )
                    }
                    XCTAssertGreaterThan(
                        width, available + 0.001,
                        "\(config.name) 少露了一张：count=\(count) available=\(available) 当前=\(layout.visible)"
                    )
                }
            }
        }
    }

    /// 钉住的高度必须正好装下行数：折叠 1 行；展开封顶 maxRows 行
    func testHeightMatchesRowCount() {
        for config in configs {
            for available in stride(from: CGFloat(0), through: 760, by: 1) {
                for count in 1...20 {
                    let collapsed = ThumbRowLayout.height(
                        available: available, thumb: config.thumb, spacing: spacing,
                        trailingCells: config.trailingCells, maxRows: 3, count: count, expanded: false
                    )
                    XCTAssertEqual(collapsed, config.thumb, "\(config.name) 折叠态高应为一行")

                    let rows = ThumbRowLayout.expandedRows(
                        available: available, thumb: config.thumb, spacing: spacing,
                        trailingCells: config.trailingCells, count: count
                    )
                    let expanded = ThumbRowLayout.height(
                        available: available, thumb: config.thumb, spacing: spacing,
                        trailingCells: config.trailingCells, maxRows: 3, count: count, expanded: true
                    )
                    let expected = CGFloat(min(rows, 3)) * config.thumb
                        + CGFloat(min(rows, 3) - 1) * spacing
                    XCTAssertEqual(expanded, expected, "\(config.name) 展开高度与行数不符：rows=\(rows)")
                    XCTAssertGreaterThanOrEqual(expanded, config.thumb, "\(config.name) 展开后不应比折叠更矮")

                    // maxRows: nil = 不封顶（消息图片条），展开就该是全高、不套滚动
                    let unbounded = ThumbRowLayout.height(
                        available: available, thumb: config.thumb, spacing: spacing,
                        trailingCells: config.trailingCells, maxRows: nil, count: count, expanded: true
                    )
                    XCTAssertEqual(
                        unbounded, CGFloat(rows) * config.thumb + CGFloat(rows - 1) * spacing,
                        "\(config.name) 不封顶时展开高度应等于全部行数：rows=\(rows)"
                    )
                    XCTAssertGreaterThanOrEqual(unbounded, expanded)
                }
            }
        }
    }

    /// 展开成一行时，视图会改走 HStack（网格吃满整宽、靠右会失效）。HStack 按内容定宽、
    /// 不像网格会裁剪，所以这里必须真的塞得进 —— 若 `expandedPerRow` 将来算多一格就会溢出
    func testSingleRowExpandedFitsWidth() {
        for config in configs {
            for available in stride(from: CGFloat(0), through: 760, by: 1) {
                for count in 1...20 {
                    let rows = ThumbRowLayout.expandedRows(
                        available: available, thumb: config.thumb, spacing: spacing,
                        trailingCells: config.trailingCells, count: count
                    )
                    guard rows == 1 else { continue }
                    let cells = ThumbRowLayout.expandedCellCount(
                        count: count, trailingCells: config.trailingCells
                    )
                    let width = CGFloat(cells) * config.thumb + CGFloat(cells - 1) * spacing
                    XCTAssertLessThanOrEqual(
                        width, available + 0.001,
                        "\(config.name) 单行展开溢出：count=\(count) available=\(available) 占宽=\(width)"
                    )
                }
            }
        }
    }

    /// 回归：托盘在 240~303pt 时 `maxPerRow` 曾多算一格（漏减行尾间距），折叠行会溢出 8pt
    func testTrayMaxPerRowAccountsForTrailingSpacing() {
        XCTAssertEqual(
            ThumbRowLayout.maxPerRow(available: 240, thumb: 56, spacing: 8, trailingCells: 2), 1
        )
        XCTAssertEqual(
            ThumbRowLayout.maxPerRow(available: 292, thumb: 56, spacing: 8, trailingCells: 2), 2
        )
        XCTAssertEqual(
            ThumbRowLayout.maxPerRow(available: 368, thumb: 56, spacing: 8, trailingCells: 2), 3
        )
        let width = ThumbRowLayout.collapsedWidth(
            available: 292, thumb: 56, spacing: 8, trailingCells: 2, count: 18
        )
        XCTAssertLessThanOrEqual(width, 292)
    }

    /// 只有「+」一格时能多露一张
    func testSingleTrailingCellFitsOneMoreThumb() {
        XCTAssertEqual(
            ThumbRowLayout.maxPerRow(available: 292, thumb: 56, spacing: 8, trailingCells: 1), 3
        )
        XCTAssertEqual(
            ThumbRowLayout.maxPerRow(available: 292, thumb: 56, spacing: 8, trailingCells: 2), 2
        )
    }

    /// 无行尾时最后一个缩略图没有尾间距，能多放一张
    func testStripUsesTrailingSlotForOneMoreThumb() {
        XCTAssertEqual(
            ThumbRowLayout.maxPerRow(available: 376, thumb: 88, spacing: 8, trailingCells: 0), 4
        )
        XCTAssertEqual(
            ThumbRowLayout.maxPerRow(available: 375, thumb: 88, spacing: 8, trailingCells: 0), 3
        )
    }

    func testWidthNotMeasuredYetDoesNotCollapse() {
        let layout = ThumbRowLayout.collapsed(
            available: 0, thumb: 56, spacing: 8, trailingCells: 2, count: 20
        )
        XCTAssertEqual(layout.visible, 20, "宽度未测出来时不该折叠出一个假「+N」")
        XCTAssertFalse(layout.overflow)
    }

    func testSmallCountsNeverShowOverflowCell() {
        for config in configs {
            for available in stride(from: narrowest, through: 760, by: 1) {
                let per = ThumbRowLayout.maxPerRow(
                    available: available, thumb: config.thumb, spacing: spacing,
                    trailingCells: config.trailingCells
                )
                guard per >= 1 else { continue }
                let layout = ThumbRowLayout.collapsed(
                    available: available, thumb: config.thumb, spacing: spacing,
                    trailingCells: config.trailingCells, count: per
                )
                XCTAssertFalse(
                    layout.overflow,
                    "\(config.name) 刚好放得下却出了「+N」：available=\(available)"
                )
                XCTAssertEqual(layout.visible, per)
            }
        }
    }

    func testTrailingWidthIncludesInnerSpacing() {
        XCTAssertEqual(ThumbRowLayout.trailingWidth(cells: 0, thumb: 56, spacing: 8), 0)
        XCTAssertEqual(ThumbRowLayout.trailingWidth(cells: 1, thumb: 56, spacing: 8), 56)
        XCTAssertEqual(ThumbRowLayout.trailingWidth(cells: 2, thumb: 56, spacing: 8), 120)
    }

    func testExpandedCellCountIncludesCollapseCellAndTrailing() {
        XCTAssertEqual(ThumbRowLayout.expandedCellCount(count: 20, trailingCells: 2), 23)
        XCTAssertEqual(ThumbRowLayout.expandedCellCount(count: 20, trailingCells: 0), 21)
    }

    func testExpandedRowsNeedVerticalScrollBeyondThreeRows() {
        // 最窄托盘 292pt：每行 4 格（4×56 + 3×8 = 248 ≤ 292，5 格要 312 放不下）
        // 20 张 + 「收起」+「+」+「清空」= 23 格 → 6 行，超过 3 行封顶
        XCTAssertEqual(
            ThumbRowLayout.expandedPerRow(available: narrowest, thumb: 56, spacing: 8), 4
        )
        let rows = ThumbRowLayout.expandedRows(
            available: narrowest, thumb: 56, spacing: 8, trailingCells: 2, count: 20
        )
        XCTAssertEqual(rows, 6)
        // 宽窗口：一行放得下 → 3 行，不滚
        let wideRows = ThumbRowLayout.expandedRows(
            available: 720, thumb: 56, spacing: 8, trailingCells: 2, count: 20
        )
        XCTAssertEqual(wideRows, 3)
    }

    // MARK: - 消息图片条的固定上限（最多露 3 张）

    private func stripLayout(available: CGFloat, count: Int) -> (visible: Int, overflow: Bool) {
        ThumbRowLayout.collapsed(
            available: available, thumb: 88, spacing: 8, trailingCells: 0,
            count: count, maxVisible: 3
        )
    }

    /// 宽窗口（一行放得下 7 格）也不多露：第 4 张起就收成「+N」
    func testStripCapsCollapsedThumbsAtThree() {
        XCTAssertEqual(stripLayout(available: 700, count: 10).visible, 3)
        XCTAssertTrue(stripLayout(available: 700, count: 10).overflow)
        XCTAssertEqual(stripLayout(available: 700, count: 4).visible, 3)
        XCTAssertTrue(stripLayout(available: 700, count: 4).overflow)
        // 3 张以内照旧全露、不出「+N」
        XCTAssertEqual(stripLayout(available: 700, count: 3).visible, 3)
        XCTAssertFalse(stripLayout(available: 700, count: 3).overflow)
        XCTAssertEqual(stripLayout(available: 700, count: 1).visible, 1)
        XCTAssertFalse(stripLayout(available: 700, count: 1).overflow)
    }

    /// 上限是上限不是承诺：窗口窄到让不出「+N」那格时只能少露，绝不能溢出
    func testStripCapNeverOverflows() {
        for available in stride(from: CGFloat(88), through: 760, by: 1) {
            let width = ThumbRowLayout.collapsedWidth(
                available: available, thumb: 88, spacing: 8, trailingCells: 0,
                count: 20, maxVisible: 3
            )
            XCTAssertLessThanOrEqual(
                width, available + 0.001, "available=\(available) 占宽=\(width)"
            )
            XCTAssertLessThanOrEqual(
                stripLayout(available: available, count: 20).visible, 3,
                "available=\(available) 超过了固定上限"
            )
        }
        // 一行能放 4 格（376pt）起，始终正好 3 张 + 「+N」
        for available in stride(from: CGFloat(376), through: 760, by: 1) {
            XCTAssertEqual(stripLayout(available: available, count: 20).visible, 3)
            XCTAssertTrue(stripLayout(available: available, count: 20).overflow)
        }
        // 300pt：一行 3 格（3×88 + 2×8 = 280 ≤ 300，4 格要 376）→ 2 张 + 「+18」
        let narrow = stripLayout(available: 300, count: 20)
        XCTAssertEqual(narrow.visible, 2)
        XCTAssertTrue(narrow.overflow)
    }

    /// 宽度还没测出来时不折叠，上限也一样（否则首帧会闪一个假「+N」）
    func testCapDoesNotCollapseBeforeWidthIsMeasured() {
        let layout = stripLayout(available: 0, count: 20)
        XCTAssertEqual(layout.visible, 20)
        XCTAssertFalse(layout.overflow)
    }
}
