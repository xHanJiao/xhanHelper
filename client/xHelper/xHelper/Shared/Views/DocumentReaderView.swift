import Combine
import Foundation
import SwiftUI

#if os(macOS)
import AppKit
import WebKit
import BeautifulMermaid

/// 文档正文阅读器状态：Swift 侧的把手，负责往 WebView 里灌 JS 指令、收回 JS 的回话。
/// 由 `DocumentFullView` 持有（`@StateObject`），生命周期跟文档详情页一致。
@MainActor
final class DocumentReaderController: ObservableObject {

    /// 查找命中总数
    @Published private(set) var matchCount = 0
    /// 当前命中下标（0-based；-1 = 无命中）
    @Published private(set) var matchIndex = -1

    /// 已装载内容的签名。变了才重载 —— 否则 `updateNSView` 每次都重新渲染
    /// markdown + mermaid（mermaid 是原生渲染，很贵）。
    fileprivate var loadedSignature = ""
    fileprivate weak var webView: WKWebView?

    // MARK: - Swift → JS

    /// 目录点击 → 滚到第 `occurrence` 个「级别 + 文字」都相同的标题。
    /// 传文字而不是下标：Swift 的 TOC 来自 markdown 源码，JS 的标题来自渲染后的 DOM，
    /// 传下标会在两边错位时静默跳错地方。
    func scrollToHeading(level: Int, text: String, occurrence: Int) {
        evaluate("reader.scrollToHeading(\(level), \(MarkdownWebUtils.jsString(text)), \(occurrence))")
    }

    func find(_ query: String) {
        evaluate("reader.find(\(MarkdownWebUtils.jsString(query)))")
    }

    func findNext() { evaluate("reader.findNext()") }

    func findPrev() { evaluate("reader.findPrev()") }

    func clearFind() { evaluate("reader.clearFind()") }

    /// 字号变化不重载 HTML（重载会丢滚动位置），改 CSS 变量。
    func setFontSize(_ px: CGFloat) {
        evaluate("reader.setFontSize(\(Int(px.rounded())))")
    }

    fileprivate func evaluate(_ js: String) {
        guard let webView else { return }
        webView.evaluateJavaScript(js) { _, error in
            if let error { print("[DocumentReader] JS 执行失败: \(error) ← \(js.prefix(160))") }
        }
    }

    fileprivate func apply(message: [String: Any]) {
        guard let type = message["type"] as? String else { return }
        switch type {
        case "findResult":
            matchCount = message["count"] as? Int ?? 0
            matchIndex = message["index"] as? Int ?? -1
        case "ready":
            // 重载前的滚动位置在这里补回去（内容被改写后重载，别把读者甩回顶部）
            if let y = pendingScrollY {
                pendingScrollY = nil
                evaluate("reader.restoreScrollY(\(Int(y)))")
            }
        default:
            print("[DocumentReader] 未知消息: \(type)")
        }
    }

    fileprivate var pendingScrollY: Double?
}

/// 文档正文（macOS）：WKWebView + 打包的 marked.min.js 渲染 markdown。
///
/// 为什么换 WebView：划线批注要在正文上做跨块选区、包 `<mark>`、按选区坐标放浮层，
/// 这些在原生的 MarkdownUI 树上拿不到。目录栏 / 返回 / 查找栏外壳仍在 SwiftUI 侧，
/// WebView 只吃正文，省掉大量坐标转换与桥接。
///
/// 三条硬约束：
/// - **全透明**：`isOpaque=false` + `backgroundColor=.clear` + `underPageBackgroundColor=.clear`，
///   且 HTML `body` 不铺底色。否则糊掉外层卡片的 material。
/// - **主题不从 Swift 推**：WKWebView 跟随窗口 appearance，`prefers-color-scheme`
///   在 CSS 里自动生效 —— 切主题不重载 HTML，不丢滚动位置，零桥接。
/// - **滚动归 WebView 自己**（文档流滚动），不外包给 SwiftUI ScrollView。
struct DocumentReaderView: View {
    let document: Document
    @ObservedObject var controller: DocumentReaderController
    let fontSize: CGFloat

