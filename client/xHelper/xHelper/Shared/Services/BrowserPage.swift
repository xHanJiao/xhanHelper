import Foundation
import WebKit

#if os(macOS)
import AppKit
typealias XHPageView = NSView
#else
import UIKit
typealias XHPageView = UIView
#endif

/// 右侧栏浏览器面板依赖的**最小**内核面。
///
/// 刻意只放 UI 真正用到的能力（地址栏 / 前进后退 / 加载态 / 承载视图）。JS 求值、下载、
/// 页内查找、快照这些 WK 语义很重的能力走 `wkWebView` 过渡口：CEF 标签那里拿到 nil，
/// 调用方必须给出明确失败（不许当成空白页静默继续）。CEF 侧的工具接入在 B6/B7。
@MainActor
protocol BrowserPage: AnyObject {
    var engine: BrowserEngine { get }
    var url: URL? { get }
    var title: String? { get }
    var isLoading: Bool { get }
    var canGoBack: Bool { get }
    var canGoForward: Bool { get }

    /// 承载视图（SwiftUI 用 Representable 挂它）。WK 就是 webView 本身，CEF 是 CEF 挂子视图的容器。
    var hostingView: XHPageView { get }

    /// 过渡口：仅 `compat` 有值。见类型注释。
    var wkWebView: WKWebView? { get }

    /// 任一可视状态变化（url / title / 加载中 / 前进后退）时广播，宿主（面板 / TabSession）各自订阅。
    var stateObservers: BrowserPageState { get }
    /// 主框架加载结束（ok=false 为失败）。CEF 侧没有 delegate，用它对齐 agent open 的等待。
    var onLoadFinish: ((Bool) -> Void)? { get set }

    func load(_ url: URL)
    func loadHTMLString(_ html: String, baseURL: URL?)
    func reload()
    func stopLoading()
    func goBack()
    func goForward()
    /// 回收内核资源（WK 停加载；CEF 走 `CloseBrowser(force_close)`）
    func close()

    /// 页面缩放（1.0 = 100%）。WK 走 `pageZoom`；CEF 走 `SetZoomLevel`（CEF 自己按 origin 记）。
    var zoomScale: Double { get }
    func setZoomScale(_ scale: Double)

    /// 页内查找。WK 同步、CEF 异步；命中数通过 `onFindResult` 回报（-1 = 未知）。
    func find(_ text: String, forward: Bool, matchCase: Bool)
    func stopFinding(clearSelection: Bool)
    var onFindResult: ((Int) -> Void)? { get set }

    /// 执行 JS 并取结果（agent 工具的快照/读正文/点击都靠它）。
    /// 返回 JSON 可序列化的对象；`undefined`/`null` 给 `NSNull`。CEF 侧经渲染进程回包。
    func evaluateJS(_ script: String) async throws -> Any

    /// 本页视频"播不了"的迹象（`no-h264` / `media-src-unsupported`），nil = 正常。
    /// 只有主引擎（CEF）会报：标准构建不含 H.264，非白名单站点会静默黑屏。
    var mediaIssueReason: String? { get }
}

/// 页面状态的一对多广播（面板要刷新地址栏，TabSession 要更新标题/host，两边都得收）。
@MainActor
final class BrowserPageState {
    private var handlers: [UUID: () -> Void] = [:]

    @discardableResult
    func add(_ handler: @escaping () -> Void) -> UUID {
        let id = UUID()
        handlers[id] = handler
        return id
    }

    func remove(_ id: UUID) { handlers.removeValue(forKey: id) }

    func fire() { for handler in handlers.values { handler() } }
}

// MARK: - 兼容引擎（WKWebView）

@MainActor
final class WKPage: NSObject, BrowserPage {
    let engine: BrowserEngine = .compat
    let webView: WKWebView

    var onLoadFinish: ((Bool) -> Void)?
    let stateObservers = BrowserPageState()
    private var observations: [NSKeyValueObservation] = []

    init(webView: WKWebView) {
        self.webView = webView
        super.init()
        observe(\.url)
        observe(\.title)
        observe(\.isLoading)
        observe(\.canGoBack)
        observe(\.canGoForward)
        observe(\.estimatedProgress)
    }

