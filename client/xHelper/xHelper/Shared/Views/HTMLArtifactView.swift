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

    var body: some View {
        ZStack {
            // 不透明纸底：别让页面直接压在照片底图上（docs/bugs/030 那类灰闪）
            Color.white
            ArtifactWebView(html: html)
        }
        .ignoresSafeArea()
    }
}

#if os(macOS)
private struct ArtifactWebView: NSViewRepresentable {
    let html: String

    func makeNSView(context: Context) -> WKWebView {
        makeWebView(html: html)
    }

    func updateNSView(_ webView: WKWebView, context: Context) {
        // html 一变（重新生成/切到另一篇）就重载；内容相同不重复 load
        guard context.coordinator.loadedHTML != html else { return }
        context.coordinator.loadedHTML = html
        webView.loadHTMLString(html, baseURL: nil)
    }

    func makeCoordinator() -> Coordinator { Coordinator(loadedHTML: html) }

    final class Coordinator {
        var loadedHTML: String
        init(loadedHTML: String) { self.loadedHTML = loadedHTML }
    }
}
#else
private struct ArtifactWebView: UIViewRepresentable {
    let html: String

    func makeUIView(context: Context) -> WKWebView {
        makeWebView(html: html)
    }

    func updateUIView(_ webView: WKWebView, context: Context) {
        guard context.coordinator.loadedHTML != html else { return }
        context.coordinator.loadedHTML = html
        webView.loadHTMLString(html, baseURL: nil)
    }

    func makeCoordinator() -> Coordinator { Coordinator(loadedHTML: html) }

    final class Coordinator {
        var loadedHTML: String
        init(loadedHTML: String) { self.loadedHTML = loadedHTML }
    }
}
#endif

/// 关掉 JS、禁掉新窗口、取消一切导航（页面是自包含单文件，不该再跳）。
private func makeWebView(html: String) -> WKWebView {
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
    webView.loadHTMLString(html, baseURL: nil)
    return webView
}

#Preview("页面") {
    HTMLArtifactView(html: "<html><body><h1>预览</h1><p>内容</p></body></html>")
        .frame(width: 700, height: 500)
}