    var body: some View {
        ReaderWebView(document: document, controller: controller, fontSize: fontSize)
    }
}

// MARK: - NSViewRepresentable

private struct ReaderWebView: NSViewRepresentable {
    let document: Document
    @ObservedObject var controller: DocumentReaderController
    let fontSize: CGFloat

    func makeCoordinator() -> Coordinator { Coordinator(controller: controller) }

    func makeNSView(context: Context) -> WKWebView {
        let config = WKWebViewConfiguration()
        config.userContentController.add(context.coordinator, name: "reader")

        let webView = WKWebView(frame: .zero, configuration: config)
        // 全透明（否则糊掉外层卡片的 material）。macOS 上 `isOpaque` / `backgroundColor`
        // 不存在（那是 iOS 的 API），得走 KVC 关掉 `drawsBackground`；再用公开的
        // `underPageBackgroundColor` 清掉页面底色。两条都要。
        webView.setValue(false, forKey: "drawsBackground")
        webView.underPageBackgroundColor = .clear
        webView.allowsBackForwardNavigationGestures = false

        controller.webView = webView
        context.coordinator.lastFontSize = fontSize
        context.coordinator.loadedSignature = signature
        webView.loadHTMLString(buildHTML(), baseURL: nil)
        return webView
    }

    func updateNSView(_ webView: WKWebView, context: Context) {
        if signature != context.coordinator.loadedSignature {
            let html = buildHTML()
            context.coordinator.loadedSignature = signature
            // 先问出滚动位置再换 HTML，ready 回话时补回去
            webView.evaluateJavaScript("window.scrollY") { value, _ in
                context.coordinator.controller.pendingScrollY = (value as? NSNumber)?.doubleValue
                webView.loadHTMLString(html, baseURL: nil)
            }
        }
        if fontSize != context.coordinator.lastFontSize {
            context.coordinator.lastFontSize = fontSize
            controller.setFontSize(fontSize)
        }
    }

    static func dismantleNSView(_ webView: WKWebView, coordinator: Coordinator) {
        webView.configuration.userContentController.removeScriptMessageHandler(forName: "reader")
        webView.stopLoading()
    }

    /// 内容签名：标题 / 标签 / 正文任一变了就重载。正文本身可能就是签名的一部分，
    /// 直接比字符串比哈希更省事（文档几 KB，比较成本可忽略）。
    private var signature: String {
        "\(document.title)\u{1}\(document.tags ?? "")\u{1}\(document.content)"
    }

    private func buildHTML() -> String { ReaderHTML.build(document: document, fontSize: fontSize) }

    final class Coordinator: NSObject, WKScriptMessageHandler {
        let controller: DocumentReaderController
        var loadedSignature = ""
        var lastFontSize: CGFloat?

        init(controller: DocumentReaderController) {
            self.controller = controller
        }

        func userContentController(_ controller: WKUserContentController, didReceive message: WKScriptMessage) {
            guard message.name == "reader", let body = message.body as? [String: Any] else { return }
            Task { @MainActor in self.controller.apply(message: body) }
        }
    }
}

// MARK: - HTML 组装

/// 屏幕阅读版 HTML：与 `DocumentPDFExporter` 同一套骨架（marked.min.js + 图表占位替换），
/// 但 CSS 换成阅读版 —— 不加 A4 的 `595px` / `12pt` 打印尺寸，改 728px 列宽 + 可调字号，
/// 并且不铺任何底色（外层是卡片 material）。
private enum ReaderHTML {

