import SwiftUI
import WebKit

struct BrowserTabView: View {
    let webView: WKWebView
    @State private var urlText = ""
    @State private var isLoading = false
    @State private var canGoBack = false
    @State private var canGoForward = false
    @State private var isHovered = false
    @State private var observations: [NSKeyValueObservation] = []
    @FocusState private var urlFocused: Bool

    var body: some View {
        VStack(spacing: 0) {
            toolbar
            Divider()
            // G2：地址栏下方统一放"确认条（状态 C）+ 活动条（A/B）"，两者同区、位置一致
            BrowserAgentConfirmBar()
            BrowserAgentActivityBar()
            BrowserWebView(webView: webView)
        }
        .onAppear {
            let initial = webView.url?.absoluteString ?? ""
            urlText = initial == "about:blank" ? "" : initial
            isLoading = webView.isLoading
            canGoBack = webView.canGoBack
            canGoForward = webView.canGoForward
            startObserving()
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

            Image(systemName: "globe")
                .font(.system(size: 12))
                .foregroundColor(.secondary)

            TextField("输入网址", text: $urlText)
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
        guard !urlText.isEmpty,
              let url = URL(string: urlText.hasPrefix("http") ? urlText : "https://\(urlText)") else { return }
        webView.load(URLRequest(url: url))
    }

    private func startObserving() {
        observations = [
            webView.observe(\.url, options: [.new]) { _, change in
                DispatchQueue.main.async {
                    guard let url = change.newValue as? URL?,
                          let u = url, !urlFocused else { return }
                    // 新标签页默认页（about:blank）不显示在地址栏
                    urlText = u.absoluteString == "about:blank" ? "" : u.absoluteString
                }
            },
            webView.observe(\.isLoading, options: [.new]) { _, change in
                DispatchQueue.main.async {
                    if let v = change.newValue { isLoading = v }
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

    func makeNSView(context: Context) -> WKWebView { webView }

    func updateNSView(_ nsView: WKWebView, context: Context) {}
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
