import Foundation
import BeautifulMermaid
#if os(macOS)
import AppKit
import WebKit

/// 文档 → 排版 PDF 导出器（macOS 专属）。
///
/// 管线：mermaid 段渲染 PNG（浅色主题、1x、纵向翻转）→ 打包的 marked.min.js 将 markdown 转 HTML
/// （图表占位符在 JS 端替换为 `<img>`）→ 离屏 WKWebView（A4 宽度）loadHTMLString →
/// `printOperation(with:)` 交给 WebKit **打印引擎**按行盒分页 → 落临时文件读回 Data。
/// 全程离线，不依赖外网。
///
/// **不要退回 `createPDF` + 固定高度切片**：切缝只看几何、不知道文字行在哪，落在缝上的行会被横切
/// 两半，末页还会因没裁剪而重复上一段（详见 `docs/bugs/039`）。
@MainActor
enum DocumentPDFExporter {

    /// 纸张尺寸（pt）：离屏窗口/webView 的尺寸，也是打印纸张尺寸（与模板里的 `@page { size }` 一致）。
    static let pageSize = CGSize(width: 595, height: 842)

    /// 导出 document 到目标 URL（由调用方 NSSavePanel 指定）。抛错由调用方记录。
    /// baseURL：文档配图相对路径 `/images/...` 的解析根（服务端地址）。
    static func export(_ document: Document, to url: URL, baseURL: URL? = nil) async throws {
        print("[PDFExport] export start: \(document.title)")
        do {
            let html = try Self.buildHTML(title: document.title, tags: document.tags ?? "", markdown: document.content)
            print("[PDFExport] html built, \(html.count) chars")
            let data = try await Self.renderPDF(html: html, baseURL: baseURL)
            print("[PDFExport] pdf generated, \(data.count) bytes")
            try data.write(to: url, options: .atomic)
            print("[PDFExport] written to \(url.path)")
        } catch {
            print("[PDFExport] FAILED: \(error)")
            throw error
        }
    }

    // MARK: - 私有

    /// 导出任意单文件 HTML（灵阅产物）到 PDF：与文档导出共用 `renderPDF` 的打印分页。
    /// `html` 需已含版式基线（调用方传 `injectingBaseline(...)`）；产物自包含，`baseURL` 传 nil。
    static func exportHTML(_ html: String, to url: URL, baseURL: URL? = nil) async throws {
        let data = try await Self.renderPDF(html: html, baseURL: baseURL)
        try data.write(to: url, options: .atomic)
    }

    /// 组装导出 HTML：mermaid 段渲染为 data URI，markdown 段拼接后交给 marked 运行时转 HTML。
    private static func buildHTML(title: String, tags: String, markdown: String) throws -> String {
        guard let markedJS = MarkdownWebUtils.markedJS() else {
            throw DocumentExportError.markedResourceNotFound
        }
        var mdParts: [String] = []
        var diagramURIs: [String] = []

        for segment in MermaidUtility.splitContent(markdown) {
            switch segment.type {
            case .markdown:
                mdParts.append(segment.content)
            case .mermaid:
                if let uri = mermaidDataURI(code: segment.content) {
                    diagramURIs.append(uri)
                    mdParts.append("\n\n\(MD(for: diagramURIs.count - 1))\n\n")
                } else {
                    mdParts.append("\n\n> 图表渲染失败。\n\n")
                }
            }
        }

        let titleHTML = MarkdownWebUtils.escapeHTML(title)
        let tagsHTML = MarkdownWebUtils.escapeHTML(tags)
        let mdLiteral = MarkdownWebUtils.jsString(mdParts.joined(separator: "\n\n"))
        let imagesLiteral = "[" + diagramURIs.map(MarkdownWebUtils.jsString).joined(separator: ",") + "]"

        return template
            .replacingOccurrences(of: "__MARKED_JS__", with: markedJS)
            .replacingOccurrences(of: "__PAGE_W__", with: "\(Int(pageSize.width))")
            .replacingOccurrences(of: "__PAGE_H__", with: "\(Int(pageSize.height))")
            .replacingOccurrences(of: "__TITLE_HTML__", with: titleHTML)
            .replacingOccurrences(of: "__TAGS_HTML__", with: tagsHTML)
            .replacingOccurrences(of: "__MD_JSON__", with: mdLiteral)
            .replacingOccurrences(of: "__IMAGES_JSON__", with: imagesLiteral)
    }

    /// mermaid 占位 token（纯字母数字，规避 GFM 强调/链接误判）
    private static func MD(for index: Int) -> String { "ZZMDIMG\(index)X" }