    static func build(document: Document, fontSize: CGFloat) -> String {
        guard let markedJS = MarkdownWebUtils.markedJS() else {
            return errorHTML("缺少内置渲染脚本 marked.min.js")
        }

        var mdParts: [String] = []
        var diagrams: [(light: String, dark: String)] = []

        // mermaid 仍走原生渲染（浅/深各出一张 PNG），不在 WebView 里引 Mermaid JS：
        // 一边是已跑通的链路，一边是几百 KB 的新依赖 + 运行时布局抖动。
        for segment in MermaidUtility.splitContent(document.content) {
            switch segment.type {
            case .markdown:
                mdParts.append(segment.content)
            case .mermaid:
                if let light = mermaidDataURI(segment.content, dark: false),
                   let dark = mermaidDataURI(segment.content, dark: true) {
                    diagrams.append((light, dark))
                    mdParts.append("\n\n\(placeholder(for: diagrams.count - 1))\n\n")
                } else {
                    mdParts.append("\n\n> 图表渲染失败。\n\n")
                }
            }
        }

        // 两张都烤进 HTML，靠媒体查询切显隐 —— 主题切换就只是 CSS 生效，不重载不丢滚动。
        // ponytail: 图多时是双倍渲染成本（真被咬到再改成按需渲染当前主题 + 重载恢复滚动）
        let imagesJSON = "[" + diagrams.map {
            "{\"light\":\(MarkdownWebUtils.jsString($0.light)),\"dark\":\(MarkdownWebUtils.jsString($0.dark))}"
        }.joined(separator: ",") + "]"

        let tags = parseTagTexts(document.tags)
        let tagsHTML = tags.map { "<span>\(MarkdownWebUtils.escapeHTML($0))</span>" }.joined()

        return template
            .replacingOccurrences(of: "__FS__", with: String(format: "%.2f", fontSize))
            .replacingOccurrences(of: "__MARKED_JS__", with: markedJS)
            .replacingOccurrences(of: "__TITLE_HTML__", with: MarkdownWebUtils.escapeHTML(document.title))
            .replacingOccurrences(of: "__TAGS_HTML__", with: tagsHTML)
            .replacingOccurrences(of: "__MD_JSON__", with: MarkdownWebUtils.jsString(mdParts.joined(separator: "\n\n")))
            .replacingOccurrences(of: "__IMAGES_JSON__", with: imagesJSON)
    }

    /// mermaid 占位 token（纯字母数字，规避 GFM 强调/链接误判），与 PDF 导出同款。
    private static func placeholder(for index: Int) -> String { "ZZMDIMG\(index)X" }

    private static func parseTagTexts(_ tags: String?) -> [String] {
        guard let tags, !tags.isEmpty else { return [] }
        return tags.split(separator: ",")
            .map { $0.trimmingCharacters(in: .whitespaces) }
            .filter { !$0.isEmpty }
    }

    /// 原生渲染 mermaid → PNG data URI。翻转修复同 `DocumentPDFExporter`（Mermaid 的
    /// 输出是 y 向下的，不翻会上传后倒过来）。
    private static func mermaidDataURI(_ code: String, dark: Bool) -> String? {
        let renderer = MermaidImageRenderer()
        renderer.theme = mermaidTheme(for: dark ? .dark : .light)
        renderer.scale = 2
        guard let png = try? renderer.renderPNG(from: code),
              let original = NSImage(data: png) else { return nil }

        let size = original.size
        let flipped = NSImage(size: size)
        flipped.lockFocus()
        let transform = NSAffineTransform()
        transform.translateX(by: 0, yBy: size.height)
        transform.scaleX(by: 1, yBy: -1)
        transform.concat()
        original.draw(at: .zero, from: .zero, operation: .copy, fraction: 1)
        flipped.unlockFocus()

        guard let tiff = flipped.tiffRepresentation,
              let rep = NSBitmapImageRep(data: tiff),
              let out = rep.representation(using: .png, properties: [:]) else { return nil }
        return "data:image/png;base64," + out.base64EncodedString()
    }

    private static func errorHTML(_ message: String) -> String {
        "<!DOCTYPE html><html><body style=\"background:transparent;font-family:-apple-system;color:#8e8e93;padding:40px\">\(MarkdownWebUtils.escapeHTML(message))</body></html>"
    }
}

// MARK: - 模板

private let template = #"""
<!DOCTYPE html>
<html lang="zh">
<head>
<meta charset="utf-8">
<style>
/* 设计 token 的唯一来源是 client/xHelper/xHelperMac/Assets.xcassets/*.colorset。
   这里只落 appAccent / assistantText 两个色相，其余（淡色、描边）一律
   color-mix 派生。带 var() 的派生变量必须在声明它的元素上求值 —— 所以
   token 与派生量都挂在 :root，深色覆盖也写在同一选择器上（同优先级靠源序生效）。 */
