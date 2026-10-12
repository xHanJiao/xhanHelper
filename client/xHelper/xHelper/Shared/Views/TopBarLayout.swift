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
    /// 中心槽各可见组（通常 0 或 1 组：浏览器 7 / 文档详情 3）。
    var centerGroups: [TopBarCenterGroup] = []
    /// 右槽导航组（`default-right`）可见按钮数；该组整体不可见时传 0。
    var navGroupButtons: Int = 0
    /// 右槽工具组（`default-utility`）可见按钮数。
    var utilityButtons: Int = 0
    /// dev 构建才有 DEV 徽标。
    var isDev: Bool = false
    /// 当前会话无痕时下拉标签带 🎭 前缀。
    var hasEphemeralBadge: Bool = false

    var centerButtons: Int { centerGroups.reduce(0) { $0 + $1.buttons } }
}

/// 中心槽的一项：可见按钮数 + 这一组自己声明的「允许部分收」开关（= `TopBarGroup.autoOverflow`）。
/// 开关关着 = 不参与部分收：这一档它只会"整组显形 / 整组进 `⋯`"。
struct TopBarCenterGroup: Equatable {
    var buttons: Int
    var autoOverflow: Bool
}

/// 降级计划：视图与宽度计算共用同一份真相（渲染什么、⋯ 装什么、标签上限多少、中心组留几个）。
struct TopBarPlan: Equatable {
    let level: TopBarLevel
    let model: TopBarWidthModel
    /// 中心槽**各组留在栏上**的按钮数（与 `model.centerGroups` 同序）。档位低于 `.foldCenter` 时
    /// 恒等于 `model.centerGroups`（整组显形）；否则 = 白给的空间能塞下的个数（`0` = 整组进 `⋯`）。
    let centerVisible: [Int]

    /// DEV 徽标是否显示（dev 且未降到收徽标档）。
    var showsDevChip: Bool { model.isDev && level < .hideDevChip }
    /// 中心槽是否还有按钮留在栏上（整组被收时为 false）。
    var showsCenter: Bool { centerVisible.contains { $0 > 0 } }
    /// 是否渲染 `⋯`（只在真收了东西时）。
    var showsOverflow: Bool {
        TopBarLayout.showsOverflow(level: level, model: model, centerVisible: centerVisible)
    }
    /// `⋯` 长在中心组末尾（有组留了一部分）—— 这时右槽不再单开胶囊。
    var overflowInline: Bool {
        TopBarLayout.inlineOverflow(centerVisible: centerVisible, model: model)
    }
    /// `⋯` 在右槽单开一颗胶囊（有组整组进了 `⋯`）。
    var overflowStandalone: Bool {
        TopBarLayout.standaloneOverflow(centerVisible: centerVisible, model: model)
    }
    /// 下拉标签宽上限（按当前档）。
    var switcherLabelCap: CGFloat { TopBarLayout.switcherLabelCap(level: level) }
    /// 被收进 `⋯` 的按钮总数。
    var hiddenCenterButtons: Int {
        // zip 取短：长度由调用方同源保证（视图侧另有 assert 兜底）
        zip(model.centerGroups, centerVisible).reduce(0) { $0 + max(0, $1.0.buttons - $1.1) }
    }
}

/// 顶栏降级的排布算术：按可用宽选档 + 算各档内在宽。
///
/// 之所以能「算」而不必「量」：顶栏所有按钮都是统一 `Layout.topBarIconSize` 的 icon，
/// 组内边距/组间距/外边距全是常数（与 `TopBarView` 渲染一一对应）。
enum TopBarLayout {
    /// 选档：**装得下的最不降级**档。`available` = 窗口内容宽；`trailingLimit` = 右簇能用的宽
    /// （右栏打开时 = 中栏卡片右侧那段，见 `ContentView.topBarTrailingLimit`；`nil` = 不限制）。
    /// 都装不下时给最深档，剩下的交给隐藏探针兜底（顶宽窗口而不是让顶栏溢出）。
    ///
    /// 档位只管「DEV 徽标 / 下拉标签」两根杠杆 + 中心槽**能否整组**显形；到 `.foldCenter` 及更深的档，
    /// 中心组再由 `fittingCenterVisible` 按白给的空间**部分保留**（能留几个留几个，`0` = 整组进 `⋯`）。
    static func plan(available: CGFloat, trailingLimit: CGFloat? = nil, model: TopBarWidthModel) -> TopBarPlan {
        var chosen = TopBarLevel.allCases[TopBarLevel.allCases.count - 1]
        for level in TopBarLevel.allCases
        where width(level: level, model: model) <= available - Layout.topBarDegradeMargin {
            // 中心槽组整组显形时，整个右簇必须落得进卡片右侧那段：右簇右对齐，一显形就把整簇
            // 往左推、压在中栏卡片上（`mac-card-top-wrap`）。中心槽已收掉的档不再过这道闸。
            if level < .foldCenter, let limit = trailingLimit,
               trailingWidth(level: level, model: model) > limit - Layout.topBarDegradeMargin {
                continue
            }
            chosen = level
            break
        }
        return TopBarPlan(
            level: chosen,
            model: model,
            centerVisible: fittingCenterVisible(
                level: chosen, available: available, trailingLimit: trailingLimit, model: model
            )
        )
    }

