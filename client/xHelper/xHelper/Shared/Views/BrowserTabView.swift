import SwiftUI
import WebKit

struct BrowserTabView: View {
    let webView: WKWebView
    @State private var urlText = ""
    @State private var isLoading = false
    @State private var progress: Double = 0
    @State private var currentURL: URL?
    @State private var canGoBack = false
    @State private var canGoForward = false
    @State private var isHovered = false
    @State private var observations: [NSKeyValueObservation] = []
    @FocusState private var urlFocused: Bool
    @FocusState private var findFocused: Bool
    @ObservedObject private var findCoordinator = BrowserFindCoordinator.shared
    @ObservedObject private var downloadStore = DownloadHistoryStore.shared

    var body: some View {
        VStack(spacing: 0) {
            toolbar
            if isLoading && progress > 0 && progress < 1 {
                ProgressView(value: progress)
                    .progressViewStyle(.linear)
                    .tint(Color("appAccent"))
                    .frame(height: 2)
                    .padding(.horizontal, 8)
            }
            Divider()
            if findCoordinator.isPresented {
                findBar
                Divider()
            }
            // G2：地址栏下方统一放"确认条（状态 C）+ 活动条（A/B）"，两者同区、位置一致
            BrowserAgentConfirmBar()
            BrowserAgentActivityBar()
            if downloadStore.records.contains(where: { $0.state == .downloading }) {
                downloadsBar
                Divider()
            }
            BrowserWebView(webView: webView)
        }
        .onAppear {
            // 重置浏览器字段焦点标志（防旧 tab 残留；FocusState 初始 false 不触发 onChange）
            BrowserFocus.isUrlFieldFocused = false
            BrowserFocus.isFindFieldFocused = false
            let initial = webView.url?.absoluteString ?? ""
            urlText = initial == "about:blank" ? "" : initial
            currentURL = webView.url
            isLoading = webView.isLoading
            canGoBack = webView.canGoBack
            canGoForward = webView.canGoForward
            startObserving()
            findCoordinator.attach(webView)
            // 新标签页自动聚焦地址栏
            if webView.url == nil || webView.url?.absoluteString == "about:blank" {
                DispatchQueue.main.async { urlFocused = true }
            }
        }
        .onChange(of: urlFocused) { _, focused in
            BrowserFocus.isUrlFieldFocused = focused
        }
        .onChange(of: findFocused) { _, focused in
            BrowserFocus.isFindFieldFocused = focused
        }
        .onChange(of: findCoordinator.isPresented) { _, presented in
            if presented { findFocused = true }
        }
        .onDisappear {
            BrowserFocus.isUrlFieldFocused = false
            BrowserFocus.isFindFieldFocused = false
        }
    }

    private var toolbar: some View {
        HStack(spacing: 6) {
            // 后退 / 前进
            Button {
                webView.goBack()
            } label: {
                Image(systemName: "chevron.left")
                    .font(.system(size: 12, weight: .semibold))
                    .foregroundColor(.secondary)
                    .frame(width: 22, height: 22)
            }
            .buttonStyle(.plain)
            .disabled(!canGoBack)
            .opacity(canGoBack ? 1 : 0.3)
            .help("后退")

            Button {
                webView.goForward()
            } label: {
                Image(systemName: "chevron.right")
                    .font(.system(size: 12, weight: .semibold))
                    .foregroundColor(.secondary)
                    .frame(width: 22, height: 22)
            }
            .buttonStyle(.plain)
            .disabled(!canGoForward)
            .opacity(canGoForward ? 1 : 0.3)
            .help("前进")

            securityIndicator

            TextField("输入网址或搜索", text: $urlText)
                .textFieldStyle(.plain)
                .font(.system(size: 12))
                .focused($urlFocused)
                .onSubmit { navigateToURL() }

            // 加载中显示停止，否则刷新
            Button {
                if isLoading {
                    webView.stopLoading()
                } else {
                    webView.reload()
                }
            } label: {
                Image(systemName: isLoading ? "xmark" : "arrow.clockwise")
                    .font(.system(size: 12, weight: .medium))
                    .foregroundColor(.secondary)
            }
            .buttonStyle(.plain)
            .help(isLoading ? "停止" : "重新加载")
        }
        .padding(.horizontal, 8)
        .padding(.vertical, 6)
        .background(.ultraThinMaterial)
    }