:root {
  --fs: __FS__px;
  --text: #3B2E1F;          /* assistantText light */
  --accent: #2F7D63;        /* appAccent     light */
  --accent-soft: color-mix(in srgb, var(--accent) 12%, transparent);
  --surface-2: color-mix(in srgb, var(--text) 7%, transparent);
  --muted: color-mix(in srgb, var(--text) 55%, transparent);
  --border: color-mix(in srgb, var(--text) 14%, transparent);
  --border-soft: color-mix(in srgb, var(--text) 7%, transparent);
}
@media (prefers-color-scheme: dark) {
  :root {
    --text: #FFFFFF;        /* assistantText dark */
    --accent: #92C8B4;      /* appAccent     dark */
  }
}
* { box-sizing: border-box; }
html, body {
  margin: 0; padding: 0;
  /* 硬约束：不铺底色，外层卡片的 material 要透上来 */
  background: transparent;
  font-family: -apple-system, "PingFang SC", "Helvetica Neue", "Microsoft YaHei", sans-serif;
  -webkit-font-smoothing: antialiased;
}
.page { max-width: 728px; margin: 0 auto; padding: 34px 40px 120px; }

.doc-title { font-size: calc(var(--fs) * 1.55); font-weight: 700; line-height: 1.32; color: var(--text); letter-spacing: -.2px; }
.doc-tags { margin-top: 10px; display: flex; gap: 6px; flex-wrap: wrap; }
.doc-tags:empty { display: none; }
.doc-tags span {
  font-size: calc(var(--fs) * 0.63); color: var(--accent); background: var(--accent-soft);
  padding: 2px 8px; border-radius: 20px; font-weight: 500;
}
.doc-rule { border: 0; border-top: 1px solid var(--border); margin: 18px 0 24px; }

#content { color: var(--text); font-size: var(--fs); line-height: 1.78; overflow-wrap: break-word; }
#content h1, #content h2, #content h3, #content h4, #content h5, #content h6 {
  color: var(--text); font-weight: 650; line-height: 1.35;
  margin: 30px 0 12px; letter-spacing: -.1px;
}
#content h2 { font-size: calc(var(--fs) * 1.28); padding-bottom: 6px; border-bottom: 1px solid var(--border-soft); }
#content h3 { font-size: calc(var(--fs) * 1.12); }
#content h4, #content h5, #content h6 { font-size: var(--fs); }
#content p { margin: 11px 0; }
#content a { color: var(--accent); text-decoration: none; border-bottom: 1px solid color-mix(in srgb, var(--accent) 35%, transparent); }
#content strong { font-weight: 650; }
#content ul, #content ol { padding-left: 24px; margin: 10px 0; }
#content li { margin: 5px 0; }
#content code {
  font-family: "SF Mono", Menlo, Consolas, monospace;
  font-size: .87em; background: var(--surface-2); padding: 1.5px 5px; border-radius: 4px;
  border: 1px solid var(--border-soft);
}
#content pre {
  background: var(--surface-2); border: 1px solid var(--border-soft); border-radius: 9px;
  padding: 12px 14px; overflow-x: auto; margin: 14px 0; line-height: 1.6;
}
#content pre code { background: none; padding: 0; border: 0; font-size: .86em; }
#content blockquote {
  margin: 14px 0; padding: 2px 0 2px 14px; border-left: 3px solid var(--border);
  color: var(--muted);
}
#content table { border-collapse: collapse; width: 100%; margin: 16px 0; font-size: .92em; }
#content th, #content td { border: 1px solid var(--border); padding: 7px 10px; text-align: left; vertical-align: top; }
#content th { background: var(--surface-2); font-weight: 600; }
#content tr:nth-child(even) td { background: var(--border-soft); }
#content hr { border: 0; border-top: 1px solid var(--border); margin: 24px 0; }
#content img { max-width: 100%; height: auto; }

