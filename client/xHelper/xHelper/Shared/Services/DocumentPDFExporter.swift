import Foundation
import BeautifulMermaid
#if os(macOS)
import AppKit
import WebKit

/// 文档 → 排版 PDF 导出器（macOS 专属）。
///
/// 管线：mermaid 段渲染 PNG（浅色主题、1x、纵向翻转）→ 打包的 marked.min.js 将 markdown 转 HTML
/// （图表占位符在 JS 端替换为 `<img>`）→ 离屏 WKWebView（A4 宽度）loadHTMLString →
/// `createPDF` 整长页 → CoreGraphics 按 A4 切片 → 返回多页 PDF Data。
/// 全程离线，不依赖外网。
@MainActor
enum DocumentPDFExporter {

    /// A4 页尺寸（pt）。用于 webView 排布宽度 + 最终切片目标页大小。
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

    /// 离屏 WKWebView 渲染 + createPDF 整长页 + CoreGraphics 按 A4 切片。
    /// 注意：macOS 上 createPDF 要求 webView 在视图层级中（挂到隐藏 window）。
    private static func renderPDF(html: String, baseURL: URL?) async throws -> Data {
        let loadWaiter = PDFLoadWaiter()

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

        // 与文档阅读器一致：非持久 store，避免文档图 URL 被持久 HTTP 缓存污染。
        let pdfConfig = WKWebViewConfiguration()
        pdfConfig.websiteDataStore = .nonPersistent()
        let webView = WKWebView(frame: CGRect(origin: .zero, size: pageSize), configuration: pdfConfig)
        window.contentView = webView
        webView.navigationDelegate = loadWaiter
        webView.loadHTMLString(html, baseURL: baseURL)
        print("[PDFExport] webview attached to hidden window, waiting for didFinish")
        await loadWaiter.waitForLoad()
        // 留给 marked 注入 innerHTML 后的排版/图片解码时间
        try? await Task.sleep(nanoseconds: 350_000_000)
        print("[PDFExport] didFinish/wait done, generating full-page PDF (html=\(html.count) chars)")

        let config = WKPDFConfiguration()
        // 不设 rect（默认 .null / .zero），WebKit 自动捕获全部可滚动内容。
        let timeout: Double = 120

        let data: Data = try await withCheckedThrowingContinuation { (cont: CheckedContinuation<Data, Error>) in
            var resumed = false
            print("[PDFExport] createPDF start, timeout=\(timeout)s")
            DispatchQueue.main.asyncAfter(deadline: .now() + timeout) {
                if !resumed {
                    resumed = true
                    print("[PDFExport] createPDF timed out after \(timeout)s")
                    cont.resume(throwing: DocumentExportError.pdfTimeout)
                }
            }
            webView.createPDF(configuration: config) { result in
                if resumed {
                    print("[PDFExport] createPDF callback arrived but already resumed (timeout)")
                    return
                }
                resumed = true
                switch result {
                case .success(let d):
                    print("[PDFExport] createPDF success, \(d.count) bytes")
                    cont.resume(returning: d)
                case .failure(let e):
                    print("[PDFExport] createPDF failed: \(e)")
                    cont.resume(throwing: e)
                }
            }
        }
        guard !data.isEmpty else {
            print("[PDFExport] empty PDF data, throwing emptyPDF")
            throw DocumentExportError.emptyPDF
        }
        print("[PDFExport] slicing \(data.count) bytes into A4 pages")
        let sliced = slicePDF(data, pageSize: pageSize)
        print("[PDFExport] sliced done, \(sliced.count) bytes")
        return sliced
    }