    private func navigateToURL() {
        let raw = urlText.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !raw.isEmpty else { return }
        // 非 URL → 默认搜索引擎兜底（含空格/无域名/无协议等）
        let looksLikeURL = raw.contains("://") || (raw.contains(".") && !raw.contains(" "))
        let target: URL
        if looksLikeURL {
            let s = raw.hasPrefix("http") ? raw : "https://\(raw)"
            guard let u = URL(string: s) else { return }
            target = u
        } else {
            let q = raw.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed) ?? ""
            guard let u = URL(string: "https://cn.bing.com/search?q=\(q)") else { return }
            target = u
        }
        webView.load(URLRequest(url: target))
    }

    /// 地址栏安全指示：https=锁 / http=警示 / 其他（空白页等）=globe
    @ViewBuilder
    private var securityIndicator: some View {
        switch currentURL?.scheme?.lowercased() {
        case "https":
            Image(systemName: "lock.fill")
                .font(.system(size: 10))
                .foregroundColor(.secondary)
                .help("HTTPS 加密连接")
        case "http":
            Image(systemName: "exclamationmark.triangle.fill")
                .font(.system(size: 11))
                .foregroundColor(.orange)
                .help("明文 HTTP，内容可能被篡改")
        default:
            Image(systemName: "globe")
                .font(.system(size: 12))
                .foregroundColor(.secondary)
        }
    }

    /// 进行中下载的紧凑进度行（每个带取消按钮）
    /// 进行中下载的紧凑进度行（速度/剩余/已用 + 取消按钮），每秒刷新
    private var downloadsBar: some View {
        TimelineView(.periodic(from: .now, by: 1)) { _ in
            VStack(spacing: 4) {
                ForEach(downloadStore.records.filter { $0.state == .downloading }) { item in
                    HStack(spacing: 8) {
                        Image(systemName: "arrow.down.circle")
                            .font(.system(size: 10, weight: .semibold))
                            .foregroundColor(.secondary)
                        VStack(alignment: .leading, spacing: 1) {
                            Text(item.filename)
                                .font(.system(size: 10))
                                .lineLimit(1)
                                .truncationMode(.middle)
                            Text(downloadStats(item))
                                .font(.system(size: 9))
                                .foregroundColor(.secondary)
                                .lineLimit(1)
                        }
                        Spacer(minLength: 8)
                        ProgressView(value: item.progress)
                            .frame(width: 60)
                        Button {
                            downloadStore.cancel(id: item.id)
                        } label: {
                            Image(systemName: "xmark")
                                .font(.system(size: 9, weight: .semibold))
                                .foregroundColor(.secondary)
                                .frame(width: 18, height: 18)
                                .contentShape(Rectangle())
                        }
                        .buttonStyle(.plain)
                        .help("取消下载")
                    }
                    .padding(.horizontal, 10)
                }
            }
            .padding(.vertical, 5)
            .background(.ultraThinMaterial)
        }
    }

    /// 下载统计行：速度 · 剩余 · 已用（totalBytes 未知时退化为百分比 · 已用）
    private func downloadStats(_ record: DownloadHistoryStore.DownloadRecord) -> String {
        var parts: [String] = []
        if let total = record.totalBytes, total > 0 {
            if record.speedBytesPerSec > 0 {
                let remaining = Double(max(total - record.completedBytes, 0)) / record.speedBytesPerSec
                parts.append("\(DownloadFormat.speedString(record.speedBytesPerSec)) · 剩余 \(DownloadFormat.durationString(remaining))")
            } else {
                parts.append("\(DownloadFormat.byteString(record.completedBytes)) / \(DownloadFormat.byteString(total))")
            }
        } else {
            parts.append("\(Int(record.progress * 100))%")
        }
        parts.append("已用 \(DownloadFormat.durationString(Date().timeIntervalSince(record.startedAt)))")
        return parts.joined(separator: " · ")
    }

    // MARK: - 页内查找（Cmd+F 唤出，Esc 关闭）

    private var findBar: some View {
        HStack(spacing: 6) {
            Image(systemName: "magnifyingglass")
                .font(.system(size: 11))
                .foregroundColor(.secondary)

            TextField("在页面中查找", text: $findCoordinator.query)
                .textFieldStyle(.plain)
                .font(.system(size: 12))
                .focused($findFocused)
                .onSubmit { findCoordinator.next() }
                .onChange(of: findCoordinator.query) { _, _ in findCoordinator.queryChanged() }

            if !findCoordinator.query.isEmpty {
                Image(systemName: findCoordinator.matchFound ? "checkmark" : "xmark")
                    .font(.system(size: 10, weight: .bold))
                    .foregroundColor(findCoordinator.matchFound ? .green : .red)
            }

            Button { findCoordinator.previous() } label: {
                Image(systemName: "chevron.up")
                    .font(.system(size: 10, weight: .semibold))
                    .foregroundColor(.secondary)
                    .frame(width: 20, height: 20)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .help("上一个匹配（Shift+⌘G）")

            Button { findCoordinator.next() } label: {
                Image(systemName: "chevron.down")
                    .font(.system(size: 10, weight: .semibold))
                    .foregroundColor(.secondary)
                    .frame(width: 20, height: 20)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .help("下一个匹配（⌘G）")

            Button { findCoordinator.close() } label: {
                Image(systemName: "xmark")
                    .font(.system(size: 10, weight: .semibold))
                    .foregroundColor(.secondary)
                    .frame(width: 20, height: 20)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .help("关闭查找（Esc）")
        }
        .padding(.horizontal, 8)
        .padding(.vertical, 5)
        .background(.ultraThinMaterial)
    }

    private func startObserving() {
        observations = [
            webView.observe(\.url, options: [.new]) { _, change in
                DispatchQueue.main.async {
                    guard let url = change.newValue as? URL?,
                          let u = url, !urlFocused else { return }
                    currentURL = u
                    // 新标签页默认页（about:blank）不显示在地址栏
                    urlText = u.absoluteString == "about:blank" ? "" : u.absoluteString
                }
            },
            webView.observe(\.isLoading, options: [.new]) { _, change in
                DispatchQueue.main.async {
                    if let v = change.newValue { isLoading = v }
                }
            },
            webView.observe(\.estimatedProgress, options: [.new]) { _, change in
                DispatchQueue.main.async {
                    if let v = change.newValue { progress = v }
                }
            },
            webView.observe(\.canGoBack, options: [.new]) { _, change in
                DispatchQueue.main.async {
                    if let v = change.newValue { canGoBack = v }
                }
            },
            webView.observe(\.canGoForward, options: [.new]) { _, change in
                DispatchQueue.main.async {
                    if let v = change.newValue { canGoForward = v }
                }
            },
        ]
    }
}

private struct BrowserWebView: NSViewRepresentable {
    let webView: WKWebView

    func makeNSView(context: Context) -> WKWebView {
        pipLog("webview attach: \(ObjectIdentifier(webView))")
        return webView
    }

    func updateNSView(_ nsView: WKWebView, context: Context) {
        if nsView !== webView {
            pipLog("webview UPDATE (stale!): attached=\(ObjectIdentifier(nsView)) want=\(ObjectIdentifier(webView))")
        }
    }
}

struct BrowserErrorPageView: View {
    let url: String
    let error: String
    let onRetry: () -> Void
    @State private var isHovered = false

    var body: some View {
        VStack(spacing: 16) {
            Spacer()

            Image(systemName: "exclamationmark.triangle")
                .font(.system(size: 32))
                .foregroundColor(.secondary.opacity(0.5))

            VStack(spacing: 4) {
                Text("无法打开网页")
                    .font(.system(size: 14, weight: .semibold))
                Text(url)
                    .font(.system(size: 12))
                    .foregroundColor(.secondary)
                    .lineLimit(2)
                Text(error)
                    .font(.system(size: 11))
                    .foregroundColor(.secondary)
            }

            Button("重试") { onRetry() }
                .buttonStyle(.borderedProminent)
                .controlSize(.small)

            Spacer()
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}
