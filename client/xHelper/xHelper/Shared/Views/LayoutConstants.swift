import Foundation

enum Layout {
    static let chatHorizontalPadding: CGFloat = 24

    /// 单条消息最多带几张图（客户端护栏：防手滑全选整个文件夹）。
    /// 服务端/DeepSeek 的上限远高于此（单请求 600 张），这里只挡误操作。
    static let maxDraftImages: Int = 20

    static let toolbarHorizontalPadding: CGFloat = 16

    // MARK: - 顶栏胶囊（icon 边长 + 组内边距，高度为派生值，改动自动跟随）

    /// 顶栏左缘红绿灯让位（与 toolbarHorizontalPadding 相加 = 左区内容起始位置）。
    static let topBarTrafficClearance: CGFloat = 56

    /// 胶囊内 icon 边长（CapsuleIcon frame）。
    static let topBarIconSize: CGFloat = 26
    /// 胶囊组水平内边距（CapsuleGroup）。
    static let topBarCapsuleHPadding: CGFloat = 6
    /// 胶囊组垂直内边距（CapsuleGroup）。
    static let topBarCapsuleVPadding: CGFloat = 5
    /// 胶囊组总高（icon + 上下内边距），顶栏分割线等按此对齐。
    static let topBarCapsuleHeight: CGFloat = topBarIconSize + topBarCapsuleVPadding * 2

    // MARK: - 顶栏自适应降级（宽度账见 docs/design/mac-topbar-degrade/design.md）

    /// 下拉标签宽上限（D5）。窄窗降级到末档时收到 `topBarSwitcherCompactLabelCap`。
    static let topBarSwitcherLabelCap: CGFloat = 240
    static let topBarSwitcherCompactLabelCap: CGFloat = 160
    /// 下拉 trigger 除标签外的固定宽：标签左右内边距 12×2 + 标签↔箭头间距 4 + 箭头 ~11
    /// + 标签↔＋间距 6 + ＋按钮 26。
    /// ⚠️ 这里面的 `4` / `6` / `11` 对应 `TopBarView` 里那几个裸 spacing 与箭头估宽，
    /// 改视图那几处 spacing 时必须同步改这条（不加新常量是为了不让 5 个碎片散在 Layout 里）。
    static let topBarSwitcherChromeWidth: CGFloat = 24 + 4 + 11 + 6 + 26
    /// 无痕 🎭 前缀占宽（12pt 图标 + 4 间距）。
    static let topBarEphemeralBadgeWidth: CGFloat = 16
    /// DEV 徽标宽（10pt 文本 + 左右各 8 padding；仅 dev 构建）。
    static let topBarDevChipWidth: CGFloat = 44
    /// 顶栏外层 HStack 间距。
    static let topBarGroupSpacing: CGFloat = 8
    /// 胶囊组内按钮间距（`CapsuleGroup` 的 HStack spacing；组内 Divider 两侧也吃它）。
    static let topBarInGroupSpacing: CGFloat = 4
    /// 顶栏里 Divider 的线宽（都是 `Divider().frame(height: 14)` 的细线）。
    static let topBarDividerLineWidth: CGFloat = 1
    /// 一根 Divider 的占宽 = 两侧间距 + 线宽 = 4 + 1 + 4。
    /// **组内按钮之间与同槽两组之间同构**（前者靠 HStack spacing，后者靠显式 padding）。
    static let topBarDividerWidth: CGFloat = topBarInGroupSpacing * 2 + topBarDividerLineWidth
    /// 中/右槽之间的 UnresizableDivider 占宽。
    static let topBarSlotDividerWidth: CGFloat = 5
    /// 选档安全余量：结构性宽度都是 Layout 精确常数，估算项只有「下拉箭头 11 / DEV 徽标 44 /
    /// 🎭 前缀 16 / `⋯` 的 Menu 内边距 / Divider 线宽 1」（见设计文档 §3.3）。
    /// 宁可少显示一档，也别让顶栏在窗口底线上溢出。
    static let topBarDegradeMargin: CGFloat = 8

