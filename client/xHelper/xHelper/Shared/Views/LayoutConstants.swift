import Foundation

enum Layout {
    static let chatHorizontalPadding: CGFloat = 24
    static let sidebarMinWidth: CGFloat = 38
    static let sidebarMaxWidth: CGFloat = 276
    static let sidebarSnapThreshold: CGFloat = 120
    static let sidebarDefaultWidth: CGFloat = 200

    static let toolbarHorizontalPadding: CGFloat = 16

    // MARK: - 顶栏胶囊（icon 边长 + 组内边距，高度为派生值，改动自动跟随）

    /// 胶囊内 icon 边长（CapsuleIcon frame）。
    static let topBarIconSize: CGFloat = 26
    /// 胶囊组水平内边距（CapsuleGroup）。
    static let topBarCapsuleHPadding: CGFloat = 6
    /// 胶囊组垂直内边距（CapsuleGroup）。
    static let topBarCapsuleVPadding: CGFloat = 5
    /// 胶囊组总高（icon + 上下内边距），顶栏分割线等按此对齐。
    static let topBarCapsuleHeight: CGFloat = topBarIconSize + topBarCapsuleVPadding * 2

    // MARK: - 三栏布局（单点改基础值，派生值自动跟随）

    /// 卡片与窗口边缘的横向间隙（缝线两侧各取一半确保视觉等宽）。
    static let cardGap: CGFloat = 11
    /// 缝线单侧间隙 = (cardGap - seamWidth) / 2。
    static let seamHalfGap: CGFloat = 5
    /// 顶栏上/下纵向间隙（窗口上缘↔按钮、按钮↔卡片）。
    static let toolbarVGap: CGFloat = 6
    /// 卡片底部与窗口边缘的纵向间隙。
    static let cardBottomGap: CGFloat = 12

    /// 中间聊天区最小宽。
    static let middleMinWidth: CGFloat = 340
    /// 右栏最小宽。
    static let rightSidebarMinWidth: CGFloat = 340
    /// 分隔线（SeamMarker）宽度。
    static let seamWidth: CGFloat = 1

    /// 左栏导航项文字字号。
    static let sidebarNavFontSize: CGFloat = 16

    /// 输入栏内容宽低于该值时切两行布局（按钮移到编辑器下方）。
    static let inputBarTwoRowThreshold: CGFloat = 380

    /// 左栏卡片内容水平内边距（卡片背景内部呼吸区）。
    static let sidebarHPadding: CGFloat = 6

    /// 左栏卡片整体水平外扩（hpad×2 + 两侧 cardGap）。
    static let sidebarOuterPadding: CGFloat =
        sidebarHPadding * 2 + cardGap * 2
    /// 右栏卡片整体水平外扩（两侧 cardGap）。
    static let rightSidebarOuterPadding: CGFloat =
        cardGap * 2
    /// 中间 + 右栏（含右栏外扩 + 两个 seam）的最小横向跨度。
    static let middleAndRightMinSpan: CGFloat =
        seamWidth + middleMinWidth + seamWidth + rightSidebarMinWidth + rightSidebarOuterPadding
    /// 右栏打开时所需的内容最小宽（左栏收起 + 其余全部最小）。
    static let rightSidebarOpenMinContentWidth: CGFloat =
        sidebarMinWidth + sidebarOuterPadding + middleAndRightMinSpan

    /// 顶栏导航胶囊左缘对齐中部内容卡片左缘的偏移。
    /// 构成: cardGap(11) + sidebarHPadding×2(12) + cardGap(11) = 34
    static let navToolbarContentOffset: CGFloat = 34

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
    /// 收起态小条高度。
    static let floatingPanelCollapsedHeight: CGFloat = 40
    /// 面板内紧凑字号系数（乘在全局字号之上）。
    static let floatingPanelCompactFontFactor: Double = 0.75
}
