import SwiftUI
import Combine
import WebKit

// MARK: - 导航白名单

/// 允许导航的同源定义。scheme + host + port 精确比较（nil port = 默认端口），
/// 防止 `localhost:5173` vs `localhost:51730` 这类前缀混淆。
struct XHWebViewOrigin: Equatable {
    let scheme: String
    let host: String
    let port: Int?

    init(scheme: String, host: String, port: Int?) {
        self.scheme = scheme
        self.host = host
        self.port = port
    }

    /// 从 URL 字符串推导（本地模式的内嵌前端地址是动态的，不能写死）。
    init?(_ urlString: String) {
        guard let url = URL(string: urlString),
              let scheme = url.scheme, let host = url.host else { return nil }
        self.init(scheme: scheme, host: host, port: url.port)
    }

    func matches(_ url: URL) -> Bool {
        guard url.scheme == scheme, url.host == host else { return false }
        let urlPort = url.port ?? (scheme == "https" ? 443 : scheme == "http" ? 80 : nil)
        return (port ?? urlPort) == urlPort
    }

    /// 前端面板通用白名单：线上 + dev Vite + 本地内嵌（127.0.0.1:18923）+ 由 `webBaseURL` 推导的同源。
    /// 本地模式下监控看板 / 消息调试面板打开的是 `http://127.0.0.1:18923/...`，漏了会直接空白。
    static func frontendOrigins(webBaseURL: String) -> [XHWebViewOrigin] {
        var origins: [XHWebViewOrigin] = [
            XHWebViewOrigin(scheme: "https", host: "helper.xhanx.cn", port: nil),
            XHWebViewOrigin(scheme: "http", host: "localhost", port: 5173),
            XHWebViewOrigin(scheme: "http", host: "127.0.0.1", port: 18923),
        ]
        if let derived = XHWebViewOrigin(webBaseURL), !origins.contains(derived) {
            origins.append(derived)
        }
        return origins
    }
}

// MARK: - 共享状态

/// XHWebView 的共享状态与控制器。
/// 由外部持有（如面板 header 的刷新按钮），驱动加载/错误状态展示。
@MainActor
final class XHWebViewModel: ObservableObject {
    @Published var isLoading = false
    @Published var loadError: String?
    @Published var blockedNotice: String?
    @Published private(set) var currentURL: URL?

    private weak var webView: WKWebView?

    private var noticeTask: Task<Void, Never>?

    func attach(_ webView: WKWebView) {
        self.webView = webView
    }

    /// 外部刷新入口（面板 header 刷新按钮）。reloadFromOrigin 忽略本地缓存强制回源，
    /// 部署新版本后点刷新必拉到最新。
    func reload() {
        loadError = nil
        if let webView {
            if webView.url != nil {
                webView.reloadFromOrigin()
            } else if let currentURL {
                webView.load(URLRequest(url: currentURL))
            }
        }
    }

    /// 白名单外导航被拦截时调用，底部 toast 提示，2.5s 自动消退。
    func showBlockedNotice(_ message: String) {
        blockedNotice = message
        noticeTask?.cancel()
        noticeTask = Task { [weak self] in
            try? await Task.sleep(nanoseconds: 2_500_000_000)
            guard !Task.isCancelled else { return }
            self?.blockedNotice = nil
        }
    }

    func setCurrentURL(_ url: URL) {
        currentURL = url
    }
}

// MARK: - 基础 WebView

/// 无浏览器 chrome 的基础 WebView。
/// 业务无关：只负责渲染 + 加载/错误状态 + 导航白名单拦截。
/// 调用方通过 `configuration` 注入 user scripts 与 script message handlers（见 XHWebViewBridge）。
struct XHWebView: View {
    @ObservedObject var model: XHWebViewModel
    let url: URL
    let configuration: WKWebViewConfiguration
    var allowedOrigins: [XHWebViewOrigin] = []

    var body: some View {
        XHWebViewRepresentable(model: model, url: url, configuration: configuration, allowedOrigins: allowedOrigins)
            .onAppear { model.setCurrentURL(url) }
    }
}

#if os(macOS)
struct XHWebViewRepresentable: NSViewRepresentable {
    @ObservedObject var model: XHWebViewModel
    let url: URL
    let configuration: WKWebViewConfiguration
    var allowedOrigins: [XHWebViewOrigin] = []

    func makeCoordinator() -> Coordinator { Coordinator(model: model, allowedOrigins: allowedOrigins) }