    /// 中心槽各组最终留在栏上的按钮数。
    ///
    /// 只吃**白给的空间**：档位选定后，「中心槽整组不显示」的宽到可用宽的距离（窗口宽与卡片右侧段
    /// 各算一遍取小）就是预算；组内按注册顺序留前 k 个，`k = 0` 就是整组进 `⋯`。
    /// 只动**开了 `autoOverflow` 的组**（默认关的组这一档整组不进栏、按钮进 `⋯`）；
    /// 也不让 nav / util 组吐按钮 —— 拿带快捷键的导航组换一个中心按钮不划算（见 `docs/design/overflow-collapse`）。
    static func fittingCenterVisible(
        level: TopBarLevel, available: CGFloat, trailingLimit: CGFloat?, model: TopBarWidthModel
    ) -> [Int] {
        // 整组显形的档：全留（开关关着也无所谓，反正整组都在栏上）
        guard level >= .foldCenter else { return model.centerGroups.map(\.buttons) }
        var visible = model.centerGroups.map { _ in 0 }
        for (index, group) in model.centerGroups.enumerated() where group.autoOverflow {
            visible[index] = fittingVisibleCount(count: group.buttons) { candidate in
                var probe = visible
                probe[index] = candidate
                return fits(
                    level: level, available: available, trailingLimit: trailingLimit,
                    model: model, centerVisible: probe
                )
            }
        }
        return visible
    }

    /// 通用「一行里能露几项」：从「全露」往下试，第一个满足 `fits` 的项数就是要露的项数。
    ///
    /// 顶栏的按钮组与缩略图行（`ThumbRowLayout.collapsed`）是同一种算术；后者目前仍自算自己的口径
    /// （只有一个消费者，不为未来单开模块，见 `docs/design/overflow-collapse` §六 D3）。
    static func fittingVisibleCount(count: Int, fits: (Int) -> Bool) -> Int {
        guard count > 0 else { return 0 }
        for visible in stride(from: count, through: 0, by: -1) where fits(visible) {
            return visible
        }
        // 兜底 0（= 整组进独立 `⋯`）：`fits(0)` **也可能**为 false —— `plan` 只在 `foldCenter` 之前过卡片闸，
        // 到这一档右槽那颗独立 `⋯` 仍可能越卡片右缘（右栏最小 380 那档差 1pt）。
        // 旧版同样如此、已在 `docs/bugs/034` §已知边角接受，**别当 bug 去"修"**。
        return 0
    }

    /// 这份「中心槽留这些」的排布装得进吗：窗口宽与卡片右侧段两道都过。
    static func fits(
        level: TopBarLevel, available: CGFloat, trailingLimit: CGFloat?,
        model: TopBarWidthModel, centerVisible: [Int]
    ) -> Bool {
        guard width(level: level, model: model, centerVisible: centerVisible)
            <= available - Layout.topBarDegradeMargin else { return false }
        guard let limit = trailingLimit else { return true }
        return trailingWidth(level: level, model: model, centerVisible: centerVisible)
            <= limit - Layout.topBarDegradeMargin
    }

    /// 某档下顶栏的内在宽（宽度账见设计文档）。口径 = **中心槽整组不显示**：
    /// 档位判据、卡片闸与探针都用它，数字与"部分收"之前逐位相同。
    static func width(level: TopBarLevel, model: TopBarWidthModel) -> CGFloat {
        width(level: level, model: model, centerVisible: zeroCenterVisible(model))
    }

    /// 指定「中心槽各组留几个」时的顶栏内在宽（找可行的 k 用）。
    static func width(level: TopBarLevel, model: TopBarWidthModel, centerVisible: [Int]) -> CGFloat {
        var children: [CGFloat] = [
            Layout.topBarTrafficClearance,
            switcherWidth(level: level, model: model),
            0, // 探针副本里的 0 宽占位 Spacer（真实栏是弹性 Spacer，不贡献内在宽）
        ]
        children.append(contentsOf: trailingChildren(level: level, model: model, centerVisible: centerVisible))

        return Layout.toolbarHorizontalPadding * 2
            + children.reduce(0, +)
            + Layout.topBarGroupSpacing * CGFloat(children.count - 1)
    }

    /// 右簇（DEV 徽标 / 中心槽 / 槽间线 / 右槽）各子项的宽 —— 与 `TopBarView.topBarHStack`
    /// 那几行一一对应。它右对齐，所以要单独核算（见 `plan` 的第一道闸）。
    static func trailingChildren(level: TopBarLevel, model: TopBarWidthModel) -> [CGFloat] {
        trailingChildren(level: level, model: model, centerVisible: zeroCenterVisible(model))
    }

