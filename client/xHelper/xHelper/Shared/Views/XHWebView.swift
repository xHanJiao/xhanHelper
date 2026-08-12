import SwiftUI
import Combine
import WebKit

// MARK: - 共享状态

/// XHWebView 的共享状态与控制器。
/// 由外部持有（如面板 header 的刷新按钮），驱动加载/错误状态展示。
@MainActor
final class XHWebViewModel: ObservableObject {
    @Published var isLoading = false
    @Published var loadError: String?
    @Published private(set) var currentURL: URL?

    private weak var webView: WKWebView?

    func attach(_ webView: WKWebView) {
        self.webView = webView
    }

    /// 外部刷新入口（面板 header 刷新按钮）
    func reload() {
        loadError = nil
        if let webView {
            if webView.url != nil {
                webView.reload()
            } else if let currentURL {
                webView.load(URLRequest(url: currentURL))
            }
        }
    }

    func setCurrentURL(_ url: URL) {
        currentURL = url
    }
}

// MARK: - 基础 WebView

/// 无浏览器 chrome 的基础 WebView。
/// 业务无关：只负责渲染 + 加载/错误状态 + 外链/新窗口拦截（抛给系统浏览器）。
/// 调用方通过 `configuration` 注入 user scripts 与 script message handlers（见 XHWebViewBridge）。
struct XHWebView: View {
    @ObservedObject var model: XHWebViewModel
    let url: URL
    let configuration: WKWebViewConfiguration

    var body: some View {
        XHWebViewRepresentable(model: model, url: url, configuration: configuration)
            .onAppear { model.setCurrentURL(url) }
    }
}

#if os(macOS)
struct XHWebViewRepresentable: NSViewRepresentable {
    @ObservedObject var model: XHWebViewModel
    let url: URL
    let configuration: WKWebViewConfiguration

    func makeCoordinator() -> Coordinator { Coordinator(model: model) }

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
        weak var webView: WKWebView?

        init(model: XHWebViewModel) {
            self.model = model
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

        /// 外链（新标签打开）→ 系统浏览器，不在面板内跳走
        func webView(_ webView: WKWebView, decidePolicyFor navigationAction: WKNavigationAction,
                     decisionHandler: @escaping (WKNavigationActionPolicy) -> Void) {
            if navigationAction.navigationType == .linkActivated,
               let url = navigationAction.request.url {
                NSWorkspace.shared.open(url)
                decisionHandler(.cancel)
                return
            }
            decisionHandler(.allow)
        }

        /// window.open() / 新窗口 → 系统浏览器
        func webView(_ webView: WKWebView, createWebViewWith configuration: WKWebViewConfiguration,
                     for navigationAction: WKNavigationAction, windowFeatures: WKWindowFeatures) -> WKWebView? {
            if let url = navigationAction.request.url {
                NSWorkspace.shared.open(url)
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

    func makeCoordinator() -> Coordinator { Coordinator(model: model) }

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
        weak var webView: WKWebView?

        init(model: XHWebViewModel) {
            self.model = model
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
            if navigationAction.navigationType == .linkActivated,
               let url = navigationAction.request.url {
                UIApplication.shared.open(url)
                decisionHandler(.cancel)
                return
            }
            decisionHandler(.allow)
        }

        func webView(_ webView: WKWebView, createWebViewWith configuration: WKWebViewConfiguration,
                     for navigationAction: WKNavigationAction, windowFeatures: WKWindowFeatures) -> WKWebView? {
            if let url = navigationAction.request.url {
                UIApplication.shared.open(url)
            }
            return nil
        }
    }
}
#endif

// MARK: - 组合面板（WebView + 加载/错误 overlay）

/// 通用无 chrome WebView 面板：XHWebView + 加载指示器 + 错误重试。
/// 业务无关，配合 XHPanel 使用。
struct XHWebViewPanel: View {
    @ObservedObject var model: XHWebViewModel
    let url: URL
    let configuration: WKWebViewConfiguration

    var body: some View {
        ZStack {
            XHWebView(model: model, url: url, configuration: configuration)

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
        }
    }
}
