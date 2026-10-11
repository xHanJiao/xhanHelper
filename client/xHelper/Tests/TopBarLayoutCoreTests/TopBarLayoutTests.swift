import XCTest
@testable import TopBarLayoutCore

/// 顶栏自适应降级：按可用宽选档 + 各档内在宽。
///
/// 断言的目标是**行为而非数字**：装得下的最不降级档、窗口底线（双栏 778 / PiP 750）下中心槽组必须
/// 让位、档位随可用宽单调不增、dev 徽标先被收掉。数字只在第一条里钉住量级，防止常量漂移后静默走偏。
final class TopBarLayoutTests: XCTestCase {
    /// 右栏开时的窗口底线（= `Layout.rightSidebarOpenMinContentWidth`）。
    private let twoColumnFloor = Layout.rightSidebarOpenMinContentWidth
    /// 无右栏 / PiP 的窗口底线。
    private let singleColumnFloor = Layout.minWindowContentWidth

    /// 右栏开着浏览器（中心槽 7 键）+ 导航组 5 键 + 工具组 4 键（侧栏已开，最大化按钮可见）。
    private var browserModel: TopBarWidthModel {
        TopBarWidthModel(centerGroups: [7], navGroupButtons: 5, utilityButtons: 4)
    }
    /// 右栏开着文档详情（中心槽 3 键）。
    private var documentModel: TopBarWidthModel {
        TopBarWidthModel(centerGroups: [3], navGroupButtons: 5, utilityButtons: 4)
    }
    /// 右栏开着非浏览器/文档面板（中心槽空）。
    private var plainPanelModel: TopBarWidthModel {
        TopBarWidthModel(centerGroups: [], navGroupButtons: 5, utilityButtons: 4)
    }

    func testWideWindowKeepsEverything() {
        let plan = TopBarLayout.plan(available: 1200, model: browserModel)
        XCTAssertEqual(plan.level, .full)
        XCTAssertTrue(plan.showsCenter)
        XCTAssertFalse(plan.showsDevChip) // 非 dev 构建本就没有徽标
        XCTAssertFalse(plan.showsOverflow)
    }

    /// 全量内在宽 ≈ 1022（浏览器态）：窗口宽 1200 时当然装得下，778 时必须让位。
    func testFullWidthMagnitude() {
        let full = TopBarLayout.width(level: .full, model: browserModel)
        XCTAssertGreaterThan(full, 950)
        XCTAssertLessThan(full, 1100)
        // 无浏览器时顶栏本来就装得进双栏底线
        XCTAssertLessThanOrEqual(TopBarLayout.width(level: .full, model: plainPanelModel), twoColumnFloor)
    }

    /// 双栏底线：中心槽组（浏览器/文档）必须收进 ⋯，且选出的档必须真装得进底线。
    /// 注：收掉中心槽后仍有 800（240 标签 + 三组）—— 778 装不下，所以会一路落到末档（标签也收到 160）。
    func testCenterGroupFoldsAtTwoColumnFloor() {
        for model in [browserModel, documentModel] {
            let plan = TopBarLayout.plan(available: twoColumnFloor, model: model)
            XCTAssertFalse(plan.showsCenter, "778 下中心槽组应让位")
            XCTAssertTrue(plan.showsOverflow)
            XCTAssertLessThanOrEqual(
                TopBarLayout.width(level: plan.level, model: model),
                twoColumnFloor - Layout.topBarDegradeMargin
            )
        }
    }

    /// 中心槽空时双栏底线不触发射降级（顶栏本来就装得下），标签上限保持 240。
    func testNoCenterGroupAtFloorStaysFull() {
        let plan = TopBarLayout.plan(available: twoColumnFloor, model: plainPanelModel)
        XCTAssertEqual(plan.level, .full)
        XCTAssertEqual(plan.switcherLabelCap, Layout.topBarSwitcherLabelCap)
        XCTAssertFalse(plan.showsOverflow)
    }

