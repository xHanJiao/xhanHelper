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

    func show(urlString: String? = nil) {
        present(urlString: urlString, probeRoot: nil)
    }

    private func present(urlString: String?, probeRoot: URL?) {
        if let window {
            window.makeKeyAndOrderFront(nil)
            return
        }

        guard XHCefRuntime.initialize(in: (probeRoot ?? LocalPaths.dataRoot).path) else {
            if probeRoot != nil {
                // 探针无人值守：弹模态就把进程锁死在弹窗上，报告永远不落盘
                finishProbe(#"{"error":"cef init failed"}"#)
            } else {
                let alert = NSAlert()
                alert.messageText = "CEF 起不来"
                alert.informativeText = "原因在 Console 里（过滤 [cef]）：缺 framework / 缺 Helper / CefInitialize 失败"
                alert.runModal()
            }
            return
        }

        let content = NSView(frame: NSRect(x: 0, y: 0, width: 1100, height: 760))
        let field = NSTextField(string: urlString ?? "https://www.baidu.com")
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

    // MARK: - 回归探针（scripts/cef-regression.sh）

    /// `--cef-probe[=url]`：加载一个会回显请求头的地址，把页面里看到的伪装结果落盘后退出。
    /// 伪装只能靠两头取证：服务端收到了什么头（httpbin 回显）+ 页面里 `navigator`/`window.chrome`
    /// 长什么样。CEF 执行 JS 没有返回值，所以走 `console.log("XHPROBE:…")` 回传。
    func runProbe(urlString: String) {
        // 探针用**独立 profile**：日常那个实例可能正开着，CEF 不能两个进程共用一份 cache_path
        // （cookie/History 会互相踩）。探针不需要登录态，干净 profile 反而更稳。
        let root = FileManager.default.temporaryDirectory.appendingPathComponent("xh-cef-probe")
        present(urlString: urlString, probeRoot: root)
        guard let browserView else {
            finishProbe(#"{"error":"cef browser view missing"}"#)
            return
        }
        window?.title = "CEF 探针"

        browserView.loadDidFinish = { [weak self] ok in
            MainActor.assumeIsolated {
                NSLog("[cef-probe] load ok=%@", ok ? "1" : "0")
                guard ok else {
                    self?.finishProbe(#"{"error":"load failed"}"#)
                    return
                }
                browserView.evaluateJavaScript(Self.probeScript)
            }
        }
        browserView.probeReported = { [weak self] payload in
            MainActor.assumeIsolated { self?.finishProbe(payload) }
        }

        // 无人值守跑：20s 没回传就自己收摊，别留个窗口卡住脚本
        DispatchQueue.main.asyncAfter(deadline: .now() + 20) { [weak self] in
            self?.finishProbe(#"{"error":"timeout"}"#)
        }
    }

    private var probeFinished = false

    private func finishProbe(_ payload: String) {
        guard !probeFinished else { return }
        probeFinished = true
        let path = LocalPaths.dataRoot.appendingPathComponent("cef-probe.json")
        do {
            try payload.write(to: path, atomically: true, encoding: .utf8)
            NSLog("[cef-probe] 报告已写 %@", path.path)
        } catch {
            NSLog("[cef-probe] 写报告失败：%@", error.localizedDescription)
        }
        NSApp.terminate(nil)
    }

    private static let probeScript = """
    (function () {
      var u = navigator.userAgentData || {};
      var data = {
        ua: navigator.userAgent,
        uad: u.brands ? { brands: u.brands, mobile: u.mobile, platform: u.platform,
                          tag: Object.prototype.toString.call(u) } : null,
        chrome: window.chrome ? Object.keys(window.chrome) : null,
        body: (document.body && document.body.innerText || "").slice(0, 4000)
      };
      console.log("XHPROBE:" + JSON.stringify(data));
    })()
    """

    func windowWillClose(_ notification: Notification) {
        // 冒烟只有一个 browser：关窗即回收，顺带验 CloseBrowser(force_close) → OnBeforeClose → Shutdown 这条路
        browserView?.closeBrowser()
        browserView = nil
        addressField = nil
        window = nil
    }
}
#endif
