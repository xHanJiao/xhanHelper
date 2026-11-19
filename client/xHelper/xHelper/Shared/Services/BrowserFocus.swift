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
/// 注意：**Cmd+W 已不再用本判定**——右栏当前 tab（任意 kind、非首页）直接关，
/// 焦点停在哪都不改变意图（`docs/bugs/070`）。这里只服务于「右栏没有可关 tab」时的兜底。
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
              rs.isWebTab(rs.activeTabId),
              let page = BrowserToolProvider.shared.activePage,
              let window = page.hostingView.window,
              window.isKeyWindow
        else { return false }

        // 点在页面里时 firstResponder 是 Chromium 自己的子视图（不是 NSTextView），
        // 用"在页面视图子树里"认它 —— 比下面那条推断更直接，也不受 CEF 内部视图类型影响。
        if let responder = window.firstResponder as? NSView,
           responder.isDescendant(of: page.hostingView) {
            return true
        }
        // 正在编辑文本框：浏览器聚焦与否取决于该框是否为浏览器字段
        if window.firstResponder is NSTextView {
            return isUrlFieldFocused || isFindFieldFocused
        }
        // 页面内（WKWebView 及其子视图）或其他位置 → 浏览器聚焦
        return true
    }

    /// ⌘W / ⌘T 被吞掉时打一行：这条路径以前是纯静默的，用户只会看到"按了没反应"，
    /// 而焦点问题的成因（activePage 为 nil / 视图没在窗口 / 焦点在聊天输入）从外部完全看不出来。
    static func diagnosis() -> String {
        let rs = BrowserToolProvider.shared.rightSidebar
        let page = BrowserToolProvider.shared.activePage
        let window = page?.hostingView.window
        let responder = window?.firstResponder.map { String(describing: type(of: $0)) } ?? "nil"
        return "sidebar=\(rs != nil) open=\(rs?.isOpen == true) rsActive=\(rs?.activeTabId ?? "nil")"
            + " split=\(rs?.splitTabIds.count ?? 0) left=\(rs?.leftDisplayTabId ?? "nil")"
            + " right=\(rs?.rightDisplayTabId ?? "nil")"
            + " page=\(page != nil) window=\(window != nil) key=\(window?.isKeyWindow == true)"
            + " firstResponder=\(responder) urlField=\(isUrlFieldFocused) findField=\(isFindFieldFocused)"
            + " chatInput=\(ChatInputFocus.shared.isFocused)"
    }
#endif
}
