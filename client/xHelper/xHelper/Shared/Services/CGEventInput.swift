#if os(macOS)
import AppKit
import ApplicationServices
import CoreGraphics
import Foundation
import WebKit

/// CGEvent 真实键盘事件（macOS），只服务 `agent_browser_press`：Enter 提交表单、
/// 方向键滚动等浏览器默认行为靠合成事件不生效，必须注入真实按键。
/// 需要「辅助功能」权限；事件是系统级注入，注入前必须将目标窗口置于前台。
enum CGEventInput {
    /// 是否已获得辅助功能权限
    static func isTrusted() -> Bool {
        AXIsProcessTrusted()
    }

    /// 触发系统授权弹窗（系统设置 → 隐私与安全 → 辅助功能）
    @discardableResult
    static func requestPermission() -> Bool {
        let options = [kAXTrustedCheckOptionPrompt.takeUnretainedValue() as String: true] as CFDictionary
        return AXIsProcessTrustedWithOptions(options)
    }

    /// 将目标窗口置于前台（CGEvent 系统级注入的前提），等待激活完成
    static func activateWindow(for webView: WKWebView) {
        NSApp.activate(ignoringOtherApps: true)
        webView.window?.makeKeyAndOrderFront(nil)
        Thread.sleep(forTimeInterval: 0.15)
    }

    // MARK: - 键盘注入（真实默认行为：Enter 提交/方向键滚动）

    /// 特殊键 → 虚拟键码映射（真实键盘事件路径）
    static func keyCode(for key: String) -> CGKeyCode? {
        let map: [String: CGKeyCode] = [
            "Enter": 36, "Return": 36,
            "Tab": 48,
            "Escape": 53, "Esc": 53,
            "ArrowUp": 126, "Up": 126,
            "ArrowDown": 125, "Down": 125,
            "ArrowLeft": 123, "Left": 123,
            "ArrowRight": 124, "Right": 124,
            "Backspace": 51,
            "Delete": 117,
            "Space": 49,
            "Home": 115,
            "End": 119,
            "PageUp": 116,
            "PageDown": 121,
        ]
        if let code = map[key] { return code }
        return map[key.capitalized]
    }

    /// 真实键盘按键（keyDown → 随机间隔 → keyUp），触发浏览器默认行为
    static func pressKey(_ virtualKey: CGKeyCode) {
        let startTime = Date()
        guard let source = CGEventSource(stateID: .hidSystemState) else { return }
        CGEvent(keyboardEventSource: source, virtualKey: virtualKey, keyDown: true)?.post(tap: .cghidEventTap)
        let hold = Int.random(in: 40...90)
        usleep(useconds_t(hold * 1000))
        CGEvent(keyboardEventSource: source, virtualKey: virtualKey, keyDown: false)?.post(tap: .cghidEventTap)
        #if DEBUG
        print("[B-Press] vk=\(virtualKey) hold=\(hold)ms elapsed=\(Int(Date().timeIntervalSince(startTime) * 1000))ms")
        #endif
    }
}
#endif
