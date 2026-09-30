import Foundation
import Combine

/// 聊天输入框焦点标记：InputBar 通过 @FocusState onChange 上报，供 AppDelegate 的
/// NSEvent monitor 判定 ⌘V 是否该由聊天接管（做法同 BrowserFocus.isUrlFieldFocused）。
///
/// 不能用 firstResponder is NSTextView 判定：整个窗口由一个 NSHostingView 承载，
/// 浏览器的地址栏/查找栏与聊天输入框同为 NSTextView，靠类型分不开。
///
/// 可观察化后还供文档详情页用：聊天输入框聚焦时把 ⌘←/⌘→ 还给文本框移光标，
/// 不让文档的「上一篇/下一篇」快捷键劫持。
///
/// 为何这里是 class 而 BrowserFocus 是 enum：SwiftUI 要靠 @Published 触发 body 重算，
/// 必须有引用类型；纯 NSEvent 判定（BrowserFocus）无需观察，enum + static var 即可。
/// 需要被 SwiftUI 观察 → class；只给 NSEvent monitor 读 → 沿用 enum，勿强行统一。
final class ChatInputFocus: ObservableObject {
    static let shared = ChatInputFocus()

    @Published var isFocused = false

    private init() {}
}
