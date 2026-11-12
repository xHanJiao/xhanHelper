#if DEBUG
import SwiftUI
import AppKit

/// CEF 合成冒烟入口（**隐藏调试口，无菜单项**）：⌃⌥⌘B。
///
/// 只验「能不能内嵌」这件门：窗口叠加与他人视图共存、输入法候选、⌘ 快捷键不被吞、
/// 底图/材质上有没有灰闪或黑帧。浏览器功能一律不在这里验（那是 B2 之后的事）。
/// 依据：docs/design/mac-apps/browser-chromium-migration.md 第十三章 + docs/bugs/030。
@MainActor
final class CefSmokeWindowController: NSObject, NSWindowDelegate {
    static let shared = CefSmokeWindowController()

    private var window: NSWindow?
    private var browserView: XHCefBrowserView?
    private var addressField: NSTextField?

    func show() {
        if let window {
            window.makeKeyAndOrderFront(nil)
            return
        }

        var initError: NSError?
        guard XHCefRuntime.initializeIn(LocalPaths.dataRoot.path, error: &initError) else {
            let alert = NSAlert()
            alert.messageText = "CEF 起不来"
            alert.informativeText = initError?.localizedDescription ?? "未知原因"
            alert.runModal()
            return
        }

        let content = NSView(frame: NSRect(x: 0, y: 0, width: 1100, height: 760))
        let field = NSTextField(string: "https://www.baidu.com")
        field.placeholderString = "输入地址，回车打开"
        field.target = self
        field.action = #selector(openTypedURL)

        let browser = XHCefBrowserView(frame: .zero, urlString: field.stringValue)
        browser.titleDidChange = { [weak self] title in
            MainActor.assumeIsolated {
                self?.window?.title = "CEF 冒烟 · \(title)"
            }
        }
        browser.loadDidFinish = { ok in
            // 冒烟只看控制台：加载失败要能一眼看见，别静默白屏
            NSLog("[cef-smoke] loadDidFinish ok=%@", ok ? "1" : "0")
        }

        for view in [field, browser] {
            view.translatesAutoresizingMaskIntoConstraints = false
            content.addSubview(view)
        }
        NSLayoutConstraint.activate([
            field.topAnchor.constraint(equalTo: content.topAnchor, constant: 8),
            field.leadingAnchor.constraint(equalTo: content.leadingAnchor, constant: 8),
            field.trailingAnchor.constraint(equalTo: content.trailingAnchor, constant: -8),
            browser.topAnchor.constraint(equalTo: field.bottomAnchor, constant: 8),
            browser.leadingAnchor.constraint(equalTo: content.leadingAnchor),
            browser.trailingAnchor.constraint(equalTo: content.trailingAnchor),
            browser.bottomAnchor.constraint(equalTo: content.bottomAnchor),
        ])

        let window = NSWindow(
            contentRect: content.frame,
            styleMask: [.titled, .closable, .resizable, .miniaturizable],
            backing: .buffered,
            defer: false
        )
        window.title = "CEF 冒烟"
        window.contentView = content
        window.delegate = self
        window.center()
        window.makeKeyAndOrderFront(nil)

        self.window = window
        self.addressField = field
        self.browserView = browser
    }

    @objc private func openTypedURL() {
        guard let addressField else { return }
        browserView?.loadURLString(addressField.stringValue)
    }

    func windowWillClose(_ notification: Notification) {
        // 冒烟只有一个 browser：关窗即回收，顺带验 CloseBrowser(force_close) → OnBeforeClose → Shutdown 这条路
        browserView?.closeBrowser()
        browserView = nil
        addressField = nil
        window = nil
    }
}
#endif
