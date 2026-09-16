import Foundation

/// 聊天输入框焦点标记：InputBar 通过 @FocusState onChange 上报，供 AppDelegate 的
/// NSEvent monitor 判定 ⌘V 是否该由聊天接管（做法同 BrowserFocus.isUrlFieldFocused）。
///
/// 不能用 firstResponder is NSTextView 判定：整个窗口由一个 NSHostingView 承载，
/// 浏览器的地址栏/查找栏与聊天输入框同为 NSTextView，靠类型分不开。
enum ChatInputFocus {
    static var isFocused = false
}