    /// 渲染 mermaid 为翻转后用 PNG base64 data URI（同 FullScreenMermaidView.saveImage 的 flip 处理）
    private static func mermaidDataURI(code: String) -> String? {
        let renderer = MermaidImageRenderer()
        renderer.theme = lightTheme
        renderer.scale = 1
        guard let pngData = try? renderer.renderPNG(from: code) else { return nil }
        guard let original = NSImage(data: pngData) else { return nil }

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
              let png = rep.representation(using: .png, properties: [:]) else { return nil }
        return "data:image/png;base64," + png.base64EncodedString()
    }

    /// 离屏 WKWebView 渲染 → WebKit 打印引擎分页 → 读回 PDF Data。
    /// 注意：macOS 上打印/快照都要求 webView 在视图层级中（挂到隐藏 window）。
    private static func renderPDF(html: String, baseURL: URL?) async throws -> Data {
        let loadWaiter = PDFLoadWaiter()
        let printWaiter = PDFPrintWaiter()

        let window = NSWindow(
            contentRect: CGRect(origin: .zero, size: pageSize),
            styleMask: [.borderless],
            backing: .buffered,
            defer: false
        )
        window.isReleasedWhenClosed = false
        window.alphaValue = 0
        // 放屏幕外并 order in，确保 WebKit 真正参与渲染（隐藏 window 也要在窗口列表里）
        window.setFrameOrigin(NSPoint(x: -pageSize.width - 100, y: -pageSize.height - 100))
        window.orderFront(nil)
        defer { window.close() }

        // 与文档阅读器**不同**：导出用非持久 store —— 一次性动作、不吃磁盘缓存，也保证「导出看的是
        // 当下的真实网络结果」（见 docs/bugs/031）。代价：阅读器靠磁盘缓存能显示的图，离线或远程不可达
        // 时导出抓不到 → 裂图（符合预期，要跟阅读器表现一致就删掉这一行）。
        let pdfConfig = WKWebViewConfiguration()
        pdfConfig.websiteDataStore = .nonPersistent()
        // 打印默认**不印背景色**是 Safari 的老毛病（代码块/表头灰底会变白）。本机 WebKit 实测：
        // 打印管线这里默认就印（翻转这个开关、或去掉模板里那条 -webkit-print-color-adjust，
        // 产出 PDF 字节完全相同），所以它是**给将来 WebKit 改默认留的保险**，不是当前生效的那道。
        pdfConfig.preferences.shouldPrintBackgrounds = true
        let webView = WKWebView(frame: CGRect(origin: .zero, size: pageSize), configuration: pdfConfig)
        window.contentView = webView
        webView.navigationDelegate = loadWaiter
        webView.loadHTMLString(html, baseURL: baseURL)
        print("[PDFExport] webview attached to hidden window, waiting for didFinish")
        await loadWaiter.waitForLoad()
        // 留给 marked 注入 innerHTML 后的排版/图片解码时间
        try? await Task.sleep(nanoseconds: 350_000_000)
        print("[PDFExport] didFinish/wait done, printing (html=\(html.count) chars)")

        // 纸张 / 零边距（页边距由模板里 .page 的 padding 提供，别两边都留）
        let printInfo = NSPrintInfo.shared.copy() as! NSPrintInfo
        printInfo.paperSize = pageSize
        printInfo.topMargin = 0
        printInfo.bottomMargin = 0
        printInfo.leftMargin = 0
        printInfo.rightMargin = 0
        // 存盘到临时文件读回——WebKit 打印接口**不返回 Data**，只能落一次盘。
        // jobDisposition 与 jobSavingURL 必须成对给齐，少哪个都会自己弹 NSSavePanel/PDF 面板。
        printInfo.jobDisposition = .save
        let tmpURL = FileManager.default.temporaryDirectory
            .appendingPathComponent("xhan-doc-export-\(UUID().uuidString).pdf")
        printInfo.dictionary()[NSPrintInfo.AttributeKey.jobSavingURL] = tmpURL
        defer {
            do { try FileManager.default.removeItem(at: tmpURL) }
            catch { print("[PDFExport] 临时文件清理失败 \(tmpURL.lastPathComponent): \(error)") }
        }

        let op = webView.printOperation(with: printInfo)
        print("[PDFExport] printOperation run, paper=\(printInfo.paperSize)")
        // 用 runModal 而不是 run()：run() 在离屏窗口里实测**挂住不返回**（45s 没动过），runModal 是
        // 异步的、完成回调里再读盘（不设超时的理由见 PDFPrintWaiter）。
        guard await printWaiter.run(op, in: window) else {
            print("[PDFExport] printOperation failed")
            throw DocumentExportError.printFailed
        }

        let data: Data
        do {
            data = try Data(contentsOf: tmpURL)
        } catch {
            print("[PDFExport] 读回临时 PDF 失败 \(tmpURL.lastPathComponent): \(error)")
            throw error
        }
        guard !data.isEmpty else {
            print("[PDFExport] empty PDF data, throwing emptyPDF")
            throw DocumentExportError.emptyPDF
        }
        print("[PDFExport] pdf ready, \(data.count) bytes")
        return data
    }

