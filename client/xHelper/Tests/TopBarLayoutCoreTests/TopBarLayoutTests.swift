import XCTest
@testable import TopBarLayoutCore

/// 顶栏自适应降级：按可用宽选档 + 各档内在宽。
///
/// 断言的目标是**行为而非数字**：装得下的最不降级档、窗口底线（双栏 778 / PiP 750）下中心槽组必须
/// 让位、档位随可用宽单调不增、dev 徽标永不收起。数字只在第一条里钉住量级，防止常量漂移后静默走偏。
final class TopBarLayoutTests: XCTestCase {
    /// 右栏开时的窗口底线（= `Layout.rightSidebarOpenMinContentWidth`）。
    private let twoColumnFloor = Layout.rightSidebarOpenMinContentWidth
    /// 无右栏 / PiP 的窗口底线。
    private let singleColumnFloor = Layout.minWindowContentWidth

    /// 中心组构造助手：默认可"部分收"（= `TopBarGroup.autoOverflow` 打开）。
    private func center(_ buttons: Int, autoOverflow: Bool = true) -> TopBarCenterGroup {
        TopBarCenterGroup(buttons: buttons, autoOverflow: autoOverflow)
    }

    /// 右栏开着浏览器（中心槽 7 键）+ 导航组 5 键 + 工具组 4 键（侧栏已开，最大化按钮可见）。
    private var browserModel: TopBarWidthModel {
        TopBarWidthModel(centerGroups: [center(7)], navGroupButtons: 5, utilityButtons: 4)
    }
    /// 右栏开着文档详情（中心槽 3 键）。
    private var documentModel: TopBarWidthModel {
        TopBarWidthModel(centerGroups: [center(3)], navGroupButtons: 5, utilityButtons: 4)
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

    /// dev 构建的 DEV 徽标**降级不收**：任何可用宽（含双栏底线 778 / PiP 750）下都在栏上，
    /// 于是 dev 各档内在宽恒比 release 多一个徽标宽。
    func testDevChipIsAlwaysShown() {
        var model = browserModel
        model.isDev = true
        // dev 每档都比 release 更宽：徽标本身 44 + 多一个簇内子项带来的一次簇内间距 8
        for level in TopBarLevel.allCases {
            XCTAssertEqual(
                TopBarLayout.width(level: level, model: model)
                    - TopBarLayout.width(level: level, model: browserModel),
                Layout.topBarDevChipWidth + Layout.topBarGroupSpacing,
                accuracy: 0.001,
                "level=\(level)"
            )
        }
        for available in [twoColumnFloor, singleColumnFloor] {
            XCTAssertTrue(TopBarLayout.plan(available: available, model: model).showsDevChip,
                          "available=\(available) 徽标被收掉了")
        }
        XCTAssertTrue(TopBarLayout.plan(available: twoColumnFloor, model: model).showsDevChip)
        XCTAssertFalse(TopBarLayout.plan(available: twoColumnFloor, model: model).showsCenter)
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

    // MARK: - 右簇必须落进「中栏卡片右侧那段」（docs/bugs/034）

    /// 右栏开着（默认宽 380）时右簇可用的宽 = cardGap 6 + seam 6 + 右栏 380 − 顶栏右内边距 16
    /// （与 `ContentView.topBarTrailingLimit` 同式；那里取真实右栏宽）。
    private let cardTrailingLimit: CGFloat =
        Layout.cardGap + Layout.seamHalfGap * 2 + Layout.seamWidth + 380 - Layout.toolbarHorizontalPadding

    /// 右栏开着：中心槽组在任何窗口宽度下都不许显形 —— 它显形就把整簇往左推、压在中栏卡片上。
    /// （窗口越宽中栏卡片越宽，所以「窗口够宽就显形」这条老判据永远会踩中。）
    func testCenterGroupStaysFoldedWhenSidebarOpen() {
        for model in [browserModel, documentModel] {
            for available in stride(from: CGFloat(700), through: 1600, by: 1) {
                let plan = TopBarLayout.plan(available: available, trailingLimit: cardTrailingLimit, model: model)
                XCTAssertFalse(plan.showsCenter, "available=\(available) 中心槽组显形了")
            }
        }
    }

    /// 这道闸只该收中心槽，不该牵连标签：宽窗 + 右栏开时档位停在 foldCenter（标签 240），
    /// 不是一路落到末档（落到末档 = 闸门放错位置了）。
    func testSidebarGateDoesNotCompactSwitcher() {
        let plan = TopBarLayout.plan(available: 1200, trailingLimit: cardTrailingLimit, model: browserModel)
        XCTAssertEqual(plan.level, .foldCenter)
        XCTAssertEqual(plan.switcherLabelCap, Layout.topBarSwitcherLabelCap)
        XCTAssertTrue(plan.showsOverflow)
    }

    /// 右栏拖宽到够放下整簇 → 中心槽组自然回栏，且整簇真落在限制内。
    func testCenterGroupReturnsWhenSidebarIsWideEnough() {
        let limit: CGFloat = 700
        let plan = TopBarLayout.plan(available: 1200, trailingLimit: limit, model: browserModel)
        XCTAssertEqual(plan.level, .full)
        XCTAssertTrue(plan.showsCenter)
        XCTAssertLessThanOrEqual(
            TopBarLayout.trailingWidth(level: plan.level, model: browserModel),
            limit - Layout.topBarDegradeMargin
        )
    }

    /// 不变量：中心槽露出来的部分（含它带来的槽间线）一定落得进限制内。
    func testShownCenterFitsTrailingLimit() {
        for model in [browserModel, documentModel, plainPanelModel] {
            for limit in stride(from: CGFloat(300), through: 900, by: 7) {
                for available in stride(from: CGFloat(700), through: 1400, by: 97) {
                    let plan = TopBarLayout.plan(available: available, trailingLimit: limit, model: model)
                    guard plan.showsCenter else { continue }
                    XCTAssertLessThanOrEqual(
                        TopBarLayout.trailingWidth(
                            level: plan.level, model: model, centerVisible: plan.centerVisible
                        ),
                        limit - Layout.topBarDegradeMargin,
                        "available=\(available) limit=\(limit) level=\(plan.level) k=\(plan.centerVisible)"
                    )
                }
            }
        }
    }

    /// 右栏关着 / PiP（limit = nil）：与加这道闸之前完全一致 —— 宽窗照样全量、中心槽组显形。
    func testNilTrailingLimitKeepsFullAtWideWindow() {
        XCTAssertEqual(TopBarLayout.plan(available: 1200, model: browserModel).level, .full)
        XCTAssertTrue(TopBarLayout.plan(available: 1200, trailingLimit: nil, model: browserModel).showsCenter)
        XCTAssertFalse(
            TopBarLayout.plan(available: 1200, trailingLimit: cardTrailingLimit, model: browserModel).showsCenter
        )
        // 折叠态的右簇口径：导航组 178 + 线 9 + ⋯ 38 + 线 9 + 工具组 143
        XCTAssertEqual(TopBarLayout.trailingWidth(level: .foldCenter, model: browserModel), 377, accuracy: 0.001)
    }

    // MARK: - 中心槽「部分收」（docs/design/overflow-collapse）

    /// 用户实拍那一屏：窗口 ≈995、右栏拖到 ≈600（卡片右侧那段 = 600 + cardGap 6 + seam 6 − 顶栏右内边距 16 = 596）。
    /// 7 键整组放不进（1028 > 987），但白给的空间够放 4 个 —— 不该整组进 ⋯。
    func testPartialCenterKeepsWhatFits() {
        let plan = TopBarLayout.plan(available: 995, trailingLimit: 596, model: browserModel)
        XCTAssertEqual(plan.centerVisible, [4])
        XCTAssertTrue(plan.showsCenter)
        XCTAssertTrue(plan.showsOverflow)
        XCTAssertEqual(plan.hiddenCenterButtons, 3)
        XCTAssertLessThanOrEqual(
            TopBarLayout.width(level: plan.level, model: browserModel, centerVisible: plan.centerVisible),
            995 - Layout.topBarDegradeMargin
        )
        XCTAssertLessThanOrEqual(
            TopBarLayout.trailingWidth(level: plan.level, model: browserModel, centerVisible: plan.centerVisible),
            596 - Layout.topBarDegradeMargin
        )
    }

    /// 部分收取的是**能露的最大值**：再多露一个就装不下。
    func testPartialCenterIsMaximal() {
        let plan = TopBarLayout.plan(available: 995, trailingLimit: 596, model: browserModel)
        let k = plan.centerVisible[0]
        XCTAssertGreaterThan(k, 0)
        XCTAssertFalse(
            TopBarLayout.fits(
                level: plan.level, available: 995, trailingLimit: 596,
                model: browserModel, centerVisible: [k + 1]
            ),
            "k+1 = \(k + 1) 也装得下 → k 取小了"
        )
    }

    /// 空间不够就一路收到 0（= 整组进 ⋯，与加这层之前一致）：双栏底线 778 + 右栏最小 380。
    func testPartialCenterFallsBackToZeroAtFloor() {
        let plan = TopBarLayout.plan(available: twoColumnFloor, trailingLimit: cardTrailingLimit, model: browserModel)
        XCTAssertEqual(plan.centerVisible, [0])
        XCTAssertFalse(plan.showsCenter)
        XCTAssertTrue(plan.showsOverflow)
        XCTAssertEqual(plan.hiddenCenterButtons, 7)
    }

    /// 多中心组同时可见时按注册顺序先到先得（今天实际不会出现，钉住口径免得将来走偏）：
    /// 每个宽度下第 0 组留的不少于第 1 组，且确实存在"第 0 组多留、第 1 组少留"的宽度。
    func testPartialCenterIsGreedyByGroupOrder() {
        let model = TopBarWidthModel(centerGroups: [center(2), center(2)], navGroupButtons: 5, utilityButtons: 4)
        var sawSplit = false
        for available in stride(from: CGFloat(800), through: 1400, by: 2) {
            let plan = TopBarLayout.plan(available: available, model: model)
            XCTAssertGreaterThanOrEqual(plan.centerVisible[0], plan.centerVisible[1], "available=\(available)")
            if plan.centerVisible[0] > plan.centerVisible[1] { sawSplit = true }
        }
        XCTAssertTrue(sawSplit, "没有任何宽度能让第 0 组比第 1 组多留 → 先到先得没生效")
    }

    // MARK: - `⋯` 长在组内 / 独立胶囊 / 组自己声明的开关（docs/design/overflow-collapse）

    /// `⋯` 长在组内：那一组多算一项（图标 26 + 组内线 9），不是旁边单开一颗胶囊。
    func testOverflowRidesInsideTheGroup() {
        let plan = TopBarLayout.plan(available: 995, trailingLimit: 596, model: browserModel)
        XCTAssertEqual(plan.centerVisible, [4])
        XCTAssertTrue(plan.overflowInline)
        XCTAssertFalse(plan.overflowStandalone)
        // 中心槽 = 4 个真按钮 + 末尾 ⋯ = groupWidth(5)
        XCTAssertEqual(
            TopBarLayout.centerSlotWidth(level: plan.level, model: browserModel, centerVisible: [4]),
            TopBarLayout.groupWidth(5),
            accuracy: 0.001
        )
        // 右槽不再挂 ⋯（导航组 178 + 线 9 + 工具组 143）：nav / util 组一颗都没少
        XCTAssertEqual(
            TopBarLayout.rightSlotWidth(level: plan.level, model: browserModel, centerVisible: [4]),
            330,
            accuracy: 0.001
        )
    }

    /// 一个都留不下时才退回右槽那颗独立 `⋯`（= 加"部分收"之前的样子）。
    func testOverflowFallsBackToStandaloneCapsule() {
        let plan = TopBarLayout.plan(available: twoColumnFloor, trailingLimit: cardTrailingLimit, model: browserModel)
        XCTAssertEqual(plan.centerVisible, [0])
        XCTAssertFalse(plan.overflowInline)
        XCTAssertTrue(plan.overflowStandalone)
        // 右槽多一颗 ⋯ 组：178 + 线 9 + ⋯ 38 + 线 9 + 143
        XCTAssertEqual(
            TopBarLayout.rightSlotWidth(level: plan.level, model: browserModel, centerVisible: [0]),
            377,
            accuracy: 0.001
        )
    }

    /// 单中心组时两种 `⋯` 互斥：要么长在组内、要么独立，不会同时出现。
    func testOverflowPlacementIsMutuallyExclusiveForSingleGroup() {
        for available in stride(from: CGFloat(700), through: 1600, by: 13) {
            for limit in [cardTrailingLimit, 596, nil as CGFloat?] {
                let plan = TopBarLayout.plan(available: available, trailingLimit: limit, model: browserModel)
                XCTAssertFalse(plan.overflowInline && plan.overflowStandalone, "available=\(available)")
                XCTAssertEqual(plan.showsOverflow, plan.overflowInline || plan.overflowStandalone)
            }
        }
    }

    /// 组没开 `autoOverflow`（默认）→ 永不部分收：这一档整组不进栏（按钮进独立 ⋯）。
    func testOptOutGroupNeverPartiallyFolds() {
        let pinned = TopBarWidthModel(
            centerGroups: [center(7, autoOverflow: false)], navGroupButtons: 5, utilityButtons: 4
        )
        let plan = TopBarLayout.plan(available: 995, trailingLimit: 596, model: pinned)
        XCTAssertEqual(plan.centerVisible, [0])
        XCTAssertFalse(plan.showsCenter)
        XCTAssertTrue(plan.overflowStandalone)
        // 但整组装得下时照旧整组显形（开关只影响"部分收"）
        let wide = TopBarLayout.plan(available: 1400, model: pinned)
        XCTAssertEqual(wide.level, .full)
        XCTAssertEqual(wide.centerVisible, [7])
    }
}
