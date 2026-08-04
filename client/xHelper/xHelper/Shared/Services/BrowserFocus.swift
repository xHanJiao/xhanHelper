import Foundation
import WebKit
#if os(macOS)
import AppKit
#endif

/// 浏览器焦点判定与快捷键路由（Tier 1 UX，macOS）。
///
/// 判定规则（详见 docs/design/mac-apps/browser-tier1-ux.md §3）：
/// 1. 浏览器态（activeTabId != nil，非浏览面板态）；
/// 2. 激活 tab 的 webView 所在窗口为 keyWindow；
/// 3. firstResponder 判定：
///    - 正在编辑文本框（field editor / NSTextView）→ 浏览器聚焦 = 该框是否为浏览器字段
///      （地址栏/查找栏，由 BrowserTabView 通过 FocusState 上报）；
///    - 其他（页面内 WKWebView、窗口、按钮、无焦点）→ 视为浏览器聚焦
///      （对齐 Chrome：有激活 tab 且未在别处输入时，Cmd+W 关 tab）。
///
/// 不用 superview 链判定"浏览器容器"：SwiftUI 窗口整个由单个根 NSHostingView 承载，
/// webView 与聊天输入框同为该宿主视图的子视图，无法靠祖先区分。
enum BrowserFocus {
    /// 地址栏是否聚焦（BrowserTabView 通过 @FocusState onChange 上报，全平台可用）
    static var isUrlFieldFocused = false
    /// 查找栏是否聚焦（同上）
    static var isFindFieldFocused = false

#if os(macOS)
    /// 是否浏览器聚焦
    static func isFocused() -> Bool {
        guard let rs = BrowserToolProvider.shared.rightSidebar,
              rs.activeTabId != nil,
              let webView = BrowserToolProvider.shared.activeWebView,
              let window = webView.window,
              window.isKeyWindow
        else { return false }

        // 正在编辑文本框：浏览器聚焦与否取决于该框是否为浏览器字段
        if window.firstResponder is NSTextView {
            return isUrlFieldFocused || isFindFieldFocused
        }
        // 页面内（WKWebView 及其子视图）或其他位置 → 浏览器聚焦
        return true
    }
#endif
}