    /// 浅色打印主题（与 MermaidDiagramInlineView 浅色分支一致）
    private static let lightTheme = DiagramTheme(
        background: BMColor(hex: "#F2F7F3"),
        foreground: BMColor(hex: "#3B2E1F"),
        line: BMColor(hex: "#C4B99A"),
        accent: BMColor(hex: "#7DB5A0"),
        muted: BMColor(hex: "#E0D8C0"),
        surface: BMColor(hex: "#FFFFFF"),
        border: BMColor(hex: "#D0C8B0")
    )
}

enum DocumentExportError: LocalizedError {
    case markedResourceNotFound
    case emptyPDF
    case printFailed

    var errorDescription: String? {
        switch self {
        case .markedResourceNotFound: return "缺少内置渲染脚本 marked.min.js"
        case .emptyPDF: return "PDF 生成失败：WebKit 未返回数据"
        case .printFailed: return "PDF 生成失败：打印管线未能落盘"
        }
    }
}

/// 静默打印落盘的等待器。`runModal(for:delegate:didRun:contextInfo:)` 的完成回调走 ObjC selector，
/// 所以要个 NSObject 接住，再转成 async。
///
/// 为什么不用 `op.run()`：那是应用模态、同步阻塞，在离屏窗口里实测**一直不返回**（不弹任何 UI 也
/// 卡死）——真机症状会是导出永远转圈。
///
/// **为什么不设超时**（原本照搬 createPDF 那套 120s）：`NSPrintOperation` 没有公开的取消 API
/// （`cleanUpOperation` / `destroyContext` 的注释明写「由打印操作自己调用，你不要调」）。超时后硬
/// 抛错会留下「会话还开着 + delegate 已释放（这族 API 的 delegate 是 assign 语义）+ 临时文件被删」
/// 的烂摊子，比它要防的挂起更糟。打印跑在本进程 runloop 上、正常必然回调；万一真不回调，日志里
/// 「printOperation run」之后没有「pdf ready」就是证据。
/// （waiters 是 `renderPDF` 的局部量，await 期间一直活着，所以 delegate 必然活到回调那一刻。）
private final class PDFPrintWaiter: NSObject {
    private var continuation: CheckedContinuation<Bool, Never>?

    func run(_ op: NSPrintOperation, in window: NSWindow) async -> Bool {
        // 这两句缺一就弹系统打印对话框 / 闪一下进度窗（默认都是 true）
        op.showsPrintPanel = false
        op.showsProgressPanel = false
        return await withCheckedContinuation { cont in
            continuation = cont
            op.runModal(for: window, delegate: self,
                        didRun: #selector(printDone(_:success:contextInfo:)), contextInfo: nil)
        }
    }

    @objc private func printDone(_ op: NSPrintOperation, success: Bool, contextInfo: UnsafeMutableRawPointer?) {
        let cont = continuation
        continuation = nil
        cont?.resume(returning: success)
    }
}

/// 离屏加载完成等待器（resume 后重用，等待 8s 超时兜底防挂起）。
private final class PDFLoadWaiter: NSObject, WKNavigationDelegate {
    private var continuation: CheckedContinuation<Void, Never>?
    private var didFinish = false

    func webView(_ webView: WKWebView, didFinish navigation: WKNavigation?) {
        didFinish = true
        continuation?.resume()
        continuation = nil
    }

    func webView(_ webView: WKWebView, didFail navigation: WKNavigation?, withError error: Error) {
        // 加载失败也放行（打印阶段再兜底报错）
        didFinish = true
        resume()
    }

    func webView(_ webView: WKWebView, didFailProvisionalNavigation navigation: WKNavigation?, withError error: Error) {
        didFinish = true
        resume()
    }

    func waitForLoad() async {
        if didFinish { return }
        await withCheckedContinuation { cont in
            continuation = cont
            // 8s 超时兜底：didFinish/didFail 未触发也不挂起
            DispatchQueue.main.asyncAfter(deadline: .now() + 8) { [weak self] in
                guard let self else { return }
                if self.continuation != nil {
                    self.continuation = nil
                    cont.resume()
                }
            }
        }
    }

