import Combine
import Foundation
import SwiftUI

#if os(macOS)
import AppKit
import WebKit
import BeautifulMermaid

/// 文档正文阅读器状态：Swift 侧的把手，负责往 WebView 里灌 JS 指令、收回 JS 的回话，
/// 并持有服务端的划线与批注（DOM 只负责渲染，落库以服务端为准）。
/// 由 `DocumentFullView` 持有（`@StateObject`），生命周期跟文档详情页一致。
@MainActor
final class DocumentReaderController: ObservableObject {

    /// 查找命中总数
    @Published private(set) var matchCount = 0
    /// 当前命中下标（0-based；-1 = 无命中）
    @Published private(set) var matchIndex = -1
    /// 本文件的划线与批注（P4 抽屉也用这份；正文里画什么由 `reader.setAnnotations` 决定）
    @Published private(set) var annotations: [DocumentAnnotation] = []
    /// 正在「重新定位」的那条（P4 抽屉里那一行显示「请在正文中重新选中」；nil = 不在该模式）
    @Published private(set) var relocatingID: String?

    // MARK: 讨论线程（批注下挂的 AI 回复 / 追问）
    /// 已加载的线程缓存（key = 批注 id）。未加载过的键不存在 = 需要拉取。
    @Published private(set) var repliesByAnnotation: [String: [AnnotationReply]] = [:]
    /// 展开了线程的批注
    @Published private(set) var expandedAnnotationIDs: Set<String> = []
    /// 正在拉取线程
    @Published private(set) var loadingReplyIDs: Set<String> = []
    /// 正在生成回复（spinner）
    @Published private(set) var busyReplyIDs: Set<String> = []

    // MARK: 连线（批注卡片 → 正文划线）
    /// 正在追踪的批注 id（鼠标悬停/操作卡片时设置）
    @Published private(set) var connectorID: String?
    /// 该划线在**阅读器内**的矩形（viewport 坐标，左上原点；nil = 未画出/失活）
    @Published private(set) var connectorRect: CGRect?

    /// 已装载内容的签名。变了才重载 —— 否则 `updateNSView` 每次都重新渲染
    /// markdown + mermaid（mermaid 是原生渲染，很贵）。
    fileprivate var loadedSignature = ""
    fileprivate weak var webView: WKWebView?

    /// 文档 id（批注 / 划线接口用）
    let documentID: String
    private let api: APIClient
    private var hasLoadedAnnotations = false
    private var isLoadingAnnotations = false

    init(documentID: String, api: APIClient) {
        self.documentID = documentID
        self.api = api
    }

    /// 文档配图的基准地址：正文里 `![](/images/xxx.png)` 是相对路径，交给 WKWebView 的
    /// baseURL 解析成绝对地址（文档图已是 public，无需鉴权头）。
    var imageBaseURL: URL? { URL(string: api.baseURL) }

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

    /// 需要 JS 返回值时用这个（`evaluate` 的返回被丢掉了，比如没命中滚动目标）
    fileprivate func evaluateBool(_ js: String, _ done: @escaping (Bool) -> Void) {
        guard let webView else { return done(false) }
        webView.evaluateJavaScript(js) { value, _ in done((value as? NSNumber)?.boolValue ?? false) }
    }

    // MARK: - 划线与批注（服务端为准）

    /// 全量重推给 JS 重画。任何增删改成功后都走这里 —— JS 不做乐观更新，
    /// 也就没有「回滚对账」这回事：画出来的永远是服务端那一份。
    private func pushAnnotations() {
        guard let data = try? JSONEncoder().encode(annotations),
              let json = String(data: data, encoding: .utf8) else { return }
        evaluate("reader.setAnnotations(\(MarkdownWebUtils.jsString(json)))")
        // 重画后 mark 是新的一批，正在连线的矩形要重取
        if let id = connectorID { measureConnector(id: id) }
    }

    private func loadAnnotations() async {
        guard !isLoadingAnnotations else { return }
        isLoadingAnnotations = true
        defer { isLoadingAnnotations = false }
        do {
            let resp = try await api.listAnnotations(documentId: documentID)
            annotations = resp.annotations.sorted(by: Self.order)
            // 服务端那份为准：清掉已不存在的批注残留的线程缓存/展开态
            let live = Set(annotations.map(\.id))
            for id in repliesByAnnotation.keys where !live.contains(id) { forgetThread(id: id) }
            expandedAnnotationIDs.formIntersection(live)
            hasLoadedAnnotations = true
            pushAnnotations()
        } catch {
            LogBuffer.shared.error("DocumentReader 载入划线批注失败: \(error)")
            ToastCenter.shared.show("划线批注载入失败：\(error.localizedDescription)")
        }
    }

    private func create(quote: String, prefix: String, suffix: String, note: String) async {
        do {
            let a = try await api.createAnnotation(
                documentId: documentID, quote: quote, note: note, prefix: prefix, suffix: suffix
            )
            annotations.append(a)
            annotations.sort(by: Self.order)
            pushAnnotations()
            if a.orphaned {
                ToastCenter.shared.show("这段文字已不在正文里，记为「已失活」")
            }
        } catch {
            LogBuffer.shared.error("DocumentReader 创建划线批注失败: \(error)")
            ToastCenter.shared.show("添加失败：\(error.localizedDescription)")
        }
    }

    private func update(id: String, note: String) async {
        do {
            let updated = try await api.updateAnnotation(id: id, note: note)
            if let i = annotations.firstIndex(where: { $0.id == updated.id }) {
                annotations[i].note = updated.note
            }
            pushAnnotations()
        } catch {
            LogBuffer.shared.error("DocumentReader 更新批注失败: \(error)")
            ToastCenter.shared.show("保存失败：\(error.localizedDescription)")
        }
    }

    private func remove(id: String) async {
        do {
            try await api.deleteAnnotation(id: id)
            annotations.removeAll { $0.id == id }
            forgetThread(id: id)
            if relocatingID == id { cancelRelocate() }
            pushAnnotations()
        } catch {
            LogBuffer.shared.error("DocumentReader 删除划线批注失败: \(error)")
            ToastCenter.shared.show("删除失败：\(error.localizedDescription)")
        }
    }

    // MARK: - 讨论线程动作

    /// 展开/折叠一条批注的线程；首次展开时拉取。
    func toggleThread(id: String) {
        if expandedAnnotationIDs.contains(id) {
            expandedAnnotationIDs.remove(id)
        } else {
            expandedAnnotationIDs.insert(id)
            if repliesByAnnotation[id] == nil, !loadingReplyIDs.contains(id) {
                Task { await loadReplies(annotationId: id) }
            }
        }
    }