    /// 把一张长页（媒体盒 = 全部内容高度）切成若干 A4 页，保证内容完整（不保证断字位置优雅）。
    private static func slicePDF(_ data: Data, pageSize: CGSize) -> Data {
        guard let provider = CGDataProvider(data: data as CFData),
              let doc = CGPDFDocument(provider),
              let page = doc.page(at: 1) else {
            return data
        }
        let src = page.getBoxRect(.mediaBox)
        guard src.width > 0, src.height > 0 else { return data }

        let output = NSMutableData()
        guard let consumer = CGDataConsumer(data: output as CFMutableData) else { return data }
        var mediaBox = CGRect(x: 0, y: 0, width: pageSize.width, height: pageSize.height)
        guard let ctx = CGContext(consumer: consumer, mediaBox: &mediaBox, nil) else { return data }

        let scale = pageSize.width / src.width
        let bandH = pageSize.height / scale
        let totalPages = Int(ceil(src.height / bandH))
        print("[PDFExport] slicePDF: source=\(src.width)x\(src.height), scale=\(scale), pages=\(totalPages)")
        var pageCount = 0
        var bandTop = src.maxY
        while bandTop > src.minY {
            let bandHeight = pageSize.height / scale
            let bandBottom = max(bandTop - bandHeight, src.minY)
            ctx.beginPDFPage(nil)
            ctx.saveGState()
            ctx.scaleBy(x: scale, y: scale)
            ctx.translateBy(x: -src.minX, y: -bandBottom)
            ctx.drawPDFPage(page)
            ctx.restoreGState()
            ctx.endPDFPage()
            bandTop -= bandHeight
            pageCount += 1
        }
        ctx.closePDF()
        print("[PDFExport] slicePDF done: wrote \(pageCount) pages, \(output.length) bytes")
        return output as Data
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
    case pdfTimeout

    var errorDescription: String? {
        switch self {
        case .markedResourceNotFound: return "缺少内置渲染脚本 marked.min.js"
        case .emptyPDF: return "PDF 生成失败：WebKit 未返回数据"
        case .pdfTimeout: return "PDF 生成超时 (15s)"
        }
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
        // 加载失败也放行（createPDF 阶段再兜底报错）
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
    :root { color-scheme: light; }
    * { box-sizing: border-box; }
    body {
        margin: 0; padding: 0;
        font-family: -apple-system, "PingFang SC", "Helvetica Neue", "Microsoft YaHei", sans-serif;
        font-size: 12pt; line-height: 1.65; color: #1c1c1e;
    }
    .page { width: 595px; padding: 44px 52px; }
    .doc-title { font-size: 20pt; font-weight: 600; color: #111; line-height: 1.3; }
    .doc-tags { color: #8e8e93; font-size: 10pt; margin-top: 6px; }
    .rule { border-top: 1px solid #e5e5ea; margin: 18px 0 22px; }
    h1, h2, h3, h4, h5, h6 { color: #111; font-weight: 600; line-height: 1.3; margin: 20px 0 10px; }
    h1 { font-size: 16pt; }
    h2 { font-size: 14pt; border-bottom: 1px solid #e5e5ea; padding-bottom: 5px; }
    h3 { font-size: 12.5pt; }
    h4, h5, h6 { font-size: 11.5pt; }
    p { margin: 8px 0; }
    a { color: #1f6feb; text-decoration: none; }
    code {
        font-family: "SF Mono", Menlo, Consolas, monospace;
        font-size: .86em; background: #f2f2f4; padding: 1px 5px; border-radius: 4px;
    }
    pre {
        background: #f6f6f8; border: 1px solid #e5e5ea; border-radius: 8px;
        padding: 10px 12px; overflow-wrap: break-word; white-space: pre-wrap;
    }
    pre code { background: none; padding: 0; border-radius: 0; }
    table { border-collapse: collapse; width: 100%; margin: 12px 0; font-size: 10.5pt; }
    th, td { border: 1px solid #d1d1d6; padding: 6px 10px; text-align: left; vertical-align: top; }
    th { background: #f2f2f7; font-weight: 600; }
    tr:nth-child(even) td { background: #fafafa; }
    blockquote {
        margin: 12px 0; padding: 8px 16px; border-left: 4px solid #d1d1d6;
        color: #6e6e73; background: #fbfbfc;
    }
    ul, ol { padding-left: 24px; }
    li { margin: 3px 0; }
    hr { border: none; border-top: 1px solid #e5e5ea; margin: 20px 0; }
    img { max-width: 100%; height: auto; }
    .mermaid { margin: 16px 0; text-align: center; }
    .mermaid img { max-width: 100%; }
    code, pre, blockquote { white-space: normal; }
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
