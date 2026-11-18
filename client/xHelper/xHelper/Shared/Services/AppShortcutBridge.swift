import Foundation
#if os(macOS)
import Carbon.HIToolbox

/// App 级快捷键桥：AppDelegate 的 NSEvent monitor 无法直接访问 SwiftUI 的
/// @State/@StateObject，通过本单例把命令分发给注册者（ContentView onAppear 注册）。
///
/// 承载的快捷键（macOS）：
/// - Cmd+Shift+B         切换右侧栏（焦点无关，直接走 BrowserToolProvider.shared.rightSidebar）
/// - Cmd+N               新建对话（焦点无关）
/// - Cmd+Shift+↑ / ↓     会话列表上/下切换（焦点无关，delta -1/+1）
/// - Tab / Esc           有 L3 工具确认待处理时：Tab=允许，Esc=拒绝（无需 Command）
/// - Ctrl+Option+V       全局按住说话（Carbon 热键，按住/松开各回调一次）
/// - 双 ⌘ / ⌥ + 双 ⌘     全局按住说话（`VoiceHotKeyController` 轮询；加 `⌥` 表示另开新会话）
@MainActor
final class AppShortcutBridge {
    static let shared = AppShortcutBridge()

    /// 新建对话（由 ContentView 注册，走 chatTabManager.newChat）
    var onNewChat: (() -> Void)?

    /// 全局语音「新会话」档：新建会话并交出它的 VM（关窗时 ContentView 已销毁，
    /// 所以这里强持有 tab manager，不跟着视图生命周期走）
    var newChatViewModel: (() -> ChatViewModel?)?

    /// 会话列表上/下切换（由 ContentView 注册，delta：-1 上移，+1 下移）
    var onSwitchConversation: ((Int) -> Void)?

    /// ⌘V 粘贴的图片字节（由 ContentView 注册，投递给激活 tab 的 ChatViewModel）
    var onPasteImages: (([Data]) -> Void)?

    /// 当前是否有待处理的 L3 工具确认（由 ContentView 注册，查激活 tab 的 ChatViewModel）
    var confirmationPending: (() -> Bool)?

    /// 解析 L3 工具确认：true=允许，false=拒绝（由 ContentView 注册）
    var resolveConfirmation: ((Bool) -> Void)?

    /// 全局按住说话：按下开始录音、松开发送（由当前活跃 ChatView 注册）
    var onVoicePress: (() -> Void)?
    var onVoiceRelease: (() -> Void)?
    /// 谁占着全局语音热键（中栏与 PiP 各有自己的 VoiceChatService，只让活着的那个接热键）
    var voiceHotKeyOwner: ObjectIdentifier?
    /// 语音条是否开着（开着时全局热键走视图那套「按住暂存」，否则走一次性采集）
    var voiceBarActive: (() -> Bool)?
    /// 视图那套语音服务是否正在朗读（浮层「说」态要看它）
    var voiceIsSpeaking: (() -> Bool)?
    /// 立刻停掉视图那套的朗读（全局热键开录前先停，否则录进去的是 TTS）
    var stopVoicePlayback: (() -> Void)?
    /// 当前会话的 VM：窗口关着时全局热键也要能把话发出去
    var currentChatViewModel: (() -> ChatViewModel?)?
    /// 后端地址 + token：窗口关着时也要能建连
    var voiceCredentials: (() -> (baseURL: String, token: String)?)?

    private init() {}
}

/// 全局热键（Carbon）：不需要辅助功能授权，且能吞掉按键（不会漏给前台 app）。
/// 同时监听 pressed / released —— 按住说话靠 released 收尾；万一系统不派发，
/// `VoiceChatService` 里有到上限自动发送兜底。
@MainActor
final class GlobalHotKey {
    private static var current: GlobalHotKey?
    private var hotKeyRef: EventHotKeyRef?
    private var handlerRef: EventHandlerRef?
    private let onPress: () -> Void
    private let onRelease: () -> Void

    private init(onPress: @escaping () -> Void, onRelease: @escaping () -> Void) {
        self.onPress = onPress
        self.onRelease = onRelease
    }

    /// ⌃⌥V。注册失败（`-9878` = 键位被别的 app 占了）返回 false，由调用方记日志。
    @discardableResult
    static func registerVoicePushToTalk(onPress: @escaping () -> Void,
                                        onRelease: @escaping () -> Void) -> Bool {
        unregister()
        let hotKey = GlobalHotKey(onPress: onPress, onRelease: onRelease)
        var eventTypes = [
            EventTypeSpec(eventClass: OSType(kEventClassKeyboard),
                          eventKind: UInt32(kEventHotKeyPressed)),
            EventTypeSpec(eventClass: OSType(kEventClassKeyboard),
                          eventKind: UInt32(kEventHotKeyReleased)),
        ]
        let installStatus = InstallEventHandler(
            GetApplicationEventTarget(),
            { _, event, _ in
                guard let event else { return noErr }
                let kind = GetEventKind(event)
                MainActor.assumeIsolated {
                    guard let hotKey = GlobalHotKey.current else { return }
                    if kind == UInt32(kEventHotKeyPressed) {
                        hotKey.onPress()
                    } else if kind == UInt32(kEventHotKeyReleased) {
                        hotKey.onRelease()
                    }
                }
                return noErr
            },
            eventTypes.count,
            &eventTypes,
            nil,
            &hotKey.handlerRef
        )
        guard installStatus == noErr else {
            LogBuffer.shared.warning("[hotkey] InstallEventHandler failed: \(installStatus)")
            return false
        }
        let hotKeyID = EventHotKeyID(signature: OSType(0x58484C50), id: 1) // 'XHLP'
        let registerStatus = RegisterEventHotKey(
            UInt32(kVK_ANSI_V), UInt32(controlKey | optionKey),
            hotKeyID, GetApplicationEventTarget(), 0, &hotKey.hotKeyRef
        )
        guard registerStatus == noErr else {
            LogBuffer.shared.warning("[hotkey] RegisterEventHotKey failed: \(registerStatus)")
            if let handler = hotKey.handlerRef { RemoveEventHandler(handler) }
            return false
        }
        current = hotKey
        return true
    }

    static func unregister() {
        guard let hotKey = current else { return }
        if let ref = hotKey.hotKeyRef { UnregisterEventHotKey(ref) }
        if let handler = hotKey.handlerRef { RemoveEventHandler(handler) }
        current = nil
    }
}

#endif
