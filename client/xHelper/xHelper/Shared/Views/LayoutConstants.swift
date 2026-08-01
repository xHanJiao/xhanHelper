import Foundation

enum Layout {
    static let chatHorizontalPadding: CGFloat = 24
    static let sidebarMinWidth: CGFloat = 48
    static let sidebarMaxWidth: CGFloat = 360
    static let sidebarSnapThreshold: CGFloat = 120
    static let sidebarDefaultWidth: CGFloat = 200

    static let toolbarHorizontalPadding: CGFloat = 16

    /// 顶栏导航胶囊左缘对齐中部内容卡片左缘的偏移
    /// 构成: sidebarLeadingPad(8) + sidebarInnerHPad(12×2=24)
    ///      + sidebarTrailingPad(12) + contentLeadingPad(12)
    ///      - toolbarHPad(16) = 40
    static let navToolbarContentOffset: CGFloat = 40
}