    /// PiP（750）：浏览器态要收中心槽组 + 收标签，且必须真装得进。
    /// 中心槽空时也装不下 240 的标签（全量 753 > 742），所以照样落末档 —— 只收标签。
    func testPipFloorFoldsCenterAndCompactsSwitcher() {
        let plan = TopBarLayout.plan(available: singleColumnFloor, model: browserModel)
        XCTAssertEqual(plan.level, .compactSwitcher)
        XCTAssertEqual(plan.switcherLabelCap, Layout.topBarSwitcherCompactLabelCap)
        XCTAssertLessThanOrEqual(
            TopBarLayout.width(level: plan.level, model: browserModel),
            singleColumnFloor - Layout.topBarDegradeMargin
        )
        let plain = TopBarLayout.plan(available: singleColumnFloor, model: plainPanelModel)
        XCTAssertEqual(plain.level, .compactSwitcher)
        XCTAssertFalse(plain.showsOverflow)
        XCTAssertLessThanOrEqual(
            TopBarLayout.width(level: plain.level, model: plainPanelModel),
            singleColumnFloor - Layout.topBarDegradeMargin
        )
    }

    /// dev 构建的 DEV 徽标只会让栏更宽；双栏底线下降级后必须已经不显示徽标（含浏览器态）。
    func testDevChipIsShedFirst() {
        var model = browserModel
        model.isDev = true
        // dev 全量比 release 全量更宽（多了徽标）
        XCTAssertGreaterThan(
            TopBarLayout.width(level: .full, model: model),
            TopBarLayout.width(level: .full, model: browserModel)
        )
        let plan = TopBarLayout.plan(available: twoColumnFloor, model: model)
        XCTAssertFalse(plan.showsDevChip)
        XCTAssertFalse(plan.showsCenter)
    }

    /// 档位随可用宽单调不增（窗口越宽只应越少降级，不许「越宽越降级」）。
    func testLevelIsMonotonicInAvailableWidth() {
        let deepest = TopBarLevel.allCases[TopBarLevel.allCases.count - 1]
        for model in [browserModel, documentModel, plainPanelModel] {
            var previous = deepest
            for available in stride(from: CGFloat(600), through: 1400, by: 1) {
                let level = TopBarLayout.plan(available: available, model: model).level
                XCTAssertLessThanOrEqual(level, previous, "available=\(available) 档位回退")
                previous = level
            }
            // 最宽时一定回到全量
            XCTAssertEqual(TopBarLayout.plan(available: 1400, model: model).level, .full)
        }
    }

    /// 选出档位的实际宽一定装得进可用宽（除非已是最深档 —— 那时交给探针兜底）。
    func testChosenLevelFitsUnlessDeepest() {
        let deepest = TopBarLevel.allCases[TopBarLevel.allCases.count - 1]
        for model in [browserModel, documentModel, plainPanelModel] {
            for available in stride(from: CGFloat(600), through: 1400, by: 7) {
                let plan = TopBarLayout.plan(available: available, model: model)
                let width = TopBarLayout.width(level: plan.level, model: model)
                if plan.level != deepest {
                    XCTAssertLessThanOrEqual(width, available - Layout.topBarDegradeMargin,
                                             "available=\(available) level=\(plan.level) width=\(width)")
                }
            }
        }
    }

    /// ⋯ 只在真收了东西时出现；组宽口径与渲染一致（单图标组 38、5 图标组 178）。
    func testOverflowOnlyWhenSomethingIsFolded() {
        XCTAssertFalse(TopBarLayout.showsOverflow(level: .foldCenter, model: plainPanelModel))
        XCTAssertTrue(TopBarLayout.showsOverflow(level: .foldCenter, model: browserModel))
        // 组宽含**组内** Divider：每多一个按钮还要多一根「4 + 1 + 4」
        XCTAssertEqual(TopBarLayout.groupWidth(1), 38, accuracy: 0.001)
        XCTAssertEqual(TopBarLayout.groupWidth(5), 178, accuracy: 0.001)
        XCTAssertEqual(TopBarLayout.groupWidth(7), 248, accuracy: 0.001)
    }
}