    private func observe<V>(_ keyPath: KeyPath<WKWebView, V>) {
        observations.append(webView.observe(keyPath, options: [.new]) { [weak self] _, _ in
            // KVO 回调线程不保证主线程
            Task { @MainActor in self?.stateObservers.fire() }
        })
    }

    var hostingView: XHPageView { webView }
    var wkWebView: WKWebView? { webView }
    var url: URL? { webView.url }
    var title: String? { webView.title }
    var isLoading: Bool { webView.isLoading }
    var canGoBack: Bool { webView.canGoBack }
    var canGoForward: Bool { webView.canGoForward }

    func load(_ url: URL) { webView.load(URLRequest(url: url, timeoutInterval: 15)) }
    func loadHTMLString(_ html: String, baseURL: URL?) { webView.loadHTMLString(html, baseURL: baseURL) }
    func reload() { webView.reload() }
    func stopLoading() { webView.stopLoading() }
    func goBack() { webView.goBack() }
    func goForward() { webView.goForward() }
    func close() { webView.stopLoading() }

    var zoomScale: Double { webView.pageZoom }
    func setZoomScale(_ scale: Double) { webView.pageZoom = scale }

    var onFindResult: ((Int) -> Void)?

    func find(_ text: String, forward: Bool, matchCase: Bool) {
        guard !text.isEmpty else {
            onFindResult?(0)
            return
        }
        let config = WKFindConfiguration()
        config.backwards = !forward
        config.caseSensitive = matchCase
        config.wraps = true
        webView.find(text, configuration: config) { [weak self] result in
            // WKFindResult 只有"命中与否"，没有计数 → 报 1/0
            self?.onFindResult?(result.matchFound ? 1 : 0)
        }
    }

    /// WK 没有 stopFinding，清掉选区即可（与 Safari 退出查找栏的观感一致）
    func stopFinding(clearSelection: Bool) {
        guard clearSelection else { return }
        webView.evaluateJavaScript("window.getSelection().removeAllRanges()", completionHandler: nil)
    }

    func evaluateJS(_ script: String) async throws -> Any {
        try await webView.evaluateJavaScript(script)
    }

    /// 兼容引擎自带完整解码器，不存在"静默播不了"
    var mediaIssueReason: String? { nil }

    deinit {
        observations.forEach { $0.invalidate() }
    }
}

// MARK: - 主引擎（CEF）

#if os(macOS)
@MainActor
final class CefPage: NSObject, BrowserPage {
    let engine: BrowserEngine = .cef
    private let browserView: XHCefBrowserView
    private var currentURL: URL?
    private var currentTitle: String?

    var onLoadFinish: ((Bool) -> Void)?
    /// `target=_blank` / `window.open` 的目标：CEF 的原生弹窗已拦掉，由宿主开新 tab
    var onPopupRequested: ((URL) -> Void)?
    /// JS 对话框：kind 0=alert / 1=confirm / 2=prompt。reply **必须调一次**（否则页面一直等）。
    var onJSDialog: ((_ kind: Int, _ message: String, _ defaultText: String,
                     _ reply: @escaping (Bool, String?) -> Void) -> Void)?
    /// 用户点击下载：返回落盘路径（nil = 取消）。**必须同步返回**。
    var onDownloadPathRequested: ((_ suggestedName: String, _ url: String) -> String?)?
    /// 下载状态：state 0=下载中 / 1=完成 / 2=已取消
    var onDownloadUpdated: ((_ downloadId: Int64, _ state: Int, _ received: Int64, _ total: Int64,
                             _ fullPath: String, _ url: String) -> Void)?
    /// 右键「用百度搜索选中文字」
    var onContextSearch: ((String) -> Void)?
    /// 媒体检测第 2/3 层 →
    private(set) var mediaIssueReason: String?
    let stateObservers = BrowserPageState()

