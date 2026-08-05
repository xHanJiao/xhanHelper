import Foundation
#if os(macOS)

/// App 级快捷键桥：AppDelegate 的 NSEvent monitor 无法直接访问 SwiftUI 的
/// @State/@StateObject，通过本单例把命令分发给注册者（ContentView onAppear 注册）。
///
/// 承载的快捷键（macOS）：
/// - Cmd+B               切换左侧栏（焦点无关）
/// - Cmd+Shift+B         切换右侧栏（焦点无关，直接走 BrowserToolProvider.shared.rightSidebar）
/// - Cmd+N               新建对话（焦点无关）
/// - Cmd+Shift+↑ / ↓     会话列表上/下切换（焦点无关，delta -1/+1）
/// - Tab / Esc           有 L3 工具确认待处理时：Tab=允许，Esc=拒绝（无需 Command）
@MainActor
final class AppShortcutBridge {
    static let shared = AppShortcutBridge()

    /// 切换左侧栏（由 ContentView 注册，操作其 @State sidebarWidth）
    var onToggleLeftSidebar: (() -> Void)?

    /// 新建对话（由 ContentView 注册，走 chatTabManager.newChat）
    var onNewChat: (() -> Void)?

    /// 会话列表上/下切换（由 ContentView 注册，delta：-1 上移，+1 下移）
    var onSwitchConversation: ((Int) -> Void)?

    /// 当前是否有待处理的 L3 工具确认（由 ContentView 注册，查激活 tab 的 ChatViewModel）
    var confirmationPending: (() -> Bool)?

    /// 解析 L3 工具确认：true=允许，false=拒绝（由 ContentView 注册）
    var resolveConfirmation: ((Bool) -> Void)?

    private init() {}
}

#endif
