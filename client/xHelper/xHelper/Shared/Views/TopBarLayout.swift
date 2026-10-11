import Foundation

/// 顶栏自适应降级的档位（按序递进：越靠后越降级；高档位包含低档位的取舍）。
///
/// 顶栏**不再决定窗口最小宽**——窗口底线由两栏公式给（右栏开 778 / 无右栏与 PiP 750），
/// 顶栏按可用宽自己降级。背景见 `docs/design/mac-topbar-degrade/design.md`
/// （来源：`docs/bugs/033` §5 的遗留项「顶栏降级 + 会话下拉常驻」）。
enum TopBarLevel: Int, CaseIterable, Comparable {
    /// 全量。
    case full = 0
    /// 收 DEV 徽标（仅 dev 构建存在；纯装饰，第一牺牲）。
    case hideDevChip = 1
    /// 中心槽组（浏览器 7 键 / 文档详情 3 键）收进 `⋯`。
    case foldCenter = 2
    /// 下拉标签收窄（240 → 160）。
    ///
    /// 为什么最后才动标签而不是收导航组：导航组（文档/账单/监控/记忆/进浏览器）的 ⌘1–5
    /// 挂在**被渲染的按钮**上（`TopBarButton.keyEquivalent` → `keyboardShortcut`），
    /// 整组收进 `⋯` 会把快捷键一起丢掉；收标题只是截断文字。
    case compactSwitcher = 3

    static func < (lhs: Self, rhs: Self) -> Bool { lhs.rawValue < rhs.rawValue }
}

/// 顶栏宽度模型：渲染时「有哪些东西、各几个」。全部能在渲染时从 registry/状态直接取到，
/// 所以降级判定是纯函数（可 `swift test`，见 `Tests/TopBarLayoutCoreTests`）。
struct TopBarWidthModel: Equatable {
    /// 中心槽各可见组的按钮数（通常 0 或 1 组：浏览器 7 / 文档详情 3）。
    var centerGroups: [Int] = []
    /// 右槽导航组（`default-right`）可见按钮数；该组整体不可见时传 0。
    var navGroupButtons: Int = 0
    /// 右槽工具组（`default-utility`）可见按钮数。
    var utilityButtons: Int = 0
    /// dev 构建才有 DEV 徽标。
    var isDev: Bool = false
    /// 当前会话无痕时下拉标签带 🎭 前缀。
    var hasEphemeralBadge: Bool = false

    var centerButtons: Int { centerGroups.reduce(0, +) }
}

/// 降级计划：视图与宽度计算共用同一份真相（渲染什么、⋯ 装什么、标签上限多少）。
struct TopBarPlan: Equatable {
    let level: TopBarLevel
    let model: TopBarWidthModel

    /// DEV 徽标是否显示（dev 且未降到收徽标档）。
    var showsDevChip: Bool { model.isDev && level < .hideDevChip }
    /// 中心槽组的按钮是否留在栏上。
    var showsCenter: Bool { level < .foldCenter }
    /// 是否渲染 `⋯`（只在真收了东西时）。
    var showsOverflow: Bool { TopBarLayout.showsOverflow(level: level, model: model) }
    /// 下拉标签宽上限（按当前档）。
    var switcherLabelCap: CGFloat { TopBarLayout.switcherLabelCap(level: level) }
}

/// 顶栏降级的排布算术：按可用宽选档 + 算各档内在宽。
///
/// 之所以能「算」而不必「量」：顶栏所有按钮都是统一 `Layout.topBarIconSize` 的 icon，
/// 组内边距/组间距/外边距全是常数（与 `TopBarView` 渲染一一对应）。
enum TopBarLayout {
    /// 选档：**装得下的最不降级**档。`available` = 窗口内容宽。
    /// 都装不下时给最深档，剩下的交给隐藏探针兜底（顶宽窗口而不是让顶栏溢出）。
    static func plan(available: CGFloat, model: TopBarWidthModel) -> TopBarPlan {
        for level in TopBarLevel.allCases
        where width(level: level, model: model) <= available - Layout.topBarDegradeMargin {
            return TopBarPlan(level: level, model: model)
        }
        return TopBarPlan(level: TopBarLevel.allCases[TopBarLevel.allCases.count - 1], model: model)
    }

