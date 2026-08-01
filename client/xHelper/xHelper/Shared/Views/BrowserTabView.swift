import SwiftUI
import WebKit

struct BrowserTabView: View {
    let webView: WKWebView
    @State private var urlText = ""
    @State private var isHovered = false

    var body: some View {
        VStack(spacing: 0) {
            HStack(spacing: 6) {
                Image(systemName: "globe")
                    .font(.system(size: 12))
                    .foregroundColor(.secondary)

                TextField("输入网址", text: $urlText)
                    .textFieldStyle(.plain)
                    .font(.system(size: 12))
                    .onSubmit { navigateToURL() }

                Button { navigateToURL() } label: {
                    Image(systemName: "arrow.clockwise")
                        .font(.system(size: 12, weight: .medium))
                        .foregroundColor(.secondary)
                }
                .buttonStyle(.plain)
                .help("重新加载")
            }
            .padding(.horizontal, 8)
            .padding(.vertical, 6)
            .background(.ultraThinMaterial)

            Divider()

            BrowserWebView(webView: webView)
        }
        .onAppear { urlText = webView.url?.absoluteString ?? "" }
    }

    private func navigateToURL() {
        guard !urlText.isEmpty,
              let url = URL(string: urlText.hasPrefix("http") ? urlText : "https://\(urlText)") else { return }
        webView.load(URLRequest(url: url))
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