    func makeNSView(context: Context) -> WKWebView {
        let webView = WKWebView(frame: .zero, configuration: configuration)
        webView.navigationDelegate = context.coordinator
        webView.uiDelegate = context.coordinator
        context.coordinator.webView = webView
        model.attach(webView)
        webView.load(URLRequest(url: url))
        return webView
    }

    func updateNSView(_ webView: WKWebView, context: Context) {
        // 仅当页面整体地址变化时重新加载；忽略 hash/query 变化（SPA 内导航不重载）
        let current = webView.url?.absoluteString ?? ""
        if !current.hasPrefix(url.absoluteString) {
            webView.load(URLRequest(url: url))
        }
    }

    static func dismantleNSView(_ webView: WKWebView, coordinator: Coordinator) {
        webView.stopLoading()
        webView.navigationDelegate = nil
        webView.uiDelegate = nil
    }

    @MainActor
    final class Coordinator: NSObject, WKNavigationDelegate, WKUIDelegate {
        let model: XHWebViewModel
        let allowedOrigins: [XHWebViewOrigin]
        weak var webView: WKWebView?

        init(model: XHWebViewModel, allowedOrigins: [XHWebViewOrigin]) {
            self.model = model
            self.allowedOrigins = allowedOrigins
        }

        // MARK: WKNavigationDelegate

        func webView(_ webView: WKWebView, didStartProvisionalNavigation navigation: WKNavigation!) {
            model.isLoading = true
        }

        func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) {
            model.isLoading = false
        }

        func webView(_ webView: WKWebView, didFail navigation: WKNavigation!, withError error: Error) {
            handleLoadError(error)
        }

        func webView(_ webView: WKWebView, didFailProvisionalNavigation navigation: WKNavigation!, withError error: Error) {
            handleLoadError(error)
        }

        private func handleLoadError(_ error: Error) {
            model.isLoading = false
            let nsError = error as NSError
            // 主动取消（stop/reload 竞态）不算错误
            guard nsError.code != NSURLErrorCancelled else { return }
            model.loadError = error.localizedDescription
        }

        /// 导航白名单：白名单内放行，白名单外静默拦截 + toast 提示。
        /// javascript:/data:/about: 等非白名单 scheme 一律 cancel，天然防注入执行。
        func webView(_ webView: WKWebView, decidePolicyFor navigationAction: WKNavigationAction,
                     decisionHandler: @escaping (WKNavigationActionPolicy) -> Void) {
            guard let url = navigationAction.request.url else {
                decisionHandler(.cancel)
                return
            }
            if allowedOrigins.contains(where: { $0.matches(url) }) {
                decisionHandler(.allow)
                return
            }
            // 静默拦截（不抛系统浏览器），弹 toast 提示
            let host = url.host ?? url.absoluteString
            model.showBlockedNotice("已拦截外部跳转：\(host)")
            decisionHandler(.cancel)
        }

        /// window.open() / 新窗口：静默拦截
        func webView(_ webView: WKWebView, createWebViewWith configuration: WKWebViewConfiguration,
                     for navigationAction: WKNavigationAction, windowFeatures: WKWindowFeatures) -> WKWebView? {
            if let url = navigationAction.request.url {
                let host = url.host ?? url.absoluteString
                model.showBlockedNotice("已拦截新窗口：\(host)")
            }
            return nil
        }
    }
}
#else
struct XHWebViewRepresentable: UIViewRepresentable {
    @ObservedObject var model: XHWebViewModel
    let url: URL
    let configuration: WKWebViewConfiguration
    var allowedOrigins: [XHWebViewOrigin] = []

    func makeCoordinator() -> Coordinator { Coordinator(model: model, allowedOrigins: allowedOrigins) }

    func makeUIView(context: Context) -> WKWebView {
        let webView = WKWebView(frame: .zero, configuration: configuration)
        webView.navigationDelegate = context.coordinator
        webView.uiDelegate = context.coordinator
        context.coordinator.webView = webView
        model.attach(webView)
        webView.load(URLRequest(url: url))
        return webView
    }

    func updateUIView(_ webView: WKWebView, context: Context) {
        // 仅当页面整体地址变化时重新加载；忽略 hash/query 变化（SPA 内导航不重载）
        let current = webView.url?.absoluteString ?? ""
        if !current.hasPrefix(url.absoluteString) {
            webView.load(URLRequest(url: url))
        }
    }

    static func dismantleUIView(_ webView: WKWebView, coordinator: Coordinator) {
        webView.stopLoading()
        webView.navigationDelegate = nil
        webView.uiDelegate = nil
    }

