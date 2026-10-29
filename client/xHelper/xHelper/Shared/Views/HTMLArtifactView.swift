import SwiftUI
import WebKit

/// 右栏里的 HTML 产物页。
///
/// 安全靠引擎而不是靠清洗：`javaScriptEnabled = false` + 取消一切导航。
/// 入库时已经清过危险标签并注入 CSP（docs/design/html-artifact §七），这里是第二道闸。
///
/// 内存约定：**不要**把这个视图塞进 `RightSidebarTab.content` 的构造参数、也不要进
/// `keptAliveTabIds`——切走即销毁，切回来重新 `loadHTMLString`（页面无 JS 无网络，重载可忽略）。
struct HTMLArtifactView: View {
    let html: String
    @State private var loadFailed = false

    var body: some View {
        ZStack {
            // 不透明纸底：别让页面直接压在照片底图上（docs/bugs/030 那类灰闪）
            Color.white
            ArtifactWebView(html: html) { message in
                loadFailed = true
                ToastCenter.shared.show("页面渲染失败：\(message)")
            }
            if loadFailed {
                // 别留一片纯白：至少说清发生了什么
                VStack(spacing: 8) {
                    Image(systemName: "exclamationmark.triangle")
                        .font(.system(size: 28))
                    Text("页面没能渲染出来")
                        .font(.system(size: 13, weight: .medium))
                }
                .foregroundColor(Color("assistantText").opacity(0.5))
                .allowsHitTesting(false)
            }
        }
        .ignoresSafeArea()
    }
}

/// 取消一切非 about: 导航（页面是自包含单文件，不该再跳），并把加载失败报给视图。
/// 必须由 Coordinator 强引用：`WKWebView.navigationDelegate` 是 weak。
private final class ArtifactNavigationGuard: NSObject, WKNavigationDelegate {
    var onFail: ((String) -> Void)?

    func webView(_ webView: WKWebView,
                 decidePolicyFor navigationAction: WKNavigationAction,
                 decisionHandler: @escaping (WKNavigationActionPolicy) -> Void) {
        // loadHTMLString 的首个导航是 about:blank（+ 页内锚点），其余（data:/http:…）一律取消
        let scheme = navigationAction.request.url?.scheme?.lowercased()
        decisionHandler(scheme == nil || scheme == "about" ? .allow : .cancel)
    }

    func webView(_ webView: WKWebView, didFailProvisionalNavigation navigation: WKNavigation!, withError error: Error) {
        // 被我们主动 cancel 的导航（点外链）不算加载失败，别误报
        let ns = error as NSError
        if ns.code == NSURLErrorCancelled { return }
        onFail?(ns.localizedDescription)
    }
}

#if os(macOS)
private struct ArtifactWebView: NSViewRepresentable {
    let html: String
    let onFail: (String) -> Void

    func makeNSView(context: Context) -> WKWebView {
        makeWebView(html: html, delegate: context.coordinator.navigationGuard)
    }

    func updateNSView(_ webView: WKWebView, context: Context) {
        // html 一变（重新生成/切到另一篇）就重载；内容相同不重复 load
        guard context.coordinator.loadedHTML != html else { return }
        context.coordinator.loadedHTML = html
        webView.loadHTMLString(html, baseURL: nil)
    }

    func makeCoordinator() -> Coordinator { Coordinator(loadedHTML: html, onFail: onFail) }

    final class Coordinator {
        var loadedHTML: String
        let navigationGuard = ArtifactNavigationGuard()

        init(loadedHTML: String, onFail: @escaping (String) -> Void) {
            self.loadedHTML = loadedHTML
            navigationGuard.onFail = onFail
        }
    }
}
#else
private struct ArtifactWebView: UIViewRepresentable {
    let html: String
    let onFail: (String) -> Void

    func makeUIView(context: Context) -> WKWebView {
        makeWebView(html: html, delegate: context.coordinator.navigationGuard)
    }

    func updateUIView(_ webView: WKWebView, context: Context) {
        guard context.coordinator.loadedHTML != html else { return }
        context.coordinator.loadedHTML = html
        webView.loadHTMLString(html, baseURL: nil)
    }

    func makeCoordinator() -> Coordinator { Coordinator(loadedHTML: html, onFail: onFail) }

    final class Coordinator {
        var loadedHTML: String
        let navigationGuard = ArtifactNavigationGuard()

        init(loadedHTML: String, onFail: @escaping (String) -> Void) {
            self.loadedHTML = loadedHTML
            navigationGuard.onFail = onFail
        }
    }
}
#endif

/// 关掉 JS、禁掉新窗口、挂导航守卫。
private func makeWebView(html: String, delegate: WKNavigationDelegate) -> WKWebView {
    let config = WKWebViewConfiguration()
    let prefs = WKWebpagePreferences()
    prefs.allowsContentJavaScript = false
    config.defaultWebpagePreferences = prefs
    config.preferences.javaScriptCanOpenWindowsAutomatically = false
    let webView = WKWebView(frame: .zero, configuration: config)
    #if os(macOS)
    webView.setValue(false, forKey: "drawsBackground")
    webView.allowsMagnification = true
    #endif
    webView.navigationDelegate = delegate
    webView.loadHTMLString(html, baseURL: nil)
    return webView
}

#Preview("页面") {
    HTMLArtifactView(html: "<html><body><h1>预览</h1><p>内容</p></body></html>")
        .frame(width: 700, height: 500)
}