    private func loadReplies(annotationId: String) async {
        loadingReplyIDs.insert(annotationId)
        defer { loadingReplyIDs.remove(annotationId) }
        do {
            let resp = try await api.listAnnotationReplies(annotationId: annotationId)
            repliesByAnnotation[annotationId] = resp.replies
        } catch {
            LogBuffer.shared.error("DocumentReader 载入批注讨论失败: \(error)")
            ToastCenter.shared.show("讨论载入失败：\(error.localizedDescription)")
            expandedAnnotationIDs.remove(annotationId)
        }
    }

    /// 让 AI 回复 / 追问。`content` 非空 = 用户追问。成功后把新增回复并入线程。
    func reply(annotationId: String, content: String?) async {
        guard !busyReplyIDs.contains(annotationId) else { return }
        busyReplyIDs.insert(annotationId)
        defer { busyReplyIDs.remove(annotationId) }
        expandedAnnotationIDs.insert(annotationId)
        do {
            let resp = try await api.replyToAnnotation(id: annotationId, content: content)
            var thread = repliesByAnnotation[annotationId] ?? []
            thread.append(contentsOf: resp.added)
            repliesByAnnotation[annotationId] = thread
            if let i = annotations.firstIndex(where: { $0.id == annotationId }) {
                annotations[i].replyCount = resp.replyCount
                annotations[i].lastReply = resp.added.last ?? annotations[i].lastReply
            }
        } catch {
            LogBuffer.shared.error("DocumentReader 生成批注回复失败: \(error)")
            ToastCenter.shared.show("生成回复失败：\(error.localizedDescription)")
        }
    }

    /// 丢掉某条批注的线程缓存与展开态
    private func forgetThread(id: String) {
        repliesByAnnotation.removeValue(forKey: id)
        expandedAnnotationIDs.remove(id)
        loadingReplyIDs.remove(id)
        busyReplyIDs.remove(id)
    }

    // MARK: - 连线追踪

    /// 开始追踪某条批注的划线位置（卡片悬停时调）
    func beginConnectorTracking(id: String) {
        connectorID = id
        measureConnector(id: id)
    }

    func endConnectorTracking() {
        connectorID = nil
        connectorRect = nil
    }

    /// 让 JS 量一次划线矩形；异步回来，期间若已换目标就丢弃。
    private func measureConnector(id: String) {
        guard let webView else { connectorRect = nil; return }
        webView.evaluateJavaScript("reader.annotationRect(\(MarkdownWebUtils.jsString(id)))") { [weak self] value, _ in
            Task { @MainActor in
                guard let self, self.connectorID == id else { return }
                guard let d = value as? [String: Any],
                      let x = (d["x"] as? NSNumber)?.doubleValue,
                      let y = (d["y"] as? NSNumber)?.doubleValue,
                      let w = (d["w"] as? NSNumber)?.doubleValue,
                      let h = (d["h"] as? NSNumber)?.doubleValue else {
                    self.connectorRect = nil
                    return
                }
                self.connectorRect = CGRect(x: x, y: y, width: w, height: h)
            }
        }
    }

    // MARK: - 抽屉（P4）用的对外动作

    /// 滚到某条划线并闪烁。JS 在当前 DOM 里定位不到（失活的、或排版对不上）就提示一声。
    func scrollToAnnotation(id: String) {
        evaluateBool("reader.scrollToAnnotation(\(MarkdownWebUtils.jsString(id)))") { hit in
            if !hit { ToastCenter.shared.show("这条划线在当前正文里定位不到") }
        }
    }

    /// 正文被改写后重拉：失活与否由服务端重算，客户端不自己猜。
    func refreshAnnotations() {
        Task { await loadAnnotations() }
    }

    /// 抽屉里失活行的「删除」
    func deleteAnnotation(id: String) {
        Task { await remove(id: id) }
    }

    /// 抽屉「重新定位」：进入重选模式，用户在正文里选中新的一段后由 JS 回传。
    func beginRelocate(id: String) {
        relocatingID = id
        evaluate("reader.beginRelocate(\(MarkdownWebUtils.jsString(id)))")
    }

    func cancelRelocate() {
        relocatingID = nil
        evaluate("reader.cancelRelocate()")
    }

    private func relocate(id: String, quote: String, prefix: String, suffix: String) async {
        do {
            let a = try await api.relocateAnnotation(id: id, quote: quote, prefix: prefix, suffix: suffix)
            if let i = annotations.firstIndex(where: { $0.id == a.id }) { annotations[i] = a }
            annotations.sort(by: Self.order)
            pushAnnotations()
            ToastCenter.shared.show("已重新定位")
            scrollToAnnotation(id: a.id)
        } catch {
            LogBuffer.shared.error("DocumentReader 重新定位失败: \(error)")
            ToastCenter.shared.show("重新定位失败：\(error.localizedDescription)")
        }
    }

    /// 与服务端 `List` 的排序一致（start_offset ASC, id ASC），画线顺序才稳定
    private static func order(_ a: DocumentAnnotation, _ b: DocumentAnnotation) -> Bool {
        a.startOffset != b.startOffset ? a.startOffset < b.startOffset : a.id < b.id
    }

    // MARK: - JS → Swift

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
            // 划线批注只在首次 ready 时拉一次；此后 ready（HTML 重载）直接重画进新 DOM
            if hasLoadedAnnotations {
                pushAnnotations()
            } else {
                Task { await loadAnnotations() }
            }

        case "create":
            let quote = message["quote"] as? String ?? ""
            guard !quote.isEmpty else { return }
            Task {
                await create(
                    quote: quote,
                    prefix: message["prefix"] as? String ?? "",
                    suffix: message["suffix"] as? String ?? "",
                    note: message["note"] as? String ?? ""
                )
            }

        case "update":
            guard let id = message["id"] as? String else { return }
            Task { await update(id: id, note: message["note"] as? String ?? "") }

        case "delete":
            guard let id = message["id"] as? String else { return }
            Task { await remove(id: id) }

        case "relocate":
            // 重定位模式下用户选完了一段：模式已在 JS 侧退出，这里只管落库
            guard let id = message["id"] as? String else { return }
            relocatingID = nil
            Task {
                await relocate(
                    id: id,
                    quote: message["quote"] as? String ?? "",
                    prefix: message["prefix"] as? String ?? "",
                    suffix: message["suffix"] as? String ?? ""
                )
            }

        case "relocateCancel":
            relocatingID = nil

        case "scroll":
            // 正文滚动后划线位置变了，正在连线就重取一次
            if let id = connectorID { measureConnector(id: id) }

        case "copy":
            // 剪贴板交给原生：`navigator.clipboard` 在 loadHTMLString 出来的页面里不是安全上下文，
            // 能不能用全看运气
            let text = message["text"] as? String ?? ""
            guard !text.isEmpty else { return }
            let pb = NSPasteboard.general
            pb.clearContents()
            pb.setString(text, forType: .string)
            ToastCenter.shared.show("已复制")

