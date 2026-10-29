import SwiftUI
import WebKit

/// 版式基线：**加载时注入**，不写进库。
/// 写进库就没法回溯——改一次版式要把所有历史页面重新生成；放在这里改一次对所有页生效。
/// 顺序在模型自己的 `<style>` 之前（模型总能覆盖），且不用 `!important`。
///
/// 关键约束（踩过）：不要把 `max-width` 写在 `body` 上。模型普遍会写 `body{margin:0}`，
/// 那会顶掉居中；而且 body 被限宽后模型的桌面底色只铺 760，侧栏一宽就露出白边。
/// 正文列交给顶层容器，`min(760px,100%)` 让写死 720 的页在窄侧栏里收缩而不是被裁掉。
private let baselineCSS = """
html,body{margin:0;padding:0;background:#fff}
html{min-height:100%}
body{box-sizing:border-box;min-height:100vh;padding:24px 20px;line-height:1.6;font-size:16px;
font-family:-apple-system,"PingFang SC",sans-serif;overflow-x:hidden;word-break:break-word;
-webkit-font-smoothing:antialiased}
body > *{max-width:min(760px,100%);margin-left:auto;margin-right:auto}
img{max-width:100%;height:auto}
table{max-width:100%;border-collapse:collapse}
pre,code{white-space:pre-wrap;word-break:break-word}
"""

/// 把基线插到 `<head>` 开头。服务端保证每页都有 head（`htmlsanitize.ensureHead`），
/// 兜底分支只是别让万一缺 head 的页面白屏。
private func injectingBaseline(_ html: String) -> String {
    let style = "<style>\(baselineCSS)</style>"
    guard let headStart = html.range(of: "<head", options: .caseInsensitive),
          let tagEnd = html[headStart.lowerBound...].firstIndex(of: ">") else {
        return style + html
    }
    var out = html
    out.insert(contentsOf: style, at: html.index(after: tagEnd))
    return out
}

/// 右栏里的 HTML 产物页。
///
/// 安全靠引擎而不是靠清洗：`javaScriptEnabled = false` + 取消一切导航。
/// 入库时已经清过危险标签并注入 CSP（docs/design/html-artifact §七），这里是第二道闸。
///
/// 内存约定：**不要**把这个视图塞进 `RightSidebarTab.content` 的构造参数、也不要进
/// `keptAliveTabIds`——切走即销毁，切回来重新 `loadHTMLString`（页面无 JS 无网络，重载可忽略）。
struct HTMLArtifactView: View {
    let id: String
    let title: String
    let html: String
    @State private var loadFailed = false
    @State private var isSharing = false

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
        // 换一篇/重新生成：失败态跟着重置，别把上一次的错误留在新页上
        .onChange(of: html) { _, _ in loadFailed = false }
        #if os(macOS)
        // 分享按钮浮在右上角：页面是整幅 WKWebView，tab 栏里塞不进额外控件
        .overlay(alignment: .topTrailing) { shareButton }
        #endif
    }

    /// 分享菜单（docs/design/html-artifact §9.6）：生成/复用链接 → 复制；也可以撤销。
    private var shareButton: some View {
        Menu {
            Button {
                Task { await copyShareLink() }
            } label: {
                Label("复制分享链接", systemImage: "link")
            }
            Button(role: .destructive) {
                Task {
                    if await ArtifactOpenCoordinator.shared.unshare(id: id) {
                        ToastCenter.shared.show("已撤销分享，旧链接已失效")
                    }
                }
            } label: {
                Label("撤销分享", systemImage: "link.badge.plus")
            }
        } label: {
            Image(systemName: isSharing ? "ellipsis" : "square.and.arrow.up")
                .font(.system(size: 13, weight: .medium))
                .foregroundColor(Color("assistantText").opacity(0.7))
                .padding(8)
                .background(.ultraThinMaterial, in: Circle())
        }
        .menuStyle(.borderlessButton)
        .menuIndicator(.hidden)
        .fixedSize()
        .disabled(isSharing)
        .padding(10)
        .help("分享这一页")
    }

    private func copyShareLink() async {
        guard !isSharing else { return }
        isSharing = true
        defer { isSharing = false }
        guard let url = await ArtifactOpenCoordinator.shared.share(id: id) else { return }
        copyToPasteboard(url)
        ToastCenter.shared.show("分享链接已复制：拿到链接的人无需登录即可查看")
    }
}

/// 剪贴板：macOS 用 NSPasteboard、iOS 用 UIPasteboard（两端共用这个视图）。
private func copyToPasteboard(_ text: String) {
    #if os(macOS)
    let pasteboard = NSPasteboard.general
    pasteboard.clearContents()
    pasteboard.setString(text, forType: .string)
    #else
    UIPasteboard.general.string = text
    #endif
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
        makeWebView(html: injectingBaseline(html), delegate: context.coordinator.navigationGuard)
    }

    func updateNSView(_ webView: WKWebView, context: Context) {
        // html 一变（重新生成/切到另一篇）就重载；内容相同不重复 load
        let page = injectingBaseline(html)
        guard context.coordinator.loadedHTML != page else { return }
        context.coordinator.loadedHTML = page
        webView.loadHTMLString(page, baseURL: nil)
    }

    // 和 makeNSView/makeUIView 加载的是同一串（注入后），否则首次 update 必判「变了」再重载一次
    func makeCoordinator() -> Coordinator { Coordinator(loadedHTML: injectingBaseline(html), onFail: onFail) }

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
        makeWebView(html: injectingBaseline(html), delegate: context.coordinator.navigationGuard)
    }

    func updateUIView(_ webView: WKWebView, context: Context) {
        let page = injectingBaseline(html)
        guard context.coordinator.loadedHTML != page else { return }
        context.coordinator.loadedHTML = page
        webView.loadHTMLString(page, baseURL: nil)
    }

    // 和 makeNSView/makeUIView 加载的是同一串（注入后），否则首次 update 必判「变了」再重载一次
    func makeCoordinator() -> Coordinator { Coordinator(loadedHTML: injectingBaseline(html), onFail: onFail) }

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
    HTMLArtifactView(id: "preview", title: "预览", html: "<html><body><h1>预览</h1><p>内容</p></body></html>")
        .frame(width: 700, height: 500)
}
