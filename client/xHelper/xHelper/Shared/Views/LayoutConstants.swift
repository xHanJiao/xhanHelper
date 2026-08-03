import Foundation

enum Layout {
    static let chatHorizontalPadding: CGFloat = 24
    static let sidebarMinWidth: CGFloat = 48
    static let sidebarMaxWidth: CGFloat = 276
    static let sidebarSnapThreshold: CGFloat = 120
    static let sidebarDefaultWidth: CGFloat = 200

    static let toolbarHorizontalPadding: CGFloat = 16

    // MARK: - 三栏布局（单点改基础值，派生值自动跟随）

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

    /// 左栏卡片外水平 padding。
    static let sidebarHPadding: CGFloat = 12
    static let sidebarLeadingPadding: CGFloat = 8
    static let sidebarTrailingPadding: CGFloat = 12
    /// 右栏卡片外水平 padding。
    static let rightSidebarLeadingPadding: CGFloat = 12
    static let rightSidebarTrailingPadding: CGFloat = 8

    /// 左栏卡片整体水平外扩（hpad×2 + leading + trailing）。
    static let sidebarOuterPadding: CGFloat =
        sidebarHPadding * 2 + sidebarLeadingPadding + sidebarTrailingPadding
    /// 右栏卡片整体水平外扩（leading + trailing）。
    static let rightSidebarOuterPadding: CGFloat =
        rightSidebarLeadingPadding + rightSidebarTrailingPadding
    /// 中间 + 右栏（含右栏外扩 + 两个 seam）的最小横向跨度。
    static let middleAndRightMinSpan: CGFloat =
        seamWidth + middleMinWidth + seamWidth + rightSidebarMinWidth + rightSidebarOuterPadding
    /// 右栏打开时所需的内容最小宽（左栏收起 + 其余全部最小）。
    static let rightSidebarOpenMinContentWidth: CGFloat =
        sidebarMinWidth + sidebarOuterPadding + middleAndRightMinSpan

    /// 顶栏导航胶囊左缘对齐中部内容卡片左缘的偏移
    /// 构成: sidebarLeadingPad(8) + sidebarInnerHPad(12×2=24)
    ///      + sidebarTrailingPad(12) + contentLeadingPad(12)
    ///      - toolbarHPad(16) = 40
    static let navToolbarContentOffset: CGFloat = 40
}