/* mermaid：浅/深各一张，媒体查询切显隐（所以切主题不用重载） */
.mermaid { margin: 18px 0; text-align: center; }
.mermaid img { max-width: 100%; height: auto; }
.mermaid img.m-dark { display: none; }
@media (prefers-color-scheme: dark) {
  .mermaid img.m-light { display: none; }
  .mermaid img.m-dark { display: inline-block; }
}

/* 查找高亮：CSS Custom Highlight API —— 不改 DOM，所以不会破坏划线的 mark 结构 */
::highlight(find) { background-color: rgba(255, 196, 0, .55); }
::highlight(find-cur) { background-color: rgba(255, 140, 0, .72); }
@media (prefers-color-scheme: dark) {
  ::highlight(find) { background-color: rgba(255, 196, 0, .32); }
  ::highlight(find-cur) { background-color: rgba(255, 140, 0, .55); }
}

/* 选区交给引擎画，我们只改字形颜色 —— 这是踩过坑之后退回来的路。
   自绘（`::highlight(sel)` + selectionchange 监听）有两个修不掉的毛病：
   ① 容器 li（自己带文本、后面跟子列表）的文本永远画不上：探针显示 range 覆盖了它、
      `getClientRects()` 也给出了矩形，就是不着色（WebKit 的高亮绘制跳过这个节点），
      表现为跨段选区中间空出一行；
   ② JS 没有「选区此刻是否可见」这个查询：引擎把选区标成不可见（如失焦）而
      `getSelection()` 仍返回那个非折叠 range 时，自绘的色带就赖在那儿不消失。
   交回引擎，两个问题一起没有；`snapshot-reader.sh` 的两个 job 就是这两个钉子的显影液。

   代价：不能再铺底色 —— WebKit 对跨块选区会填一个横跨整个视图的大矩形（段间空隙、
   两侧留白全填，比正文列还宽），CSS 关不掉，把段落 inline 化更糟。所以只动 color，
   `background-color: transparent` 必须留着。

   color 必须写死、不能用 var()：实测 `::selection` 里 var() 解析不出来（写死 red 生效，
   写 var(--text) 无效；不写则引擎涂它自己的高亮前景色＝白，浅色下等于看不见）。
   改 token 时记得同步这里（对应上面 :root 的 --accent 两处）。 */