    static func trailingChildren(level: TopBarLevel, model: TopBarWidthModel, centerVisible: [Int]) -> [CGFloat] {
        var children: [CGFloat] = []
        if model.isDev && level < .hideDevChip { children.append(Layout.topBarDevChipWidth) }

        let centerWidth = centerSlotWidth(level: level, model: model, centerVisible: centerVisible)
        if centerWidth > 0 { children.append(centerWidth) }

        let rightWidth = rightSlotWidth(level: level, model: model, centerVisible: centerVisible)
        // 中/右两槽都有内容时中间有一根 UnresizableDivider（与 TopBarView 同条件）
        if centerWidth > 0 && rightWidth > 0 { children.append(Layout.topBarSlotDividerWidth) }
        if rightWidth > 0 { children.append(rightWidth) }
        return children
    }

    /// 中心槽占宽：档位低于 `.foldCenter` = 整组显形；否则按传进来的可见数算（可能为 0）。
    /// `⋯` 长在组内时，**最后一组**多算一项（图标 26 + 组内分隔线 9）—— 与视图里
    /// `OverflowCapsuleGroup` 末尾挂 `⋯` 一一对应。
    static func centerSlotWidth(level: TopBarLevel, model: TopBarWidthModel, centerVisible: [Int]) -> CGFloat {
        if level < .foldCenter { return slotWidth(model.centerGroups.map(\.buttons)) }
        var counts = centerVisible.filter { $0 > 0 }
        guard !counts.isEmpty else { return 0 }
        if inlineOverflow(centerVisible: centerVisible, model: model) { counts[counts.count - 1] += 1 }
        return slotWidth(counts)
    }

    /// `⋯` 要不要长在中心组末尾（有组留了一部分、又没收全）。
    static func inlineOverflow(centerVisible: [Int], model: TopBarWidthModel) -> Bool {
        // zip 取短：长度由调用方同源保证（视图侧另有 assert 兜底）
        zip(model.centerGroups, centerVisible).contains { $0.1 > 0 && $0.1 < $0.0.buttons }
    }

    /// `⋯` 要不要在右槽单开一颗胶囊（有组一个都没留下 → 它整组进 `⋯`）。
    static func standaloneOverflow(centerVisible: [Int], model: TopBarWidthModel) -> Bool {
        // zip 取短：长度由调用方同源保证（视图侧另有 assert 兜底）
        zip(model.centerGroups, centerVisible).contains { $0.1 == 0 && $0.0.buttons > 0 }
    }

    /// 右簇占宽（含簇内组间距）。
    static func trailingWidth(level: TopBarLevel, model: TopBarWidthModel) -> CGFloat {
        trailingWidth(level: level, model: model, centerVisible: zeroCenterVisible(model))
    }

    static func trailingWidth(level: TopBarLevel, model: TopBarWidthModel, centerVisible: [Int]) -> CGFloat {
        let children = trailingChildren(level: level, model: model, centerVisible: centerVisible)
        guard !children.isEmpty else { return 0 }
        return children.reduce(0, +) + Layout.topBarGroupSpacing * CGFloat(children.count - 1)
    }

    /// 「中心槽一个都不留」的可见数（缺省口径）。
    private static func zeroCenterVisible(_ model: TopBarWidthModel) -> [Int] {
        model.centerGroups.map { _ in 0 }
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
        rightSlotWidth(level: level, model: model, centerVisible: zeroCenterVisible(model))
    }

    static func rightSlotWidth(level: TopBarLevel, model: TopBarWidthModel, centerVisible: [Int]) -> CGFloat {
        var counts: [Int] = []
        // 导航组 / 工具组**永远整组参与**：它们没开 `autoOverflow`，这里也不对它们做部分收
        // （右槽两组的口径与"部分收"无关，只随 ⋯ 有无变化）。
        if model.navGroupButtons > 0 { counts.append(model.navGroupButtons) }
        // ⋯ 单开一颗胶囊：只在中心组**整组**进 ⋯ 时（留了一部分的话，⋯ 长在组内，见 `centerSlotWidth`）
        if level >= .foldCenter, standaloneOverflow(centerVisible: centerVisible, model: model) {
            counts.append(1)
        }
        if model.utilityButtons > 0 { counts.append(model.utilityButtons) }
        return slotWidth(counts)
    }

    /// 是否有东西被收进 `⋯`：中心组留了一部分（组内 `⋯`）或有组整组进了 `⋯`（右槽独立胶囊）。
    static func showsOverflow(level: TopBarLevel, model: TopBarWidthModel) -> Bool {
        showsOverflow(level: level, model: model, centerVisible: zeroCenterVisible(model))
    }

    static func showsOverflow(level: TopBarLevel, model: TopBarWidthModel, centerVisible: [Int]) -> Bool {
        guard level >= .foldCenter else { return false }
        return inlineOverflow(centerVisible: centerVisible, model: model)
            || standaloneOverflow(centerVisible: centerVisible, model: model)
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