    @MainActor
    final class Coordinator: NSObject, WKNavigationDelegate, WKUIDelegate {
        let model: XHWebViewModel
        let allowedOrigins: [XHWebViewOrigin]
        weak var webView: WKWebView?

        init(model: XHWebViewModel, allowedOrigins: [XHWebViewOrigin]) {
            self.model = model
            self.allowedOrigins = allowedOrigins
        }

        func webView(_ webView: WKWebView, didStartProvisionalNavigation navigation: WKNavigation!) {
            model.isLoading = true
        }

        func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) {
            model.isLoading = false
        }

        func webView(_ webView: WKWebView, didFail navigation: WKNavigation!, withError error: Error) {
            handleLoadError(error)
        }

        func webView(_ webView: WKWebView, didFailProvisionalNavigation navigation: WKNavigation!, withError error: Error) {
            handleLoadError(error)
        }

        private func handleLoadError(_ error: Error) {
            model.isLoading = false
            let nsError = error as NSError
            guard nsError.code != NSURLErrorCancelled else { return }
            model.loadError = error.localizedDescription
        }

        func webView(_ webView: WKWebView, decidePolicyFor navigationAction: WKNavigationAction,
                     decisionHandler: @escaping (WKNavigationActionPolicy) -> Void) {
            guard let url = navigationAction.request.url else {
                decisionHandler(.cancel)
                return
            }
            if allowedOrigins.contains(where: { $0.matches(url) }) {
                decisionHandler(.allow)
                return
            }
            let host = url.host ?? url.absoluteString
            model.showBlockedNotice("已拦截外部跳转：\(host)")
            decisionHandler(.cancel)
        }

        func webView(_ webView: WKWebView, createWebViewWith configuration: WKWebViewConfiguration,
                     for navigationAction: WKNavigationAction, windowFeatures: WKWindowFeatures) -> WKWebView? {
            if let url = navigationAction.request.url {
                let host = url.host ?? url.absoluteString
                model.showBlockedNotice("已拦截新窗口：\(host)")
            }
            return nil
        }
    }
}
#endif

// MARK: - 组合面板（WebView + 加载/错误 overlay）

/// 通用无 chrome WebView 面板：XHWebView + 加载指示器 + 错误重试 + 拦截提示。
/// 业务无关，配合 XHPanel 使用。
struct XHWebViewPanel: View {
    @ObservedObject var model: XHWebViewModel
    let url: URL
    let configuration: WKWebViewConfiguration
    var allowedOrigins: [XHWebViewOrigin] = []

    var body: some View {
        ZStack {
            XHWebView(model: model, url: url, configuration: configuration, allowedOrigins: allowedOrigins)
                // 每次面板打开强制回源：WKWebView 持久缓存会抱着旧 index.html，
                // 部署新前端后自动失效（reload() 内部走 reloadFromOrigin）
                .onAppear { model.reload() }

            if model.isLoading && model.loadError == nil {
                ProgressView()
                    .controlSize(.small)
                    .padding(10)
                    .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 8))
            }

            if let error = model.loadError {
                VStack(spacing: 12) {
                    Image(systemName: "exclamationmark.triangle")
                        .font(.system(size: 28))
                        .foregroundColor(.secondary.opacity(0.6))
                    VStack(spacing: 4) {
                        Text("无法加载页面")
                            .font(.system(size: 13, weight: .semibold))
                        Text(error)
                            .font(.system(size: 11))
                            .foregroundColor(.secondary)
                            .multilineTextAlignment(.center)
                            .lineLimit(3)
                    }
                    Button("重试") { model.reload() }
                        .buttonStyle(.borderedProminent)
                        .controlSize(.small)
                }
                .padding(20)
                .frame(maxWidth: .infinity, maxHeight: .infinity)
                #if os(macOS)
                .background(Color(nsColor: .textBackgroundColor))
                #else
                .background(Color(uiColor: .systemBackground))
                #endif
            }

            // 白名单拦截 toast（底部）
            if let notice = model.blockedNotice {
                Text(notice)
                    .font(.system(size: 11))
                    .foregroundColor(.white)
                    .padding(.horizontal, 12)
                    .padding(.vertical, 6)
                    .background(Color.black.opacity(0.75), in: Capsule())
                    .padding(.bottom, 12)
                    .frame(maxHeight: .infinity, alignment: .bottom)
            }
        }
    }
}