        case "toast":
            // JS 侧的轻提示（如：选区没有可锚定的文字）
            let text = message["text"] as? String ?? ""
            if !text.isEmpty { ToastCenter.shared.show(text) }

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
        webView.navigationDelegate = context.coordinator

        controller.webView = webView
        context.coordinator.lastFontSize = fontSize
        context.coordinator.loadedSignature = signature
        webView.loadHTMLString(buildHTML(), baseURL: controller.imageBaseURL)
        return webView
    }

    func updateNSView(_ webView: WKWebView, context: Context) {
        if signature != context.coordinator.loadedSignature {
            let html = buildHTML()
            context.coordinator.loadedSignature = signature
            // 先问出滚动位置再换 HTML，ready 回话时补回去
            webView.evaluateJavaScript("window.scrollY") { value, _ in
                context.coordinator.controller.pendingScrollY = (value as? NSNumber)?.doubleValue
                webView.loadHTMLString(html, baseURL: controller.imageBaseURL)
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

    final class Coordinator: NSObject, WKScriptMessageHandler, WKNavigationDelegate {
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

        /// 正文里的 `[标题](xhanhelper://document/<id>)` 不能交给 WebKit（无 handler 时会被甩给
        /// NSWorkspace，弹「未设定用来打开URL…的应用程序」）。拦下来交宿主打开，其余导航放行。
        nonisolated func webView(_ webView: WKWebView, decidePolicyFor navigationAction: WKNavigationAction,
                                 decisionHandler: @escaping (WKNavigationActionPolicy) -> Void) {
            guard let url = navigationAction.request.url, url.scheme == "xhanhelper" else {
                decisionHandler(.allow)
                return
            }
            decisionHandler(.cancel)
            guard let id = DocumentSelectionCoordinator.documentID(from: url) else { return }
            Task { @MainActor in DocumentSelectionCoordinator.shared.openDocument(id: id) }
        }
    }
}

// MARK: - HTML 组装

/// 屏幕阅读版 HTML：与 `DocumentPDFExporter` 同一套骨架（marked.min.js + 图表占位替换），
/// 但 CSS 换成阅读版 —— 不加 A4 的 `595px` / `12pt` 打印尺寸，改自适应列宽
/// （`clamp(728px, 68vw, 1100px)`：窄栏维持 728，宽窗按 68% 视口长到 1100 封顶）+ 可调字号，
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
.page { max-width: clamp(728px, 68vw, 1100px); margin: 0 auto; padding: 34px 40px 120px; }

.doc-title { font-size: calc(var(--fs) * 1.55); font-weight: 700; line-height: 1.32; color: var(--text); letter-spacing: -.2px; }
.doc-tags { margin-top: 10px; display: flex; gap: 6px; flex-wrap: wrap; }
.doc-tags:empty { display: none; }
.doc-tags span {
  font-size: calc(var(--fs) * 0.63); color: var(--accent); background: var(--accent-soft);
  padding: 2px 8px; border-radius: 20px; font-weight: 500; white-space: nowrap;
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

/* ============ 划线批注（P3） ============
   浮层配色沿用 mockup 被确认过的那套：--surface / --win-chrome 是中性浮层色
   （Assets 里没有对应 colorset），其余仍从 --text / --accent 派生。 */
:root {
  --faint: color-mix(in srgb, var(--text) 38%, transparent);
  --danger: #FF3B30;
  --on-accent: #FFFFFF;
  --surface: #FCFCFC;
  --win-chrome: rgba(250, 250, 250, .72);
  /* 划线 = 文字底部波浪线（原生 text-decoration，引擎逐行片段画）：
     --hl/--hl-hover 是"线的颜色"，不是底色。想要底色高亮得另加背景。
     线色要实、浅色下还得压深一档 —— 暖米底上的浅琥珀（试过 rgba(240,186,60,.85)）
     用户反馈"太浅不显眼"，所以用实色赭金。 */
  --hl: rgba(217, 151, 27, 1);
  --hl-hover: rgba(186, 125, 10, 1);
  --wave-w: 1.5px;    /* 线粗 */
  --wave-off: 3px;    /* 下划线偏移 */
  --wave-skip: none;  /* auto = 遇到 g/y 等降部断开，none = 连续 */
  --shadow: 0 12px 34px rgba(0,0,0,.16), 0 2px 8px rgba(0,0,0,.08);
}
@media (prefers-color-scheme: dark) {
  :root {
    --on-accent: #141416;
    --surface: #2C2E2D;          /* surfaceInput dark */
    --win-chrome: rgba(40, 40, 42, .72);
    --hl: rgba(146, 200, 180, .90);
    --hl-hover: rgba(146, 200, 180, 1);
    --shadow: 0 16px 40px rgba(0,0,0,.55), 0 2px 10px rgba(0,0,0,.4);
  }
}

mark.hl {
  color: inherit;
  /* mark 的 UA 默认底色是黄色，必须显式清掉，否则"划线"底下会透出一块黄底 */
  background: transparent;
  cursor: pointer;
  text-decoration: underline;
  text-decoration-style: wavy;
  text-decoration-color: var(--hl);
  text-decoration-thickness: var(--wave-w);
  text-underline-offset: var(--wave-off);
  text-decoration-skip-ink: var(--wave-skip);
  transition: text-decoration-color .13s;
}
mark.hl:hover { text-decoration-color: var(--hl-hover); }
mark.hl.flash { animation: flash 1.05s ease-out; }
@keyframes flash {
  0%, 30% { text-decoration-color: var(--accent); text-decoration-thickness: 3px; }
  100% { text-decoration-color: var(--hl); text-decoration-thickness: var(--wave-w); }
}

/* 批注标记：句尾一个小圆点，只用线色 —— 没有图标、没有底色。
   原来是个绿色圆底 + 气泡图标，真机上太抢眼（用户："有点丑，而且很突兀"）。
   候选对比见 docs/design/document-highlight-annotation/mockup.html 控制条的「批注标记」。 */
span.ann-bubble {
  display: inline-block; width: 5px; height: 5px; margin: 0 2px 0 3px;
  border-radius: 50%; background: var(--hl-hover);
  vertical-align: 1px; cursor: pointer;
  transition: transform .13s;
}
span.ann-bubble:hover { transform: scale(1.6); }

/* 浮动工具条 / 菜单 / 浮层 */
.floating {
  position: fixed; z-index: 60; display: none;
  background: var(--win-chrome);
  backdrop-filter: blur(22px) saturate(180%);
  -webkit-backdrop-filter: blur(22px) saturate(180%);
  border: 1px solid var(--border);
  border-radius: 11px;
  box-shadow: var(--shadow);
  padding: 5px;
  gap: 2px;
}
.floating.open { display: flex; }
.floating button {
  display: flex; flex-direction: column; align-items: center; gap: 2px;
  min-width: 50px; padding: 6px 8px 5px;
  border: 0; border-radius: 8px; background: none; cursor: pointer;
  color: var(--text); font-family: inherit; font-size: 10.5px;
  transition: background .13s;
}
.floating button:hover { background: var(--accent-soft); color: var(--accent); }
.floating button svg { width: 16px; height: 16px; }
.floating .sep { width: 1px; background: var(--border-soft); margin: 5px 3px; }

.note-editor {
  position: fixed; z-index: 70; display: none; width: 306px;
  background: var(--surface); border: 1px solid var(--border);
  border-radius: 12px; box-shadow: var(--shadow);
  padding: 12px;
}
.note-editor.open { display: block; }
.note-editor .quote {
  font-size: 11.5px; line-height: 1.55; color: var(--muted);
  border-left: 2.5px solid var(--hl-hover);
  padding: 1px 0 1px 9px; margin-bottom: 9px;
  max-height: 62px; overflow: hidden;
}
.note-editor textarea {
  width: 100%; height: 84px; resize: none;
  border: 1px solid var(--border); border-radius: 8px;
  background: var(--surface-2); color: var(--text);
  padding: 8px 10px; font-size: 12.5px; line-height: 1.6;
  font-family: inherit; outline: none;
}
.note-editor textarea:focus { border-color: var(--accent); background: var(--surface); }
.note-editor .foot { display: flex; align-items: center; gap: 8px; margin-top: 9px; }
.note-editor .foot .spacer { flex: 1; }
.note-editor .hint { font-size: 10.5px; color: var(--faint); }
.btn {
  border: 1px solid var(--border); background: var(--surface-2); color: var(--text);
  font-family: inherit; font-size: 12px; padding: 5px 12px; border-radius: 7px; cursor: pointer;
  transition: background .13s;
}
.btn:hover { background: var(--border-soft); }
.btn.primary { background: var(--accent); border-color: var(--accent); color: var(--on-accent); font-weight: 500; }
.btn.primary:hover { filter: brightness(1.08); }

.popover {
  position: fixed; z-index: 65; display: none;
  /* 长批注（P5 把 AI 回答存进来之后常态）：卡片自己封顶内滚。
     不封顶的话它是 fixed 的，超出视口那截永远拿不到；滚轮也会冒泡去滚正文。
     contain 防「滚到底连滚正文」；宽度放宽是为了少滚（340 会让长回答拉成一根带） */
  max-width: min(420px, 88vw); max-height: min(62vh, 520px);
  overflow-y: auto; overscroll-behavior: contain;
  /* 卡片里不要滚动条（内容照样滚，与 macOS 覆盖式滚动条一致的观感） */
  scrollbar-width: none;
  background: var(--surface); border: 1px solid var(--border);
  border-radius: 11px; box-shadow: var(--shadow); padding: 11px 13px;
}
.popover::-webkit-scrollbar { width: 0; height: 0; }
.popover.open { display: block; }
.popover .p-quote {
  font-size: 11.5px; line-height: 1.55; color: var(--muted);
  border-left: 2.5px solid var(--hl-hover); padding-left: 9px; margin-bottom: 8px;
  display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden;
}
.popover .p-note { font-size: 13px; line-height: 1.68; color: var(--text); white-space: pre-wrap; overflow-wrap: anywhere; }
.popover .p-foot { margin-top: 9px; font-size: 10.5px; color: var(--faint); display: flex; align-items: center; gap: 8px; }
.popover .p-item + .p-item { margin-top: 10px; padding-top: 10px; border-top: 1px solid var(--border-soft); }
.popover .p-tag { font-size: 10px; color: var(--accent); background: var(--accent-soft); padding: 1px 6px; border-radius: 20px; }

.ctxmenu {
  position: fixed; z-index: 72; display: none; min-width: 158px;
  background: var(--win-chrome);
  backdrop-filter: blur(22px) saturate(180%);
  -webkit-backdrop-filter: blur(22px) saturate(180%);
  border: 1px solid var(--border); border-radius: 10px;
  box-shadow: var(--shadow); padding: 5px;
}
.ctxmenu.open { display: block; }
.ctxmenu button {
  display: flex; align-items: center; gap: 9px; width: 100%;
  border: 0; background: none; font-family: inherit; font-size: 12.5px;
  color: var(--text); padding: 6px 9px; border-radius: 6px; cursor: pointer; text-align: left;
}
.ctxmenu button:hover { background: var(--accent-soft); color: var(--accent); }
.ctxmenu button svg { width: 14px; height: 14px; opacity: .8; }
.ctxmenu button.danger:hover { background: color-mix(in srgb, var(--danger) 12%, transparent); color: var(--danger); }
.ctxmenu hr { border: 0; border-top: 1px solid var(--border-soft); margin: 4px 6px; }
</style>
</head>
<body>
<div class="page">
  <div class="doc-title">__TITLE_HTML__</div>
  <div class="doc-tags">__TAGS_HTML__</div>
  <hr class="doc-rule">
  <div id="content"></div>
</div>

<!-- 选区工具条 / 批注浮层 / hover popover / 点击菜单：都在 HTML 里，
     跟着选区坐标走（`.ui` 类用来把「点浮层」和「点正文」区分开） -->
<div class="floating ui" id="toolbar">
  <button data-act="hl">
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M4 20h16"/><path d="M14.5 4.5l5 5L11 18H6v-5z"/></svg>
    划线
  </button>
  <button data-act="note">
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M20 12a7 7 0 0 1-7 7H9l-4.2 2.6L6 17.5A7 7 0 1 1 20 12z"/></svg>
    批注
  </button>
  <div class="sep"></div>
  <button data-act="copy">
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="9" y="9" width="11" height="11" rx="2"/><path d="M5 15V5a2 2 0 0 1 2-2h10"/></svg>
    复制
  </button>
</div>

<div class="note-editor ui" id="note-editor">
  <div class="quote" id="ne-quote"></div>
  <textarea id="ne-text" placeholder="写点什么…"></textarea>
  <div class="foot">
    <span class="hint">⌘↵ 保存</span>
    <div class="spacer"></div>
    <button class="btn" id="ne-cancel">取消</button>
    <button class="btn primary" id="ne-save">保存为纯划线</button>
  </div>
</div>

<div class="popover ui" id="popover"></div>

<div class="ctxmenu ui" id="ctxmenu">
  <button data-cact="edit">
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 20h9"/><path d="M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4z"/></svg>
    <span id="cact-edit-label">编辑批注</span>
  </button>
  <button data-cact="copy">
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="9" y="9" width="11" height="11" rx="2"/><path d="M5 15V5a2 2 0 0 1 2-2h10"/></svg>
    复制原文
  </button>
  <hr>
  <button data-cact="del" class="danger">
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M4 7h16M9 7V5h6v2M6 7l1 13h10l1-13"/></svg>
    删除划线
  </button>
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

  var BLOCK_TAGS = { P: 1, DIV: 1, H1: 1, H2: 1, H3: 1, H4: 1, H5: 1, H6: 1,
                     UL: 1, OL: 1, LI: 1, BLOCKQUOTE: 1, PRE: 1, TABLE: 1,
                     TR: 1, TD: 1, TH: 1, FIGURE: 1, HR: 1 };

  /* 文本节点所属的最近块级祖先（到 #content 为止）。 */
  function blockOf(node) {
    var el = node.nodeType === 1 ? node : node.parentNode;
    while (el && el !== content) {
      if (el.nodeType === 1 && BLOCK_TAGS[el.tagName]) return el;
      el = el.parentNode;
    }
    return content;
  }

  /* 把 #content 拍平成一条字符串 + 文本节点的偏移索引。
     查找与划线都靠它做「字符串下标 → DOM Range」的换算。

     块边界补一个 '\n'：marked 渲染出的 `<p>a</p><p>b</p>` 拍平后是 "ab"，
     而跨段选区的 quote 在服务端是 "a b"（空白折叠成单空格）—— 不补分隔符，
     跨段划线在 DOM 里根本搜不到。补上后两边投影到同一空间。 */
  function flatten() {
    var walker = document.createTreeWalker(content, NodeFilter.SHOW_TEXT, null);
    var nodes = [];
    var text = '';
    var lastBlock = null;
    var n;
    while ((n = walker.nextNode())) {
      var block = blockOf(n);
      if (lastBlock && block !== lastBlock) text += '\n';
      nodes.push({ node: n, start: text.length });
      text += n.nodeValue;
      lastBlock = block;
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

  /* ============ 划线与批注 ============
     服务端是唯一事实：Swift 每次增删改成功后全量重推列表，这里只按
     quote + 前后文重新定位并包 <mark>。所以既没有乐观更新，也没有回滚对账。 */
  var annotations = [];
  /* 行内标记字符（与服务端 project 的 mdInlineMarkers 一致）。
     不含 `[` / `]`：正文里字面的方括号在 DOM 里是真实字符，剥掉会让 `[内容]` 划不满、
     `[　]` 这类选区 quote 变空（真机踩过）。链接目标 `(url)` 的噪声照旧。 */
  var MD_MARKERS = /[*_`~|]/g;
  /* 行内标记字符集。从 MD_MARKERS.source 派生，杜绝两处字面量漂移；
     不能直接拿 MD_MARKERS 逐字符判断 —— 它带 g 标志，test() 的 lastIndex 有状态。 */
  var MD_MARKER_CHARS = MD_MARKERS.source.slice(1, -1).replace(/\\/g, '');
  /* 空白判定与服务端 project 的 unicode.IsSpace、fold() 的 \s 对齐。
     不用硬编码白名单：NBSP(\u00a0)、\u2028/\u2029 等粘贴文本里常见，
     漏一个就是又一条「haystack 和 needle 不在同一空间、划线静默画不出」。 */
  var WS_CHAR = /\s/;

  /* 文本投影，与服务端 project 对齐：丢 Markdown 行内标记与行首块标记，空白折叠成单空格。
     客户端的 haystack 是 DOM 文本（本来没有标记），但 quote / prefix / suffix 可能是
     服务端从源文截的快照（带标记），两边要能对上就得投到同一空间。 */
  function fold(s) {
    return String(s || '')
      .replace(/^[ \t]*[#>\-+*]+[ \t]+/gm, '')
      .replace(/^[ \t]*[0-9]+[.)][ \t]+/gm, '')
      .replace(MD_MARKERS, '')
      .replace(/\s+/g, ' ')
      .trim();
  }

  /* DOM 文本的投影版 + 「投影下标 → 拍平下标」映射。
     必须与服务端 project 剥同样多的东西：DOM 里也含字面量 `~`/`*`/`_` 等
     （如行内代码 `~/xhanCodes` 渲染后仍带 `~`），只折叠空白不剥标记的话，
     已投影的 quote（存库时 `~` 被 fold 掉）永远搜不到 → 划线静默画不出。 */
  function normalized() {
    var flat = flatten();
    var out = '';
    var map = [];
    var prevSpace = true;
    for (var i = 0; i < flat.text.length; i++) {
      var ch = flat.text.charAt(i);
      if (WS_CHAR.test(ch)) {
        if (!prevSpace) { out += ' '; map.push(i); prevSpace = true; }
        continue;
      }
      if (MD_MARKER_CHARS.indexOf(ch) >= 0) continue;   /* 丢行内标记，不占投影下标 */
      out += ch; map.push(i); prevSpace = false;
    }
    return { text: out, toFlat: map, flat: flat };
  }

  function commonTail(a, b) {
    var n = 0;
    while (n < a.length && n < b.length && a.charAt(a.length - 1 - n) === b.charAt(b.length - 1 - n)) n++;
    return n;
  }

  function commonHead(a, b) {
    var n = 0;
    while (n < a.length && n < b.length && a.charAt(n) === b.charAt(n)) n++;
    return n;
  }

  /* 客户端定位：只复刻服务端 rung 1 + rung 2（design §4.4）。
     rung 3 的夹逼不做 —— 服务端夹逼成功时已经把 quote 刷成新文本，整段查找即可命中。
     分不出时返回 null：不画、也不改任何状态（orphan 由服务端唯一裁定）。 */
  function locate(quote, prefix, suffix) {
    var needle = fold(quote);
    if (!needle) return null;
    var norm = normalized();
    var hay = norm.text;
    var hits = [];
    var at = hay.indexOf(needle);
    while (at !== -1) {
      hits.push(at);
      if (hits.length > 64) return null;   /* 复读机文档：服务端也放弃消歧，这里不猜 */
      at = hay.indexOf(needle, at + needle.length);
    }
    if (!hits.length) return null;

    var pre = fold(prefix);
    var suf = fold(suffix);
    var best = hits[0];
    if (hits.length > 1 && (pre.length || suf.length)) {
      var need = Math.floor((pre.length + suf.length) * 0.5);
      var bestScore = -1;
      for (var i = 0; i < hits.length; i++) {
        var h = hits[i];
        var score = 2 * commonTail(pre, hay.slice(0, h)) + 2 * commonHead(suf, hay.slice(h + needle.length));
        if (score > bestScore) { bestScore = score; best = h; }
      }
      if (bestScore < need) return null;
    }
    /* 返回拍平文本上的区间（不是 Range）：画的时候要逐文本节点切，
       拍平下标里本来就有「哪个节点、从第几个字」的信息，省掉一轮 Range 反解。 */
    return { flat: norm.flat, start: norm.toFlat[best], end: norm.toFlat[best + needle.length - 1] + 1 };
  }

  function annotationById(id) {
    for (var i = 0; i < annotations.length; i++) if (annotations[i].id === id) return annotations[i];
    return null;
  }

  /* 擦掉全部划线：气泡先删，再把每个 mark 拆回成它的子节点，最后归一化文本节点。
     重画走「全擦全画」而不是增量修补 —— 一条批注跨几个块、与别的批注重叠时，
     增量修补的状态机很难对，全擦全画只依赖服务端那一份。 */
  function unwrapAll() {
    var bubbles = content.querySelectorAll('span.ann-bubble');
    for (var i = 0; i < bubbles.length; i++) {
      if (bubbles[i].parentNode) bubbles[i].parentNode.removeChild(bubbles[i]);
    }
    var marks = content.querySelectorAll('mark.hl');
    for (var j = 0; j < marks.length; j++) {
      var m = marks[j];
      var parent = m.parentNode;
      if (!parent) continue;
      while (m.firstChild) parent.insertBefore(m.firstChild, m);
      parent.removeChild(m);
    }
    content.normalize();
  }

  /* 把命中的区间包成 mark：**逐文本节点切，一点块结构都不动**。
     一个 id 可以落在多个 mark 上（跨块），重叠时后画的 mark 自然嵌在前一个里面。

     别改回 `range.extractContents()` + `insertNode()` 那套（mockup 里的写法，在 Chrome 下
     看着正常）：跨块时它会给被部分包含的块级元素造克隆，再把克隆插到 range 起点 ——
     起点若在 `<h2>` 里就变成「标题里套列表」，表格行更是被整段塞进 `<mark>`，都是非法
     内容模型，WebKit 于是少画几个字或把整段行重组（真机踩过：划完字消失、bullet 被拆成
     两条、气泡另起一行）。而且那坨克隆不是 mark，`unwrapAll()` 擦不掉，坏结构会一直留着。
     逐文本节点包是唯一不碰结构的路子，标题/列表/表格/pre 全部天然安全。 */
  function wrapSpan(flat, start, end, ann) {
    var marks = [];
    for (var i = 0; i < flat.nodes.length; i++) {
      var item = flat.nodes[i];
      var len = item.node.nodeValue.length;
      var from = Math.max(0, start - item.start);
      var to = Math.min(len, end - item.start);
      if (to <= from) continue;            /* 不相交（含首尾擦边） */
      /* 块之间的换行是独立文本节点：包一层 mark 只是徒增段数，还会把气泡顶到空行上 */
      if (!item.node.nodeValue.slice(from, to).trim()) continue;

      var target = item.node;
      /* splitText 把后半截留给新节点：先切尾、再切头，偏移才不会互相影响 */
      if (to < len) target.splitText(to);
      if (from > 0) target = target.splitText(from);

      var m = document.createElement('mark');
      m.className = 'hl';
      m.dataset.id = ann.id;
      target.parentNode.insertBefore(m, target);
      m.appendChild(target);
      marks.push(m);
    }

    /* 小圆点只挂在最后一段，避免每段都挂一个 */
    if (ann.note && marks.length) {
      var b = document.createElement('span');
      b.className = 'ann-bubble ui';
      b.dataset.for = ann.id;
      b.title = '查看批注';
      marks[marks.length - 1].after(b);
    }
    return marks;
  }

  function redrawAnnotations() {
    unwrapAll();
    for (var i = 0; i < annotations.length; i++) {
      var a = annotations[i];
      if (a.orphaned) continue;      /* 失活的不画（P4 抽屉里可见、可重挂） */
      var hit = locate(a.quote, a.prefix, a.suffix);
      if (!hit) continue;            /* 客户端找不到就不画：不报错，也不动服务端状态 */
      wrapSpan(hit.flat, hit.start, hit.end, a);
    }
  }

  /* Swift 每次增删改成功后调用（全量替换） */
  function setAnnotations(json) {
    try { annotations = JSON.parse(json) || []; } catch (e) { annotations = []; }
    redrawAnnotations();
  }

  function scrollToAnnotation(id) {
    var marks = content.querySelectorAll('mark.hl[data-id="' + id + '"]');
    if (!marks.length) return false;
    marks[0].scrollIntoView({ behavior: 'smooth', block: 'center' });
    for (var i = 0; i < marks.length; i++) {
      marks[i].classList.remove('flash');
      void marks[i].offsetWidth;
      marks[i].classList.add('flash');
    }
    return true;
  }

  /* 连线用：某条划线第一段的视口矩形（viewport 坐标，左上原点）。
     取第一个 mark 的**首个行盒** —— 跨行划线连到最上面一行即可。
     找不到（失活/未画出）返回 null，Swift 侧就不画连线。 */
  function annotationRect(id) {
    var marks = content.querySelectorAll('mark.hl[data-id="' + id + '"]');
    if (!marks.length) return null;
    var rects = marks[0].getClientRects();
    var r = rects.length ? rects[0] : marks[0].getBoundingClientRect();
    return { x: r.left, y: r.top, w: r.width, h: r.height };
  }

  /* ---------- 选区 → 工具条 ---------- */
  var toolbar = document.getElementById('toolbar');
  var editor = document.getElementById('note-editor');
  var neQuote = document.getElementById('ne-quote');
  var neText = document.getElementById('ne-text');
  var neSave = document.getElementById('ne-save');
  var popover = document.getElementById('popover');
  var ctxmenu = document.getElementById('ctxmenu');
  var pendingRange = null;
  var editingId = null;
  var ctxTargetId = null;
  /* 非空 = 正在重新定位这一条（下一次选区当作新锚点） */
  var relocateId = null;
  var hideTimer = null;
  var hoverKey = '';

  /* DOM 位置 → 拍平文本下标。选区的 start/endContainer 通常是文本节点；
     整块被选中时才是元素容器 —— 那时取「容器内 child 之前的最后一个文本节点」。 */
  function flatOffset(flat, container, offset) {
    for (var i = 0; i < flat.nodes.length; i++) {
      if (flat.nodes[i].node === container) return flat.nodes[i].start + offset;
    }
    var child = container.childNodes ? (container.childNodes[offset] || null) : null;
    var best = null;
    for (var j = 0; j < flat.nodes.length; j++) {
      var item = flat.nodes[j];
      if (!container.contains(item.node)) continue;
      if (child && (child.compareDocumentPosition(item.node) & Node.DOCUMENT_POSITION_FOLLOWING)) break;
      best = item;
    }
    if (best) return best.start + best.node.nodeValue.length;
    for (var k = 0; k < flat.nodes.length; k++) {
      if (container.contains(flat.nodes[k].node)) return flat.nodes[k].start;
    }
    return 0;
  }

  /* 选区 → 服务端要的四元组。prefix/suffix 只当消歧提示（服务端优先用它而非快照），
     所以直接取拍平文本里紧邻选区的各 60 字。 */
  function selectionInfo(range) {
    var flat = flatten();
    var start = flatOffset(flat, range.startContainer, range.startOffset);
    var end = flatOffset(flat, range.endContainer, range.endOffset);
    if (end < start) { var t = start; start = end; end = t; }
    return {
      quote: fold(flat.text.slice(start, end)),
      prefix: flat.text.slice(Math.max(0, start - 60), start),
      suffix: flat.text.slice(end, end + 60)
    };
  }

  document.addEventListener('mouseup', function (e) {
    if (e.target.closest && e.target.closest('.ui')) return;
    setTimeout(function () {
      var sel = window.getSelection();
      if (!sel || sel.isCollapsed || !sel.rangeCount) { hideToolbar(); return; }
      var range = sel.getRangeAt(0);
      if (!content.contains(range.commonAncestorContainer)) { hideToolbar(); return; }
      if (!sel.toString().trim()) { hideToolbar(); return; }
      /* 重定位模式：选中的这段就是要重挂的新位置，不弹工具条 */
      if (relocateId) { relocateFromSelection(range); return; }
      pendingRange = range.cloneRange();
      showToolbar(range);
    }, 0);
  });

  /* ---------- 重新定位模式（抽屉里的「重新定位」） ----------
     只负责「这次选中是重挂，不是新建」这一件事：提示文案在 SwiftUI 抽屉那边，
     这里不弹横幅、不改样式。Esc 或选中一次即退出。 */
  function beginRelocate(id) {
    relocateId = id;
    hideAll();
    hidePopover();
    pendingRange = null;
    clearSel();
  }

  function cancelRelocate() { relocateId = null; }

  function relocateFromSelection(range) {
    if (!relocateId) return;
    var id = relocateId;
    relocateId = null;
    var info = selectionInfo(range);
    clearSel(); hideAll();
    if (!info.quote) { post({ type: 'relocateCancel' }); return; }
    post({ type: 'relocate', id: id, quote: info.quote, prefix: info.prefix, suffix: info.suffix });
  }

  document.addEventListener('keydown', function (e) {
    if (e.key !== 'Escape' || !relocateId) return;
    cancelRelocate();
    clearSel();
    post({ type: 'relocateCancel' });
  });

  document.addEventListener('mousedown', function (e) {
    if (e.target.closest && e.target.closest('.ui')) return;
    hidePopover();
    if (!(e.target.closest && e.target.closest('mark.hl, .ann-bubble'))) hideCtx();
  });

  /* capture 阶段的 scroll 监听连**后代元素**的滚动也收得到（scroll 不冒泡，但 capture 会命中）。
     长批注的 hover 浮层自己是滚动容器，它内滚时 e.target 就是浮层 —— 那种不算「页面滚动」，
     不然用户一滚浮层就被关掉，紧接着滚轮落到正文上，看着像「浮层滚不动、只有文档在滚」 */
  window.addEventListener('scroll', function (e) {
    if (popover.contains(e.target)) return;
    hideAll(); hidePopover();
    post({ type: 'scroll' });
  }, true);

  toolbar.addEventListener('mousedown', function (e) { e.preventDefault(); });

  function showToolbar(range) {
    var rects = range.getClientRects();
    var r = rects.length ? rects[rects.length - 1] : range.getBoundingClientRect();
    toolbar.classList.add('open');
    var tw = toolbar.offsetWidth, th = toolbar.offsetHeight;
    var left = r.right - tw;
    var top = r.top - th - 9;
    if (top < 8) top = r.bottom + 9;
    left = Math.max(10, Math.min(left, window.innerWidth - tw - 10));
    toolbar.style.left = left + 'px';
    toolbar.style.top = top + 'px';
  }

  toolbar.addEventListener('click', function (e) {
    var btn = e.target.closest('button');
    if (!btn) return;
    var act = btn.dataset.act;
    if (act === 'hl') { createFromSelection(''); return; }
    if (act === 'note') { openEditor(null); return; }
    if (act === 'copy') {
      if (pendingRange) post({ type: 'copy', text: pendingRange.toString() });
      clearSel(); hideAll();
      return;
    }
  });

  /* 服务端锚定成功后会回推整份列表，划线由 redrawAnnotations 定位画出 —— 
     这里只负责把选区告诉 Swift（不在原地包 mark，免得客户端与服务端各画一份）。 */
  function createFromSelection(note) {
    if (!pendingRange) return;
    var info = selectionInfo(pendingRange);
    if (!info.quote) {
      /* 选区折成空串（纯空白 / 纯标记）→ 没有可锚定的文字。别静默什么都不做。 */
      post({ type: 'toast', text: '选中的内容没有可锚定的文字，连旁边的字一起选' });
      clearSel(); hideAll();
      return;
    }
    post({ type: 'create', quote: info.quote, prefix: info.prefix, suffix: info.suffix, note: note });
    clearSel(); hideAll();
  }

  /* ---------- 批注输入浮层 ---------- */
  function updateSaveLabel() {
    neSave.textContent = neText.value.trim() ? '保存批注' : '保存为纯划线';
  }

  function anchorRect(id) {
    if (id) {
      var m = content.querySelector('mark.hl[data-id="' + id + '"]');
      if (m) return m.getBoundingClientRect();
    }
    if (pendingRange) {
      var rects = pendingRange.getClientRects();
      return rects.length ? rects[rects.length - 1] : pendingRange.getBoundingClientRect();
    }
    return null;
  }

  function openEditor(id) {
    editingId = id;
    var a = id ? annotationById(id) : null;
    if (id) {
      neQuote.textContent = a ? a.quote : '';
      neText.value = a ? a.note : '';
    } else {
      neQuote.textContent = pendingRange ? fold(pendingRange.toString()).slice(0, 160) : '';
      neText.value = '';
    }
    updateSaveLabel();

    editor.classList.add('open');
    var anchor = anchorRect(id);
    var w = editor.offsetWidth, h = editor.offsetHeight;
    var left = anchor ? anchor.left : 40;
    var top = anchor ? anchor.bottom + 10 : 140;
    left = Math.max(12, Math.min(left, window.innerWidth - w - 12));
    if (top + h > window.innerHeight - 16) top = Math.max(12, (anchor ? anchor.top : 200) - h - 10);
    editor.style.left = left + 'px';
    editor.style.top = top + 'px';
    neText.focus();
  }

  neSave.addEventListener('click', function () {
    var v = neText.value.trim();
    editor.classList.remove('open');
    if (editingId) {
      post({ type: 'update', id: editingId, note: v });
      editingId = null;
    } else {
      createFromSelection(v);
    }
    clearSel(); hideAll();
  });

  document.getElementById('ne-cancel').addEventListener('click', function () {
    editor.classList.remove('open');
    editingId = null;
    clearSel(); hideAll();
  });

  neText.addEventListener('input', updateSaveLabel);
  neText.addEventListener('keydown', function (e) {
    /* 去掉 Esc 关浮层：误触会丢正在写的批注。退出只剩「取消」按钮 / 保存（⌘↵）。
       别加回来（曾经 §8.4 写死「Esc 取消」，用户明确要求去掉）。 */
    if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') { e.preventDefault(); neSave.click(); }
  });

  /* ---------- hover：划线 / 气泡 → popover（重叠时列出全部） ---------- */
  content.addEventListener('mousemove', function (e) {
    var els = document.elementsFromPoint(e.clientX, e.clientY);
    var ids = [];
    for (var i = 0; i < els.length; i++) {
      var el = els[i];
      if (!el.matches || !(el.matches('mark.hl') || el.matches('.ann-bubble'))) continue;
      var id = el.dataset.id || el.dataset.for;
      if (id && ids.indexOf(id) === -1) ids.push(id);
    }
    var items = [];
    for (var j = 0; j < ids.length; j++) {
      var a = annotationById(ids[j]);
      if (a && a.note) items.push(a);
    }
    var key = items.map(function (a) { return a.id; }).join(',');
    if (key === hoverKey) return;
    hoverKey = key;
    if (!items.length) { scheduleHidePopover(); return; }
    showPopover(items, e.clientX, e.clientY);
  });

  content.addEventListener('mouseleave', function () { hoverKey = ''; scheduleHidePopover(); });
  popover.addEventListener('mouseenter', function () { clearTimeout(hideTimer); });
  popover.addEventListener('mouseleave', function () { scheduleHidePopover(); });

  function scheduleHidePopover() {
    clearTimeout(hideTimer);
    hideTimer = setTimeout(function () { hidePopover(); }, 140);
  }

  /* 任何隐藏路径都要复位 hoverKey：漏了的话，光标还在正文里时再 hover 同一处会走
     `key === hoverKey` 的提前返回，浮层再也开不出来（曾因页面 scroll 把浮层关掉而卡住） */
  function hidePopover() { hoverKey = ''; popover.classList.remove('open'); }

  function esc(s) {
    return String(s || '').replace(/[&<>"]/g, function (c) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c];
    });
  }

  function timeStr(iso) {
    var d = new Date(iso);
    if (isNaN(d.getTime())) return '';
    var p = function (n) { return (n < 10 ? '0' : '') + n; };
    return p(d.getMonth() + 1) + '-' + p(d.getDate()) + ' ' + p(d.getHours()) + ':' + p(d.getMinutes());
  }

  function showPopover(items, x, y) {
    clearTimeout(hideTimer);
    var multi = items.length > 1;
    var html = '';
    for (var i = 0; i < items.length; i++) {
      var a = items[i];
      html += '<div class="p-item">'
        + '<div class="p-quote">' + esc(a.quote) + '</div>'
        + '<div class="p-note">' + esc(a.note) + '</div>'
        + '<div class="p-foot">' + (multi ? '<span class="p-tag">重叠划线</span>' : '')
        + '<span>' + timeStr(a.created_at) + '</span></div>'
        + '</div>';
    }
    popover.innerHTML = html;
    popover.classList.add('open');
    var w = popover.offsetWidth, h = popover.offsetHeight;
    var left = Math.max(10, Math.min(x + 14, window.innerWidth - w - 10));
    var top = y + 18;
    if (top + h > window.innerHeight - 16) top = Math.max(10, y - h - 14);
    popover.style.left = left + 'px';
    popover.style.top = top + 'px';
  }

  /* ---------- 点击划线 → 菜单 ---------- */
  content.addEventListener('click', function (e) {
    var sel = window.getSelection();
    if (sel && !sel.isCollapsed && sel.toString().trim()) return;   /* 正在选词 */
    var t = e.target.closest ? e.target.closest('mark.hl, .ann-bubble') : null;
    if (!t) { hideCtx(); return; }
    ctxTargetId = t.dataset.id || t.dataset.for;
    var a = annotationById(ctxTargetId);
    if (!a) { hideCtx(); return; }   /* 临时闪烁层没有对应批注，别弹菜单 */
    document.getElementById('cact-edit-label').textContent = (a && a.note) ? '编辑批注' : '添加批注';
    ctxmenu.classList.add('open');
    var w = ctxmenu.offsetWidth, h = ctxmenu.offsetHeight;
    var left = Math.max(10, Math.min(e.clientX, window.innerWidth - w - 10));
    var top = e.clientY + 6;
    if (top + h > window.innerHeight - 16) top = Math.max(10, e.clientY - h - 6);
    ctxmenu.style.left = left + 'px';
    ctxmenu.style.top = top + 'px';
    hidePopover();
  });

  ctxmenu.addEventListener('click', function (e) {
    var btn = e.target.closest('button');
    if (!btn) return;
    var act = btn.dataset.cact;
    var id = ctxTargetId;
    var a = annotationById(id);
    hideCtx();
    if (act === 'edit') { openEditor(id); return; }
    if (act === 'copy') { post({ type: 'copy', text: a ? a.quote : '' }); return; }
    if (act === 'del') { post({ type: 'delete', id: id }); }
  });

  function hideCtx() { ctxmenu.classList.remove('open'); }
  function clearSel() { var s = window.getSelection(); if (s) s.removeAllRanges(); }
  function hideToolbar() { toolbar.classList.remove('open'); }
  function hideAll() { hideToolbar(); hideCtx(); }

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
    /* 硬换行 <br>（源文「行尾两空格 + 换行」）：服务端投影里它是一个空格，而 DOM 文本里
       没有任何字符（<br> 是元素），flatten 也不会在它两侧补分隔符 —— 跨硬换行的选区就会
       少一个空格、永远锚不上。补一个 '\n' 文本节点（行内空白，渲染无影响），折成空格后两边对齐。 */
    [].forEach.call(content.querySelectorAll('br'), function (br) {
      br.parentNode.insertBefore(document.createTextNode('\n'), br.nextSibling);
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
    restoreScrollY: restoreScrollY,
    setAnnotations: setAnnotations,
    scrollToAnnotation: scrollToAnnotation,
    annotationRect: annotationRect,
    beginRelocate: beginRelocate,
    cancelRelocate: cancelRelocate
  };
})();
</script>
</body>
</html>
"""#
#endif
