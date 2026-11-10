import AppKit
import Carbon.HIToolbox

/// 全局热键释放事件探针：验证 Carbon `RegisterEventHotKey` 在 macOS 26 上
/// 是否真的派发 `kEventHotKeyReleased`。按住说话要靠它收尾 —— 不派发就得换 CGEventTap。
///
/// 用法：
///   xcrun swiftc -o /tmp/hotkey-probe client/xHelper/scripts/hotkey-release-probe.swift \
///     -sdk "$(xcrun --show-sdk-path --sdk macosx)" -target arm64-apple-macos26.3
///   /tmp/hotkey-probe
/// 然后按住 ⌃⌥V 约 1 秒再松开，日志应出现 pressed 与 released 各一行。

func stamp() -> String {
    let formatter = DateFormatter()
    formatter.dateFormat = "HH:mm:ss.SSS"
    return formatter.string(from: Date())
}

var hotKeyRef: EventHotKeyRef?
var handlerRef: EventHandlerRef?
var eventTypes = [
    EventTypeSpec(eventClass: OSType(kEventClassKeyboard), eventKind: UInt32(kEventHotKeyPressed)),
    EventTypeSpec(eventClass: OSType(kEventClassKeyboard), eventKind: UInt32(kEventHotKeyReleased)),
]

let installStatus = InstallEventHandler(
    GetApplicationEventTarget(),
    { _, event, _ in
        guard let event else { return noErr }
        let kind = GetEventKind(event)
        let name: String
        switch kind {
        case UInt32(kEventHotKeyPressed): name = "pressed"
        case UInt32(kEventHotKeyReleased): name = "released"
        default: name = "kind=\(kind)"
        }
        print("\(stamp()) \(name)")
        fflush(stdout)
        return noErr
    },
    eventTypes.count,
    &eventTypes,
    nil,
    &handlerRef
)
guard installStatus == noErr else {
    print("InstallEventHandler failed: \(installStatus)")
    exit(1)
}

let hotKeyID = EventHotKeyID(signature: OSType(0x58484C50), id: 1) // 'XHLP'
let registerStatus = RegisterEventHotKey(
    UInt32(kVK_ANSI_V), UInt32(controlKey | optionKey),
    hotKeyID, GetApplicationEventTarget(), 0, &hotKeyRef
)
guard registerStatus == noErr else {
    print("RegisterEventHotKey failed: \(registerStatus)（-9878 = 键位被别的 app 占用）")
    exit(1)
}

print("ready: 按住 ⌃⌥V 约 1 秒再松开（Ctrl+C 退出）")
fflush(stdout)

let app = NSApplication.shared
app.setActivationPolicy(.accessory)
app.run()