    // MARK: - 双栏布局（单点改基础值，派生值自动跟随）

    /// 卡片与窗口边缘的横向间隙（缝线两侧各取一半确保视觉等宽）。
    static let cardGap: CGFloat = 6
    /// 缝线单侧间隙 = (cardGap - seamWidth) / 2。
    static let seamHalfGap: CGFloat = (cardGap - seamWidth) / 2
    /// 顶栏上/下纵向间隙（窗口上缘↔按钮、按钮↔卡片）。
    static let toolbarVGap: CGFloat = 6
    /// 卡片底部与窗口边缘的纵向间隙。
    static let cardBottomGap: CGFloat = 6

    /// 中间聊天区最小宽。
    static let middleMinWidth: CGFloat = 380
    /// 右栏最小宽。
    static let rightSidebarMinWidth: CGFloat = 380
    /// 窗口最小内容宽下限（底栏公式之外的兜底；实测 <750 观感变差）。
    static let minWindowContentWidth: CGFloat = 750
    /// 分隔线（SeamMarker）宽度。
    static let seamWidth: CGFloat = 1

    /// 右栏卡片整体水平外扩（两侧 cardGap）。
    static let rightSidebarOuterPadding: CGFloat =
        cardGap * 2
    /// 右栏打开时所需的内容最小宽（中栏 + 左 cardGap + seam 区 + 右栏 + 右 cardGap）。
    /// seam 区 = seamHalfGap*2 + seamWidth；两侧 cardGap 由 rightSidebarOuterPadding 承担。
    static let rightSidebarOpenMinContentWidth: CGFloat =
        middleMinWidth + seamHalfGap * 2 + seamWidth + rightSidebarMinWidth + rightSidebarOuterPadding

    // MARK: - 文档阅读器内三栏（目录 / 正文 / 批注）

    /// 批注面板展开宽 / 收起窄条宽。
    static let docAnnotPanelWidth: CGFloat = 320
    static let docPanelStripWidth: CGFloat = 32
    /// 目录栏展开宽。
    static let docTocExpandedWidth: CGFloat = 220
    /// 正文可读最小宽（收放判定的基准）。
    static let docReaderMinWidth: CGFloat = 260
    /// 批注面板兜底收起阈值：低于它，正文已被压到最小。
    static let docAnnotHardFloor: CGFloat = docReaderMinWidth + docAnnotPanelWidth
    /// 目录兜底收起阈值：比批注更高 → 窄时「先收目录、再收批注」。
    static let docTocHardFloor: CGFloat = docReaderMinWidth + docAnnotPanelWidth + docTocExpandedWidth

    // MARK: - 聊天画中画面板（PiP）

    /// 面板默认尺寸（首次进入/未持久化时），可调。
    static let floatingPanelDefaultWidth: CGFloat = 360
    static let floatingPanelDefaultHeight: CGFloat = 480
    /// 面板缩小地板：拖拽缩放不可小于此（被窗口既有最小值天然保证）。
    static let floatingPanelMinWidth: CGFloat = 240
    static let floatingPanelMinHeight: CGFloat = 200
    /// 面板放大硬上限（避免拖得过大/边界怪异）。
    static let floatingPanelMaxWidth: CGFloat = 800
    static let floatingPanelMaxHeight: CGFloat = 900
    /// 面板距容器边缘的最小留白。
    static let floatingPanelEdgeMargin: CGFloat = 8
    /// 拖拽结束距边缘小于该阈值时吸附（x/y 独立判定）。
    static let floatingPanelSnapThreshold: CGFloat = 16
    /// 收起态圆点边长。
    static let floatingPanelCollapsedDotSize: CGFloat = 45
    /// 面板内紧凑字号系数（乘在全局字号之上）。
    static let floatingPanelCompactFontFactor: Double = 0.75
}