    private func resume() {
        continuation?.resume()
        continuation = nil
    }
}

#if os(macOS)
/// 导出 HTML 模板：A4 浅色打印风。
private let template = """
<!DOCTYPE html>
<html lang="zh">
<head>
<meta charset="utf-8">
<title>文档</title>
<style>
    /* 纸张与 DocumentPDFExporter.pageSize 一致（值由 buildHTML 注入，别在两处各写一个）。
       **页边距必须给 @page，不能给 .page 的 padding**：CSS 分片语义下，跨页块的 padding-top 只落在
       第一个分片、padding-bottom 只落在最后一个分片 —— 实测「padding 版」第 2 页起上边距只剩 3pt
       （第 1 页 47pt），且末尾 44pt 的 padding-bottom 装不进最后一页时会**多顶出一张空白页**。
       左边距是块盒横向属性、每页都在，所以只有上下会出问题。 */
    @page { size: __PAGE_W__pt __PAGE_H__pt; margin: 44pt 52pt; }
    :root { color-scheme: light; }
    * { box-sizing: border-box; }
    /* 长度一律用 pt：打印引擎里 1px = 1/96in，沿用 px 的话 595px 只占 A4 的 3/4 宽（右边一大块空白）。
       pt 是绝对单位，595pt 就是 595pt 纸宽——与旧 createPDF「1px 映射 1pt」的观感一致。 */
    body {
        margin: 0; padding: 0;
        font-family: -apple-system, "PingFang SC", "Helvetica Neue", "Microsoft YaHei", sans-serif;
        font-size: 12pt; line-height: 1.65; color: #1c1c1e;
        -webkit-print-color-adjust: exact;   /* 通行写法；本机实测打印默认就印底色，留着防将来默认变 */
    }
    /* 填满 @page 的可打印区（595-104=491pt，与「padding 版」正文列宽一致，换法不改排版） */
    .page { width: auto; }
    .doc-title { font-size: 20pt; font-weight: 600; color: #111; line-height: 1.3; }
    .doc-tags { color: #8e8e93; font-size: 10pt; margin-top: 6pt; }
    .rule { border-top: 1pt solid #e5e5ea; margin: 18pt 0 22pt; }
    h1, h2, h3, h4, h5, h6 { color: #111; font-weight: 600; line-height: 1.3; margin: 20pt 0 10pt; }
    h1 { font-size: 16pt; }
    h2 { font-size: 14pt; border-bottom: 1pt solid #e5e5ea; padding-bottom: 5pt; }
    h3 { font-size: 12.5pt; }
    h4, h5, h6 { font-size: 11.5pt; }
    p { margin: 8pt 0; }
    a { color: #1f6feb; text-decoration: none; }
    code {
        font-family: "SF Mono", Menlo, Consolas, monospace;
        font-size: .86em; background: #f2f2f4; padding: 1pt 5pt; border-radius: 4pt;
    }
    pre {
        background: #f6f6f8; border: 1pt solid #e5e5ea; border-radius: 8pt;
        padding: 10pt 12pt; overflow-wrap: break-word; white-space: pre-wrap;
    }
    pre code { background: none; padding: 0; border-radius: 0; }
    table { border-collapse: collapse; width: 100%; margin: 12pt 0; font-size: 10.5pt; }
    th, td { border: 1pt solid #d1d1d6; padding: 6pt 10pt; text-align: left; vertical-align: top; }
    th { background: #f2f2f7; font-weight: 600; }
    tr:nth-child(even) td { background: #fafafa; }
    blockquote {
        margin: 12pt 0; padding: 8pt 16pt; border-left: 4pt solid #d1d1d6;
        color: #6e6e73; background: #fbfbfc;
    }
    ul, ol { padding-left: 24pt; }
    li { margin: 3pt 0; }
    hr { border: none; border-top: 1pt solid #e5e5ea; margin: 20pt 0; }
    img { max-width: 100%; height: auto; }
    .mermaid { margin: 16pt 0; text-align: center; }
    .mermaid img { max-width: 100%; }
    code, pre, blockquote { white-space: normal; }
    /* 分页：整块内容不跨页被切两半（与 Web 端 useExportPDF.js 同一套做法）。
       故意**不含 p**：比一页还长的段落引擎只能硬断，加了 avoid 反而会把整段推到下一页、上一页留大片空白。 */
    pre, table, blockquote, img, .mermaid { break-inside: avoid; }
    h1, h2, h3, h4, h5, h6 { break-after: avoid; }
</style>
</head>
<body>
<div class="page">
    <div class="doc-title">__TITLE_HTML__</div>
    <div class="doc-tags">__TAGS_HTML__</div>
    <div class="rule"></div>
    <div id="content">加载中…</div>
</div>
<script>
__MARKED_JS__
</script>
<script>
    const md = __MD_JSON__;
    const images = __IMAGES_JSON__;
    const html = marked.parse(md, { gfm: true, breaks: true });
    document.getElementById("content").innerHTML = html.replace(
        /ZZMDIMG(\\d+)X/g,
        function (match, idx) {
            return '<div class="mermaid"><img src="' + images[Number(idx)] + '"></div>';
        }
    );
</script>
</body>
</html>
"""
#endif
#endif
