import Foundation
import Combine

/// 窗口最小内容宽的**唯一来源**：ContentView 算出来写这里，App 层根视图读它声明
/// `.frame(minWidth:)` —— 这条路径会被 SwiftUI 落到 `NSWindow.contentMinSize`，是唯一在
/// 所有缩放路径（拖拽 / 缩放 / 系统平铺 / 程序化）都被 AppKit 尊重的执行器；
/// `AppDelegate.windowWillResize` 也直接读它（第二道栅栏，只覆盖交互拖拽）。
/// 反例（已实测）：直接写 `window.contentMinSize` 会在下一轮布局被 SwiftUI 覆盖回根视图声明值。
/// 未进主界面（登录 / 首次配置页）时是 `defaultWidth`。详见 docs/bugs/033。
final class WindowMinWidthStore: ObservableObject {
    static let shared = WindowMinWidthStore()
    /// 未进入主界面时的默认值（与旧的 ContentView 根 `.frame(minWidth: 400)` 一致）。
    static let defaultWidth: CGFloat = 400

    @Published var width: CGFloat = WindowMinWidthStore.defaultWidth

    private init() {}
}