    /// 某档下顶栏的内在宽（宽度账见设计文档）。
    static func width(level: TopBarLevel, model: TopBarWidthModel) -> CGFloat {
        var children: [CGFloat] = [
            Layout.topBarTrafficClearance,
            switcherWidth(level: level, model: model),
            0, // 探针副本里的 0 宽占位 Spacer（真实栏是弹性 Spacer，不贡献内在宽）
        ]
        if model.isDev && level < .hideDevChip { children.append(Layout.topBarDevChipWidth) }

        let centerWidth = level < .foldCenter ? slotWidth(model.centerGroups) : 0
        if centerWidth > 0 { children.append(centerWidth) }

        let rightWidth = rightSlotWidth(level: level, model: model)
        // 中/右两槽都有内容时中间有一根 UnresizableDivider（与 TopBarView 同条件）
        if centerWidth > 0 && rightWidth > 0 { children.append(Layout.topBarSlotDividerWidth) }
        if rightWidth > 0 { children.append(rightWidth) }

        return Layout.toolbarHorizontalPadding * 2
            + children.reduce(0, +)
            + Layout.topBarGroupSpacing * CGFloat(children.count - 1)
    }

    /// 下拉 trigger 宽。标签按**当前档的上限吃满**算 —— 于是档位不随会话标题抖动
    /// （短标题时顶栏实际更窄，属保守方向，不会溢出）。
    static func switcherWidth(level: TopBarLevel, model: TopBarWidthModel) -> CGFloat {
        Layout.topBarSwitcherChromeWidth
            + switcherLabelCap(level: level)
            + (model.hasEphemeralBadge ? Layout.topBarEphemeralBadgeWidth : 0)
    }

    /// 下拉标签上限（D5 = 240；末档收到 compact）。
    static func switcherLabelCap(level: TopBarLevel) -> CGFloat {
        level >= .compactSwitcher
            ? Layout.topBarSwitcherCompactLabelCap
            : Layout.topBarSwitcherLabelCap
    }

    /// 右槽 = 导航组 + ⋯（若收了东西）+ 工具组，相邻两组之间一根 Divider。
    static func rightSlotWidth(level: TopBarLevel, model: TopBarWidthModel) -> CGFloat {
        var counts: [Int] = []
        if model.navGroupButtons > 0 { counts.append(model.navGroupButtons) }
        if showsOverflow(level: level, model: model) { counts.append(1) } // ⋯：单图标组
        if model.utilityButtons > 0 { counts.append(model.utilityButtons) }
        return slotWidth(counts)
    }

    /// 是否有东西被收进 `⋯`（当前只有中心槽组会收）。
    static func showsOverflow(level: TopBarLevel, model: TopBarWidthModel) -> Bool {
        level >= .foldCenter && model.centerButtons > 0
    }

    /// 一个槽位的宽 = 各组宽 + 组间 Divider。
    static func slotWidth(_ groupButtonCounts: [Int]) -> CGFloat {
        let counts = groupButtonCounts.filter { $0 > 0 }
        guard !counts.isEmpty else { return 0 }
        return counts.reduce(0) { $0 + groupWidth($1) }
            + Layout.topBarDividerWidth * CGFloat(counts.count - 1)
    }

    /// 单个 CapsuleGroup 的宽 = 左右内边距 + n 个 icon + (n−1) 根**组内** Divider。
    ///
    /// ⚠️ 组内按钮之间渲染的是 `Divider()`（见 `TopBarView.buttonRow`），而 `CapsuleGroup` 是
    /// `HStack(spacing: topBarInGroupSpacing)` —— 相邻子项各吃一次间距，所以每对按钮占
    /// `间距 + 线宽 + 间距`（= 9，与组间 Divider 同构），**不是**单个间距。
    static func groupWidth(_ buttonCount: Int) -> CGFloat {
        guard buttonCount > 0 else { return 0 }
        return Layout.topBarCapsuleHPadding * 2
            + CGFloat(buttonCount) * Layout.topBarIconSize
            + CGFloat(buttonCount - 1) * Layout.topBarDividerWidth
    }
}
