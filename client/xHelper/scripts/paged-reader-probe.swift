// 横向翻页（CSS 多栏）可行性探针 —— 在真 WKWebView 里跑一遍，只打印断言，不截图。
//
// 为什么要它：`design.md §5.7` 的翻页方案（多栏 + 横向滚动）能不能成立，取决于 WebKit
// 的真实行为 —— 分栏是否真的生成、跨栏划线/选区文本是否被改、大图会不会撑破栏。
// Chrome 测不准这些（引擎不同），所以用与 snapshot-reader 同一套离屏 WKWebView 跑。
//
// 由 paged-reader-probe.sh 调用：paged-reader-probe <html>
// 六条全过退出 0，有失败退出 1，加载/返回值不对退出 2。
import AppKit
import WebKit

final class Driver: NSObject, WKNavigationDelegate {
    private let web: WKWebView

    init(web: WKWebView) {
        self.web = web
    }

    func webView(_ w: WKWebView, didFinish n: WKNavigation!) {
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.9) {
            let fn = ProcessInfo.processInfo.environment["PROBE_FN"] ?? "__pagedProbe"
            w.evaluateJavaScript("window.\(fn) ? window.\(fn)() : 'NO_PROBE'") { value, error in
                guard let raw = value as? String else {
                    print("探针没有返回：\(error?.localizedDescription ?? "nil")")
                    exit(2)
                }
                guard let data = raw.data(using: .utf8),
                      let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
                      let rows = obj["rows"] as? [[String: Any]] else {
                    print("探针返回无法解析：\(raw.prefix(500))")
                    exit(2)
                }
                var failed = 0
                for row in rows {
                    let ok = (row["ok"] as? Bool) ?? false
                    if !ok { failed += 1 }
                    print("\(ok ? "PASS" : "FAIL")  \(row["name"] as? String ?? "?")")
                    print("      \(row["detail"] as? String ?? "")")
                }
                print(failed == 0 ? "全部通过（\(rows.count) 条）" : "\(failed)/\(rows.count) 条不通过")
                exit(failed == 0 ? 0 : 1)
            }
        }
    }

    func webView(_ w: WKWebView, didFail n: WKNavigation!, withError e: Error) {
        print("加载失败：\(e.localizedDescription)")
        exit(2)
    }

    func webView(_ w: WKWebView, didFailProvisionalNavigation n: WKNavigation!, withError e: Error) {
        print("加载失败：\(e.localizedDescription)")
        exit(2)
    }
}

let args = CommandLine.arguments
guard args.count > 1 else {
    print("用法: paged-reader-probe <html 路径>")
    exit(2)
}
let htmlPath = args[1]

// 与 snapshot-reader 同款：窗口只提供渲染链，不 activate、不抢焦点
NSApplication.shared.setActivationPolicy(.regular)
NSApp.appearance = NSAppearance(named: .aqua)
// 面板窄时（书阅读器在右栏里）用 PROBE_WIDTH 模拟；默认是文档阅读器的宽度
let probeWidth = CGFloat(Double(ProcessInfo.processInfo.environment["PROBE_WIDTH"] ?? "900") ?? 900)
let win = NSWindow(contentRect: NSRect(x: 0, y: 0, width: probeWidth, height: 820),
                   styleMask: [.titled], backing: .buffered, defer: false)
let web = WKWebView(frame: win.contentView!.bounds, configuration: WKWebViewConfiguration())
win.contentView!.addSubview(web)
win.makeKeyAndOrderFront(nil)
web.setValue(false, forKey: "drawsBackground")
web.underPageBackgroundColor = .clear
win.backgroundColor = .white
win.contentView!.wantsLayer = true
win.contentView!.layer?.backgroundColor = NSColor.white.cgColor

let driver = Driver(web: web)
web.navigationDelegate = driver
web.loadFileURL(URL(fileURLWithPath: htmlPath),
                allowingReadAccessTo: URL(fileURLWithPath: htmlPath).deletingLastPathComponent())

DispatchQueue.main.asyncAfter(deadline: .now() + 30) {
    print("超时（30s）")
    exit(2)
}
NSApplication.shared.run()