    /// `url == nil` 表示从空白页起（新标签页随后用 loadHTMLString 填）。
    init(url: URL?) {
        currentURL = url
        browserView = XHCefBrowserView(frame: .zero, urlString: url?.absoluteString ?? "about:blank")
        super.init()
        // 这些 block 由 ObjC 侧 RunOnMain 保证在主线程回调
        browserView.titleDidChange = { [weak self] title in
            MainActor.assumeIsolated {
                self?.currentTitle = title
                self?.stateObservers.fire()
            }
        }
        browserView.urlDidChange = { [weak self] urlString in
            MainActor.assumeIsolated {
                self?.currentURL = URL(string: urlString)
                self?.stateObservers.fire()
            }
        }
        browserView.stateDidChange = { [weak self] in
            MainActor.assumeIsolated { self?.stateObservers.fire() }
        }
        browserView.loadDidFinish = { [weak self] ok in
            MainActor.assumeIsolated { self?.onLoadFinish?(ok) }
        }
        browserView.popupRequested = { [weak self] urlString in
            MainActor.assumeIsolated {
                guard let url = URL(string: urlString) else { return }
                self?.onPopupRequested?(url)
            }
        }
        browserView.findResultCount = { [weak self] count in
            MainActor.assumeIsolated { self?.onFindResult?(Int(count)) }
        }
        browserView.jsDialogRequested = { [weak self] kind, message, defaultText, reply in
            MainActor.assumeIsolated {
                guard let self, let handler = self.onJSDialog else {
                    reply(false, nil)   // 没接线：按"取消"放行，绝不能让页面干等
                    return
                }
                handler(Int(kind), message, defaultText) { ok, text in reply(ok, text) }
            }
        }
        browserView.downloadPathRequested = { [weak self] suggestedName, urlString in
            MainActor.assumeIsolated {
                self?.onDownloadPathRequested?(suggestedName, urlString)
            }
        }
        browserView.downloadUpdated = { [weak self] downloadId, state, received, total, fullPath, urlString in
            MainActor.assumeIsolated {
                self?.onDownloadUpdated?(downloadId, Int(state), received, total, fullPath, urlString)
            }
        }
        browserView.contextSearchRequested = { [weak self] text in
            MainActor.assumeIsolated { self?.onContextSearch?(text) }
        }
        browserView.mediaIssueReported = { [weak self] reason in
            MainActor.assumeIsolated {
                guard let self, self.mediaIssueReason != reason else { return }
                self.mediaIssueReason = reason
                self.stateObservers.fire()
            }
        }
    }

    var hostingView: XHPageView { browserView }
    var wkWebView: WKWebView? { nil }
    var url: URL? { currentURL }
    var title: String? { currentTitle }
    var isLoading: Bool { browserView.isLoading }
    var canGoBack: Bool { browserView.canGoBack }
    var canGoForward: Bool { browserView.canGoForward }

    func load(_ url: URL) {
        currentURL = url
        browserView.loadURLString(url.absoluteString)
    }
    func loadHTMLString(_ html: String, baseURL: URL?) { browserView.loadHTMLString(html) }
    func reload() { browserView.reload() }
    func stopLoading() { browserView.stopLoading() }
    func goBack() { browserView.goBack() }
    func goForward() { browserView.goForward() }
    func close() { browserView.closeBrowser() }

    var zoomScale: Double { browserView.zoomScale }
    func setZoomScale(_ scale: Double) { browserView.setZoomScale(scale) }

    var onFindResult: ((Int) -> Void)?

    func find(_ text: String, forward: Bool, matchCase: Bool) {
        // ObjC 的 findString:forward:matchCase: 在 Swift 侧被自动改名为 find(_:forward:matchCase:)
        browserView.find(text, forward: forward, matchCase: matchCase)
    }

    func stopFinding(clearSelection: Bool) {
        browserView.stopFinding(clearSelection)
    }

    /// 取消进行中的下载（store 的「取消」按钮 → 这里 → CEF）
    func cancelDownload(_ downloadId: Int64) {
        browserView.cancelDownload(downloadId)
    }

    func evaluateJS(_ script: String) async throws -> Any {
        try await withCheckedThrowingContinuation { continuation in
            browserView.evaluateJavaScript(script) { value, error in
                if let error {
                    continuation.resume(throwing: CefPageError.jsFailed(error))
                } else {
                    continuation.resume(returning: value ?? NSNull())
                }
            }
        }
    }
}

private enum CefPageError: LocalizedError {
    case jsFailed(String)

    var errorDescription: String? {
        switch self {
        case .jsFailed(let message): return message
        }
    }
}
#endif
