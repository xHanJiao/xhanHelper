#if os(macOS)
import AppKit
import WebKit

/// App 内浏览器 WebView：拦截 WebKit 右键菜单的「用X搜索」项，
/// 改为在 App 内新 tab 打开搜索结果（否则 WebKit 直接调 NSWorkspace 跳到默认浏览器 Safari）。
final class AppBrowserWebView: WKWebView {

    override func willOpenMenu(_ menu: NSMenu, with event: NSEvent) {
        super.willOpenMenu(menu, with: event)
        for item in menu.items where Self.isSearchMenuItem(item) {
            item.target = self
            item.action = #selector(performInAppSearch(_:))
        }
    }

    private static func isSearchMenuItem(_ item: NSMenuItem) -> Bool {
        let title = item.title.trimmingCharacters(in: .whitespaces)
        return title.contains("搜索") || title.hasPrefix("Search with")
    }

    @objc private func performInAppSearch(_ sender: NSMenuItem) {
        let engine = Self.engineName(from: sender.title)
        Task { @MainActor in
            let text = await selectionText()
            let query = text.trimmingCharacters(in: .whitespacesAndNewlines)
            guard !query.isEmpty,
                  let url = Self.searchURL(for: query, engine: engine) else { return }
            BrowserToolProvider.shared.openURL(url)
        }
    }

    private func selectionText() async -> String {
        do {
            let result = try await evaluateJavaScript("window.getSelection().toString()")
            return result as? String ?? ""
        } catch {
            return ""
        }
    }

    private static func engineName(from title: String) -> String {
        let t = title.trimmingCharacters(in: .whitespaces)
        if t.hasPrefix("用"), t.hasSuffix("搜索") {
            return String(t.dropFirst().dropLast())
        }
        if t.hasPrefix("Search with ") {
            return String(t.dropFirst("Search with ".count))
        }
        return t
    }

    private static func searchURL(for query: String, engine: String) -> URL? {
        guard let encoded = query.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed) else { return nil }
        let name = engine.trimmingCharacters(in: .whitespaces).lowercased()
        let base: String
        switch name {
        case "百度", "baidu": base = "https://www.baidu.com/s?wd="
        case "google": base = "https://www.google.com/search?q="
        case "必应", "bing": base = "https://cn.bing.com/search?q="
        case "搜狗", "sogou": base = "https://www.sogou.com/web?query="
        default: base = "https://www.baidu.com/s?wd=" // 未知引擎回退百度
        }
        return URL(string: base + encoded)
    }
}
#endif