::selection { color: #2F7D63; background-color: transparent; }
@media (prefers-color-scheme: dark) {
  ::selection { color: #92C8B4; background-color: transparent; }
}
</style>
</head>
<body>
<div class="page">
  <div class="doc-title">__TITLE_HTML__</div>
  <div class="doc-tags">__TAGS_HTML__</div>
  <hr class="doc-rule">
  <div id="content"></div>
</div>
<script>
__MARKED_JS__
</script>
<script>
var reader = (function () {
  var content = document.getElementById('content');
  var findRanges = [];
  var findIdx = -1;

  function post(payload) {
    try { window.webkit.messageHandlers.reader.postMessage(payload); } catch (e) {}
  }

  /* 把 #content 拍平成一条字符串 + 文本节点的偏移索引。
     查找与划线都靠它做「字符串下标 → DOM Range」的换算。 */
  function flatten() {
    var walker = document.createTreeWalker(content, NodeFilter.SHOW_TEXT, null);
    var nodes = [];
    var text = '';
    var n;
    while ((n = walker.nextNode())) {
      nodes.push({ node: n, start: text.length });
      text += n.nodeValue;
    }
    return { nodes: nodes, text: text };
  }

  function rangeAt(flat, start, end) {
    var range = document.createRange();
    var gotStart = false, gotEnd = false;
    for (var i = 0; i < flat.nodes.length; i++) {
      var item = flat.nodes[i];
      var len = item.node.nodeValue.length;
      if (!gotStart && start >= item.start && start <= item.start + len) {
        range.setStart(item.node, start - item.start);
        gotStart = true;
      }
      if (!gotEnd && end >= item.start && end <= item.start + len) {
        range.setEnd(item.node, end - item.start);
        gotEnd = true;
      }
      if (gotStart && gotEnd) break;
    }
    return (gotStart && gotEnd) ? range : null;
  }

  function paint() {
    if (!window.CSS || !CSS.highlights) return;
    CSS.highlights.delete('find');
    CSS.highlights.delete('find-cur');
    if (!findRanges.length) return;
    CSS.highlights.set('find', new Highlight(...findRanges));
    if (findIdx >= 0) CSS.highlights.set('find-cur', new Highlight(findRanges[findIdx]));
  }

  /* 选区不着色：交给引擎（原因见 CSS 里 ::selection 那段），所以这里没有 selectionchange 监听 */

  function scrollToCurrent() {
    if (findIdx < 0 || findIdx >= findRanges.length) return;
    var rect = findRanges[findIdx].getBoundingClientRect();
    /* 顶部留一点余量，别让命中项贴着上边缘。Range 没有 scrollIntoView，只能自己算 */
    window.scrollTo({ top: Math.max(0, window.scrollY + rect.top - 120), behavior: 'smooth' });
  }

  function report() {
    post({ type: 'findResult', count: findRanges.length, index: findIdx });
  }

  function find(query) {
    findRanges = [];
    findIdx = -1;
    if (query) {
      var flat = flatten();
      var hay = flat.text.toLowerCase();
      var needle = query.toLowerCase();
      var at = hay.indexOf(needle);
      while (at !== -1 && findRanges.length < 2000) {
        var r = rangeAt(flat, at, at + needle.length);
        if (r) findRanges.push(r);
        at = hay.indexOf(needle, at + needle.length);
      }
    }
    if (findRanges.length) {
      findIdx = 0;
      paint();
      scrollToCurrent();
    } else {
      paint();
    }
    report();
  }

  function step(delta) {
    if (!findRanges.length) return;
    findIdx = (findIdx + delta + findRanges.length) % findRanges.length;
    paint();
    scrollToCurrent();
    report();
  }

  function clearFind() {
    findRanges = [];
    findIdx = -1;
    paint();
    report();
  }

  /* 目录点击：按「级别 + 文字」找，第 occurrence 个同名同级的。
     不传下标 —— Swift 的 TOC 来自 markdown 源码、标题来自 DOM，传下标会在两边错位时跳错地方。 */
  function scrollToHeading(level, text, occurrence) {
    var heads = content.querySelectorAll('h1, h2, h3, h4, h5, h6');
    var seen = 0;
    var fallback = null;
    for (var i = 0; i < heads.length; i++) {
      var h = heads[i];
      if (Number(h.tagName.charAt(1)) !== level) continue;
      if (h.textContent.trim() !== text) continue;
      if (fallback === null) fallback = h;
      if (seen === occurrence) { fallback = h; break; }
      seen++;
    }
    if (fallback) fallback.scrollIntoView({ block: 'start', behavior: 'smooth' });
  }

  function setFontSize(px) {
    document.documentElement.style.setProperty('--fs', px + 'px');
  }

  function restoreScrollY(y) {
    window.scrollTo({ top: y, behavior: 'auto' });
  }

  function render(md, images) {
    /* breaks:false —— 与旧的 MarkdownUI 阅读器（CommonMark）一致：单个换行按空格接续，
       不变成硬换行。PDF 导出用的是 breaks:true，那边是另一套排版，别跟着改。 */
    var html = marked.parse(md, { gfm: true, breaks: false });
    content.innerHTML = html.replace(/ZZMDIMG(\d+)X/g, function (match, idx) {
      var d = images[Number(idx)];
      if (!d) return '';
      return '<div class="mermaid"><img class="m-light" src="' + d.light +
             '"><img class="m-dark" src="' + d.dark + '"></div>';
    });
  }

  render(__MD_JSON__, __IMAGES_JSON__);
  post({ type: 'ready' });

  return {
    find: find,
    findNext: function () { step(1); },
    findPrev: function () { step(-1); },
    clearFind: clearFind,
    scrollToHeading: scrollToHeading,
    setFontSize: setFontSize,
    restoreScrollY: restoreScrollY
  };
})();
</script>
</body>
</html>
"""#
#endif
