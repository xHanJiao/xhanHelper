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
    /// 本文件的划线与批注（批注面板也用这份；正文里画什么由 `reader.setAnnotations` 决定）
    @Published private(set) var annotations: [DocumentAnnotation] = []

    // MARK: 讨论线程（批注下挂的 AI 回复 / 追问）
    /// 已加载的线程缓存（key = 批注 id）。未加载过的键不存在 = 需要拉取。
    @Published private(set) var repliesByAnnotation: [String: [AnnotationReply]] = [:]
    /// 展开了线程的批注
    @Published private(set) var expandedAnnotationIDs: Set<String> = []
    /// 正在拉取线程
    @Published private(set) var loadingReplyIDs: Set<String> = []
    /// 正在生成回复（spinner）
    @Published private(set) var busyReplyIDs: Set<String> = []

    // MARK: 定位（正文划线 → 右侧批注面板）
    /// 左键点划线/气泡时投递的定位请求；面板观察后滚动 + 闪烁。`tick` 保证连点同一条也重新触发。
    @Published private(set) var focusRequest: AnnotationFocus?

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

    /// 正文疑似过旧时自增，宿主观察到就拉一次新正文。用 tick 不用闭包：
    /// controller 在 `DocumentFullView.init` 里就建好了，那会儿还拿不到 viewModel。
    @Published private(set) var staleContentTick = 0
    /// 自动拉新正文的闸门（同一份屏幕正文只拉一次 / 正文换了重新开口 / 提示只给一次）
    private var staleGate = DocumentStaleGate()

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

    /// 正文可能已旧（服务端手里的正文跟你屏幕上不是一份）：先自动拉一次新正文，
    /// 拉完还画不出来就提示一次。触发点：① 创建时服务端靠夹逼改写 quote（`quoteRewritten`）
    /// ② 重画时"正文里压根找不到这句"（`stale`）。文档是 agent 改的，这两种不一致会经常发生，
    /// 不处理就是"服务端一切正常、正文里就是没线"。
    private func contentLooksStale(undrawn: Int) {
        if staleGate.noteStale() {
            LogBuffer.shared.info("DocumentReader: 正文疑似过旧（\(undrawn) 条划线画不出），拉一次新正文")
            staleContentTick += 1
            return
        }
        guard undrawn > 0, staleGate.noteStillUndrawn() else { return }
        ToastCenter.shared.show("正文可能已更新，\(undrawn) 条划线暂时画不出来")
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

    /// 建一条划线批注。返回落库后的批注（失败 nil）——「让 AI 回答」要拿 id 接着追问。
    private func create(quote: String, prefix: String, suffix: String, note: String) async -> DocumentAnnotation? {
        do {
            let a = try await api.createAnnotation(
                documentId: documentID, quote: quote, note: note, prefix: prefix, suffix: suffix
            )
            annotations.append(a)
            annotations.sort(by: Self.order)
            pushAnnotations()
            if a.orphaned {
                ToastCenter.shared.show("这段文字已不在正文里，记为「已失活」")
            } else if a.quoteRewritten {
                // 服务端靠前后文夹逼才锚上 → 它手里的正文跟你屏幕上不是一份（文档被改过）。
                // 这条的 quote 已经是新正文的文本，拉一次新正文它才画得出来。
                contentLooksStale(undrawn: 0)
            }
            return a
        } catch {
            LogBuffer.shared.error("DocumentReader 创建划线批注失败: \(error)")
            ToastCenter.shared.show("添加失败：\(error.localizedDescription)")
            return nil
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

    /// 收起所有已展开的线程（头部「收起全部」按钮）。
    /// 生成中的线程不收 —— 与卡片折叠行的 `.disabled(busy)` 守卫保持一致，
    /// 否则守着 spinner 的线程被折叠、`defer` 清掉 busy 后回复就没人看了。
    func collapseAllThreads() {
        expandedAnnotationIDs.subtract(busyReplyIDs)
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

        // 乐观回显：服务端是「生成成功才把 [追问, 回复] 一起落库」的同步长任务（最长 120s），
        // 不先本地插入的话，用户会看到自己发的内容和 AI 回复同一刻才冒出来。响应回来后用落库版替换。
        var pendingID: String?
        if let content, !content.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            let pending = AnnotationReply(
                id: "pending-\(UUID().uuidString)",
                annotationId: annotationId,
                role: "user",
                content: content,
                source: "followup",
                createdAt: ISO8601DateFormatter().string(from: Date())
            )
            pendingID = pending.id
            repliesByAnnotation[annotationId, default: []].append(pending)
        }

        do {
            let resp = try await api.replyToAnnotation(id: annotationId, content: content)
            var thread = repliesByAnnotation[annotationId] ?? []
            if let pendingID { thread.removeAll { $0.id == pendingID } }
            thread.append(contentsOf: resp.added)
            repliesByAnnotation[annotationId] = thread
            if let i = annotations.firstIndex(where: { $0.id == annotationId }) {
                annotations[i].replyCount = resp.replyCount
                annotations[i].lastReply = resp.added.last ?? annotations[i].lastReply
            }
        } catch {
            if let pendingID { repliesByAnnotation[annotationId]?.removeAll { $0.id == pendingID } }
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
        // 屏幕上的正文换了（服务端改写 / 手动刷新 / 上面那次自动校新）→ 重新给"自动拉新"开口：
        // 上一轮画不出的划线，这一轮可能就画得出了（也可能又发现新的不一致）。
        staleGate.screenChanged()
        Task { await loadAnnotations() }
    }

    /// 批注面板失活条目的「删除」
    func deleteAnnotation(id: String) {
        Task { await remove(id: id) }
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

        case "annotationsMissed":
            // 服务端说没失活却画不出来。stale>0 = 正文里压根没这句 → 屏幕上的正文是旧的；
            // stale=0 = 多命中分不出，跟正文新旧无关（刷新救不了），留痕即可。
            let undrawn = message["count"] as? Int ?? 0
            if (message["stale"] as? Int ?? 0) > 0 {
                contentLooksStale(undrawn: undrawn)
            } else {
                LogBuffer.shared.info("DocumentReader: \(undrawn) 条划线多命中分不出，未画出")
            }

        case "create":
            let quote = message["quote"] as? String ?? ""
            guard !quote.isEmpty else { return }
            let note = message["note"] as? String ?? ""
            let ask = message["ask"] as? Bool ?? false
            Task {
                guard let a = await create(
                    quote: quote,
                    prefix: message["prefix"] as? String ?? "",
                    suffix: message["suffix"] as? String ?? "",
                    note: note
                ) else { return }
                // 浮层里点了「让 AI 回答」：接着让 AI 回一条（与右侧卡片「让 AI 回复」同一个 reply 路径）。
                // 空 note（纯划线）不给 AI 入口、失活批注不给追问 —— 两条都是右侧面板的规则，
                // JS 那侧挡过了，这里按同一份 message 再挡一刀（服务端不拦，别指望它）。
                guard ask,
                      !a.orphaned,
                      !note.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return }
                // 先投定位请求：面板若被收起，DocumentListView 的 onChange 会展开，卡片随即显示「正在生成…」
                focusRequest = AnnotationFocus(id: a.id, tick: (focusRequest?.tick ?? 0) + 1)
                await reply(annotationId: a.id, content: nil)
            }

        case "update":
            guard let id = message["id"] as? String else { return }
            Task { await update(id: id, note: message["note"] as? String ?? "") }

        case "delete":
            guard let id = message["id"] as? String else { return }
            Task { await remove(id: id) }

        case "scroll":
            // 正文滚动后划线位置变了，正在连线就重取一次
            if let id = connectorID { measureConnector(id: id) }

        case "focus":
            // 左键点划线/气泡：请求右侧面板定位到该条批注（面板滚动 + 闪烁）
            guard let id = message["id"] as? String, !id.isEmpty else { return }
            focusRequest = AnnotationFocus(id: id, tick: (focusRequest?.tick ?? 0) + 1)

        case "copy":
            // 剪贴板交给原生：`navigator.clipboard` 在 loadHTMLString 出来的页面里不是安全上下文，
            // 能不能用全看运气
            let text = message["text"] as? String ?? ""
            guard !text.isEmpty else { return }
            let pb = NSPasteboard.general
            pb.clearContents()
            pb.setString(text, forType: .string)
            ToastCenter.shared.show("已复制")

        case "image":
            // 正文里的图（mermaid 预渲染 PNG / 文档配图）点击 → 原生大图浮层。
            // src 已被浏览器解析成绝对地址：配图是 http URL，mermaid 是 data URI。
            guard let src = message["src"] as? String, !src.isEmpty else { return }
            LightboxCenter.shared.present {
                DocumentImageLightbox(src: src)
            }

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
        // 用默认持久 store 吃 WebKit 磁盘缓存：文档图是 `immutable` + 一年 max-age 的公开
        // 资源，换篇/重进/重启 app 都该直接命中。曾因 nginx 漏配 `/images/` 返 SPA HTML 被
        // 缓存成裂图而改成非持久 store（docs/bugs/031），根因已修，故恢复。
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
        /// NSWorkspace，弹「未设定用来打开URL…的应用程序」）。拦下来交宿主打开；其余只放行
        /// http/https/about/mailto/tel，未知协议（站点探测本机 App 的 `bitbrowser://` 等）一律吞掉。
        nonisolated func webView(_ webView: WKWebView, decidePolicyFor navigationAction: WKNavigationAction,
                                 decisionHandler: @escaping (WKNavigationActionPolicy) -> Void) {
            guard let url = navigationAction.request.url, let scheme = url.scheme?.lowercased() else {
                decisionHandler(.allow)
                return
            }
            switch scheme {
            case "xhanhelper":
                decisionHandler(.cancel)
                guard let id = DocumentSelectionCoordinator.documentID(from: url) else { return }
                Task { @MainActor in DocumentSelectionCoordinator.shared.openDocument(id: id) }
            case "http", "https":
                // 外链不在阅读器里打开：`reader` bridge 只该服务本文档，放行导航等于把它交给任意网站。
                // 初始 loadHTMLString 的导航类型是 .other（baseURL 本身），不能拦；只处理用户点的链接。
                guard navigationAction.navigationType == .linkActivated else {
                    decisionHandler(.allow)
                    return
                }
                if Self.isSameDocument(url, current: webView.url) {
                    decisionHandler(.allow)   // 同文档锚点（#foo）
                } else {
                    decisionHandler(.cancel)
                    NSWorkspace.shared.open(url)
                }
            case "about", "mailto", "tel":
                decisionHandler(.allow)
            default:
                decisionHandler(.cancel)
            }
        }

        /// 同文档锚点判断（去 fragment 后与当前页相同）。
        private static func isSameDocument(_ url: URL, current: URL?) -> Bool {
            guard let current, url.fragment != nil else { return false }
            func withoutFragment(_ u: URL) -> Substring {
                u.absoluteString.split(separator: "#", maxSplits: 1, omittingEmptySubsequences: false)[0]
            }
            return withoutFragment(url) == withoutFragment(current)
        }
    }
}

// MARK: - HTML 组装

/// 屏幕阅读版 HTML：与 `DocumentPDFExporter` 同一套骨架（marked.min.js + 图表占位替换），
/// 但 CSS 换成阅读版 —— 不加 A4 的 `595pt` / `12pt` 打印尺寸，改自适应列宽
/// （`clamp(728px, 68vw, 1100px)`：窄栏维持 728，宽窗按 68% 视口长到 1100 封顶）+ 可调字号，
/// 并且不铺任何底色（外层是卡片 material）。
enum ReaderHTML {

    /// 书阅读器的初始排版（`buildBook` 注入，之后由 `reader.setTypo` 改）
    struct BookTypography: Equatable {
        var fontSize: CGFloat = 17
        var lineHeight: Double = 1.78
        /// 段间距（em）
        var paragraphGap: Double = 0.55
        /// 正文列宽/栏宽
        var measure: CGFloat = 728
        var fontFamily: String = #"-apple-system, "PingFang SC", "Helvetica Neue", sans-serif"#
        /// `scroll` / `paged`
        var readingMode: String = "scroll"

        var json: String {
            "{fs:\(String(format: "%.2f", fontSize)),line:\(lineHeight),para:\(paragraphGap),"
            + "measure:\(String(format: "%.1f", measure)),font:\(MarkdownWebUtils.jsString(fontFamily)),"
            + "mode:\(MarkdownWebUtils.jsString(readingMode))}"
        }
    }

    /// mermaid 渲染结果缓存：key = 主题 + 源码。
    /// 换篇会重建 WebView 并重跑 `build`（外层 `.id(doc.id)`），没缓存就每次从零烤
    /// （解析 + 布局 + 位图 + PNG 编码 + base64，全在主线程同步）—— 图多时直接卡出彩球。
    /// NSCache 自带线程安全与淘汰（countLimit），不必自己管。
    private static let mermaidCache: NSCache<NSString, NSString> = {
        let cache = NSCache<NSString, NSString>()
        cache.countLimit = 64
        return cache
    }()

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
        let headerHTML = """
        <div class="doc-title">\(MarkdownWebUtils.escapeHTML(document.title))</div>
        <div class="doc-tags">\(tagsHTML)</div>
        <hr class="doc-rule">
        """

        // 每次加载换一个 CSP nonce：只放行本模板的两段脚本，正文里注入的 <script>/内联事件一律不执行。
        let nonce = UUID().uuidString.replacingOccurrences(of: "-", with: "")

        return template
            .replacingOccurrences(of: "__NONCE__", with: nonce)
            .replacingOccurrences(of: "__MODE__", with: "doc")
            .replacingOccurrences(of: "__HEADER_HTML__", with: headerHTML)
            .replacingOccurrences(of: "__BOOK_JSON__", with: "null")
            .replacingOccurrences(of: "__FS__", with: String(format: "%.2f", fontSize))
            .replacingOccurrences(of: "__MARKED_JS__", with: markedJS)
            .replacingOccurrences(of: "__MD_JSON__", with: MarkdownWebUtils.jsString(mdParts.joined(separator: "\n\n")))
            .replacingOccurrences(of: "__IMAGES_JSON__", with: imagesJSON)
    }

    /// 书阅读器的 HTML：同一份模板，书模式不渲染 markdown（正文由 `setChapter` 灌进来）。
    static func buildBook(typography: BookTypography) -> String {
        let nonce = UUID().uuidString.replacingOccurrences(of: "-", with: "")
        return template
            .replacingOccurrences(of: "__NONCE__", with: nonce)
            .replacingOccurrences(of: "__MODE__", with: "book")
            .replacingOccurrences(of: "__HEADER_HTML__", with: "")
            .replacingOccurrences(of: "__BOOK_JSON__", with: typography.json)
            .replacingOccurrences(of: "__FS__", with: String(format: "%.2f", typography.fontSize))
            .replacingOccurrences(of: "__MARKED_JS__", with: "")
            .replacingOccurrences(of: "__MD_JSON__", with: "null")
            .replacingOccurrences(of: "__IMAGES_JSON__", with: "[]")
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
        let cacheKey = "\(dark ? "dark" : "light")\u{1}\(code)" as NSString
        if let cached = mermaidCache.object(forKey: cacheKey) { return cached as String }

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
        let uri = "data:image/png;base64," + out.base64EncodedString()
        mermaidCache.setObject(uri as NSString, forKey: cacheKey)
        return uri
    }

    private static func errorHTML(_ message: String) -> String {
        "<!DOCTYPE html><html><body style=\"background:transparent;font-family:-apple-system;color:#8e8e93;padding:40px\">\(MarkdownWebUtils.escapeHTML(message))</body></html>"
    }
}

// MARK: - 大图浮层

/// 文档正文里点图放大：mermaid 是 data URI（直接解），配图是绝对 http URL（拉一次）。
/// 加载好复用 `AttachmentPreviewView`，与聊天图放大同款。
struct DocumentImageLightbox: View {
    let src: String
    @State private var image: NSImage?
    @State private var failed = false

    /// 解码后的图按 src 缓存：关掉再开、切篇再回都直接命中，不再走网络与解码。
    private static let cache = NSCache<NSString, NSImage>()
    /// 取字节专用 session：WebKit 的磁盘缓存跟 URLSession 不共享，灯箱得自带一套；
    /// 默认 `URLCache.shared` 只有 512KB/20MB，大图基本存不下。图 immutable，落盘安全。
    private static let session: URLSession = {
        let config = URLSessionConfiguration.default
        config.urlCache = URLCache(memoryCapacity: 64 << 20, diskCapacity: 512 << 20,
                                   diskPath: "doc-images")
        return URLSession(configuration: config)
    }()

    var body: some View {
        Group {
            if let image {
                AttachmentPreviewView(image: image) { LightboxCenter.shared.dismiss() }
            } else {
                VStack(spacing: 12) {
                    if failed {
                        Text("图片加载失败").foregroundStyle(.secondary)
                    } else {
                        ProgressView()
                    }
                }
                .frame(minWidth: 480, minHeight: 360)
            }
        }
        .task { await load() }
    }

    private func load() async {
        if let cached = Self.cache.object(forKey: src as NSString) {
            image = cached
            return
        }
        let data: Data?
        if let inline = Self.dataURIData(src) {
            data = inline
        } else {
            guard let url = URL(string: src) else { failed = true; return }
            do {
                data = try await Self.session.data(from: url).0
            } catch {
                LogBuffer.shared.error("文档配图加载失败: \(error)")
                failed = true
                return
            }
        }
        guard let data, let img = NSImage(data: data) else { failed = true; return }
        Self.cache.setObject(img, forKey: src as NSString)
        image = img
    }

    /// `data:image/...;base64,xxxx` → 原始字节；非 data URI 返回 nil。
    private static func dataURIData(_ s: String) -> Data? {
        guard s.hasPrefix("data:"),
              let comma = s.firstIndex(of: ","),
              s[..<comma].contains("base64") else { return nil }
        return Data(base64Encoded: String(s[s.index(after: comma)...]))
    }
}

// MARK: - 模板

private let template = #"""
<!DOCTYPE html>
<html lang="zh">
<head>
<meta charset="utf-8">
<!-- 内容信任边界：正文可能来自 agent 抓取的网页（含提示注入），markdown 里的 HTML
     是**不可信输入**。nonce 只发给本模板的两段脚本，注入的 <script> 与内联事件
     处理器都执行不了；外链图片/脚本/连接也一并收紧。 -->
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; img-src 'self' data: blob:; style-src 'unsafe-inline'; script-src 'nonce-__NONCE__'; font-src data:; media-src 'self' data: blob:; frame-src 'none'; object-src 'none'; base-uri 'none'; form-action 'none'">
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
#content img { max-width: 100%; height: auto; cursor: zoom-in; }

/* 外链图片被 CSP 挡住（img-src 只放行本站 + data/blob）：换成可点击占位，别留裂图 */
.blocked-img {
  display: inline-block; max-width: 100%; padding: 8px 12px; border: 1px dashed var(--border);
  border-radius: 8px; color: var(--muted); font-size: calc(var(--fs) * 0.72);
  text-decoration: none; overflow-wrap: anywhere; user-select: none;
}
.blocked-img:hover { border-color: var(--accent); color: var(--accent); }

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
     线色取主题绿 appAccent（浅 #2F7D63）；hover 压深一档给交互反馈。 */
  --hl: rgba(47, 125, 99, 1);
  --hl-hover: rgba(36, 95, 75, 1);
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

/* 注释卡片：doc 模式不显示；book 模式只在 .open 时显示 */
#note-scrim, #note-pop { display: none; }

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
  color: var(--text); font-family: inherit; font-size: calc(var(--fs) * 0.72);
  transition: background .13s;
}
.floating button:hover { background: var(--accent-soft); color: var(--accent); }
.floating button svg { width: calc(var(--fs) * 0.8); height: calc(var(--fs) * 0.8); }
.floating .sep { width: 1px; background: var(--border-soft); margin: 5px 3px; }

.note-editor {
  position: fixed; z-index: 70; display: none; width: 410px;
  /* 窄正文（docReaderMinWidth 可到 260）下别越出 WebView 被裁 —— 定位钳位只保左缘 */
  max-width: calc(100vw - 24px);
  background: var(--surface); border: 1px solid var(--border);
  border-radius: 12px; box-shadow: var(--shadow);
  padding: 12px;
}
.note-editor.open { display: block; }
.note-editor .quote {
  font-size: calc(var(--fs) * 0.82); line-height: 1.55; color: var(--muted);
  border-left: 2.5px solid var(--hl-hover);
  padding: 1px 0 1px 9px; margin-bottom: 9px;
  max-height: 62px; overflow: hidden;
}
.note-editor textarea {
  width: 100%; height: 84px; resize: none;
  border: 1px solid var(--border); border-radius: 8px;
  background: var(--surface-2); color: var(--text);
  padding: 8px 10px; font-size: calc(var(--fs) * 0.90); line-height: 1.6;
  font-family: inherit; outline: none;
}
.note-editor textarea:focus { border-color: var(--accent); background: var(--surface); }
.note-editor .foot { display: flex; align-items: center; gap: 8px; margin-top: 9px; flex-wrap: wrap; }
.note-editor .foot .spacer { flex: 1; }
/* 三个按钮包一组：能整组落到第二行（而不是只把最后一个挤下去），行内右对齐；
   btn 不收缩 —— 挤窄时靠组内换行，不让文字在按钮里逐字折。 */
.note-editor .foot .actions { display: flex; flex-wrap: wrap; justify-content: flex-end; gap: 8px; margin-left: auto; }
.note-editor .foot .actions .btn { flex: 0 0 auto; }
.note-editor .hint { font-size: calc(var(--fs) * 0.62); color: var(--faint); }
.btn {
  border: 1px solid var(--border); background: var(--surface-2); color: var(--text);
  font-family: inherit; font-size: calc(var(--fs) * 0.82); padding: 5px 12px; border-radius: 7px; cursor: pointer;
  transition: background .13s;
}
.btn:hover { background: var(--border-soft); }
.btn.primary { background: var(--accent); border-color: var(--accent); color: var(--on-accent); font-weight: 500; }
.btn.primary:hover { filter: brightness(1.08); }
/* 没写批注内容时「让 AI 回答」禁用；.btn:hover 对 disabled 也命中，必须一起挡掉 */
.btn:disabled { opacity: .45; cursor: default; }
.btn:disabled:hover { background: var(--surface-2); filter: none; }

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
  border: 0; background: none; font-family: inherit; font-size: calc(var(--fs) * 0.88);
  color: var(--text); padding: 6px 9px; border-radius: 6px; cursor: pointer; text-align: left;
}
.ctxmenu button:hover { background: var(--accent-soft); color: var(--accent); }
.ctxmenu button svg { width: calc(var(--fs) * 0.7); height: calc(var(--fs) * 0.7); opacity: .8; }
.ctxmenu button.danger:hover { background: color-mix(in srgb, var(--danger) 12%, transparent); color: var(--danger); }
.ctxmenu hr { border: 0; border-top: 1px solid var(--border-soft); margin: 4px 6px; }

/* ============ 书阅读器（BOOK 模式） ============
   正文是原书 XHTML，排版由这里接管（书自带的 <style>/<link> 在 JS 里剥掉）：
   字号/行距/段距/列宽/字体全部走 CSS 变量，改一个变量就整篇重排。
   doc 模式不受影响（这些规则都挂在 html.book 下）。 */
html.book { --measure: 728px; --line: 1.78; --para: 0.55em; }
html.book .page { max-width: none; padding: 0; }
html.book #content {
  /* 列宽是「正文列」宽度：左右各留 28px 呼吸位，面板窄于列宽时跟着缩 */
  max-width: calc(var(--measure) + 56px); margin: 0 auto; padding: 26px 28px 64px;
  font-family: var(--book-font, -apple-system, "PingFang SC", "Helvetica Neue", sans-serif);
  line-height: var(--line);
}
html.book #content p { margin: var(--para) 0; }
html.book #content h1 { font-size: calc(var(--fs) * 1.45); }
html.book #content h2 { font-size: calc(var(--fs) * 1.24); border-bottom: 0; padding-bottom: 0; }
html.book #content h3 { font-size: calc(var(--fs) * 1.1); }
html.book #content h4, html.book #content h5, html.book #content h6 { font-size: var(--fs); }
/* 图片默认按行内处理（注标、行内符号、场景分隔符）；只有「独占一段」的才当块级图 */
html.book #content img, html.book #content svg { max-width: 100%; height: auto; }
html.book #content img.fig, html.book #content svg.fig,
html.book #content figure > img, html.book #content figure > svg {
  display: block; margin: 18px auto;
}
html.book #content a { color: inherit; border-bottom: 0; text-decoration: none; }
html.book #content a.ext-link {
  color: var(--accent);
  border-bottom: 1px dotted color-mix(in srgb, var(--accent) 45%, transparent);
}
html.book #content a.fn-ref { color: var(--accent); white-space: nowrap; }
html.book #content a.fn-ref img { width: 1em; height: 1em; vertical-align: -0.12em; margin: 0 .08em; }
html.book #content .book-note { display: none !important; }
html.book #content .broken-img {
  display: inline-block; padding: 2px 6px; color: var(--muted);
  border: 1px dashed var(--border); border-radius: 6px;
  font-size: calc(var(--fs) * .72);
}
html.book #content ruby rt { font-size: .5em; line-height: 1; color: var(--muted); }
html.book #content figcaption {
  margin: 6px 0 16px; text-align: center;
  font-size: calc(var(--fs) * .82); color: var(--muted);
}
html.book #content dl { margin: 14px 0; }
html.book #content dt { font-weight: 600; }
html.book #content dd { margin: 4px 0 10px 1.6em; color: var(--muted); }
html.book #content .chapter-next {
  margin: 40px auto 0; padding: 10px 0; max-width: 260px;
  text-align: center; font-size: calc(var(--fs) * 0.82); color: var(--accent);
  border: 1px solid var(--border); border-radius: 9px; cursor: pointer;
  user-select: none;
}
html.book #content .chapter-next:hover { background: var(--accent-soft); }

/* 注释底部卡片：实色纸底（不用 backdrop-filter，WKWebView 与非不透明窗叠加不可靠） */
html.book #note-scrim { position: fixed; inset: 0; z-index: 78; }
html.book #note-scrim.open { display: block; }
html.book #note-pop {
  position: fixed; left: 50%; bottom: 16px; transform: translateX(-50%);
  z-index: 80; display: none; flex-direction: column;
  width: min(calc(100vw - 32px), calc(var(--measure, 728px) + 56px));
  max-height: 38vh; padding: 9px 14px 12px;
  background: rgba(255,255,255,.96); color: var(--text);
  border: 1px solid var(--border); border-radius: 12px;
  box-shadow: 0 12px 32px rgba(0,0,0,.16);
}
html.book #note-pop.open { display: flex; }
@media (prefers-color-scheme: dark) {
  html.book #note-pop { background: rgba(28,28,30,.96); }
}
html.book #note-pop .note-head {
  display: flex; align-items: center; justify-content: space-between; margin-bottom: 4px;
}
html.book #note-pop .note-head span {
  font-size: calc(var(--fs) * .72); letter-spacing: .2em;
  color: var(--accent); font-weight: 600;
}
html.book #note-pop .note-close {
  border: 0; background: none; color: var(--muted); cursor: pointer;
  font-size: calc(var(--fs) * 1.1); line-height: 1; padding: 0 2px;
}
html.book #note-pop .note-close:hover { color: var(--text); }
html.book #note-pop .note-body {
  overflow-y: auto; font-size: calc(var(--fs) * .9); line-height: 1.7;
}
html.book #note-pop .note-body p { margin: .35em 0; }
html.book #note-pop .note-body img { max-width: 100%; height: auto; }

/* 翻页模式：`#content` 自己就是 pager（多栏 + 横向滚动）。
   一栏 = 一屏 = `--measure`：几何与滚动模式完全一致，换模式版面不跳。 */
html.paged, html.paged body { overflow: hidden; height: 100%; }
html.paged .page {
  /* 与 `html.book #content` 同款：外框 = 列宽 + 左右各 28px 呼吸位，窄了就跟着缩。
     内边距放在这里而不是 `#content` 上：后者是横向滚动容器，左右 padding 只作用于
     首尾栏，中间每页仍然贴边。 */
  height: 100%; padding: 0 28px;
  max-width: min(calc(var(--measure, 728px) + 56px), 100%);
  margin: 0 auto;
}
html.paged #content {
  /* `column-width` 给一个远大于容器的值 → 栏数恒为 1、栏宽 = 容器宽；
     正文继续向右排成多栏，容器自己横向滚动（一页 = 一列）。 */
  width: 100%; max-width: 100%;
  height: calc(100% - 8px); margin: 4px 0 0; padding: 0;
  column-width: 100000px; column-gap: 64px; column-fill: auto;
  overflow-x: auto; overflow-y: hidden;
  scrollbar-width: none;
}
html.paged #content::-webkit-scrollbar { display: none; }
/* 竖长图不约束会被分栏切成横跨整条滚动区的长条，且超出栏高的部分被静默裁掉 */
html.paged #content img { max-width: 100%; max-height: 100%; object-fit: contain; }
html.paged #content pre, html.paged #content table, html.paged #content figure,
html.paged #content blockquote { break-inside: avoid; }
html.paged #content h1, html.paged #content h2, html.paged #content h3 { break-after: avoid; }
</style>
</head>
<body>
<div class="page">
  __HEADER_HTML__
  <div id="content"></div>
</div>

<!-- 选区工具条 / 批注输入浮层 / 点击菜单：都在 HTML 里，
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
    <div class="actions">
      <button class="btn" id="ne-cancel">取消</button>
      <button class="btn" id="ne-ask" disabled>让 AI 回答</button>
      <button class="btn primary" id="ne-save">保存为纯划线</button>
    </div>
  </div>
</div>
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

<!-- 注释底部卡片（book 模式）：scrim 负责「点空白关闭」，卡片本身不挡翻页分区 -->
<div id="note-scrim" class="ui"></div>
<div id="note-pop" class="ui" role="dialog" aria-label="注释">
  <div class="note-head"><span>注</span><button class="note-close" id="note-close" aria-label="关闭">×</button></div>
  <div class="note-body"></div>
</div>

<script nonce="__NONCE__">
__MARKED_JS__
</script>
<script nonce="__NONCE__">
var reader = (function () {
  var content = document.getElementById('content');
  var MODE = '__MODE__';        /* 'doc' | 'book' */
  var BOOK = MODE === 'book';
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
    /* 被挡的外链图片会换成带文字的占位（.blocked-img），那段提示不属于正文：
       计入 flat 会让跨图的选区把「外链图片已阻止」写进 quote，而服务端正文投影里没有这段，
       批注 relocate 必然失败。图片本来就不参与 flat，跳过占位保持一致。 */
    var walker = document.createTreeWalker(content, NodeFilter.SHOW_TEXT, {
      acceptNode: function (n) {
        return n.parentNode && n.parentNode.closest
          && n.parentNode.closest('.blocked-img, .chapter-next, .book-note')
          ? NodeFilter.FILTER_REJECT : NodeFilter.FILTER_ACCEPT;
      }
    });
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

  /* 上一次 locate 落空的原因：true = 正文里压根没这句（屏幕上的正文是旧的，该拉一次新正文）；
     false = 有这句但多命中分不出（跟正文新旧无关，刷新也救不了）。 */
  var lastMissMissing = false;

  /* 客户端定位：只复刻服务端 rung 1 + rung 2（design §4.4）。
     rung 3 的夹逼不做 —— 服务端夹逼成功时已经把 quote 刷成新文本，整段查找即可命中。
     分不出时返回 null：不画、也不改任何状态（orphan 由服务端唯一裁定）。 */
  function locate(quote, prefix, suffix) {
    lastMissMissing = false;
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
    if (!hits.length) { lastMissMissing = true; return null; }

    var pre = fold(prefix);
    var suf = fold(suffix);
    var best = hits[0];
    if (hits.length > 1 && (pre.length || suf.length)) {
      var need = Math.floor((pre.length + suf.length) * 0.5);
      var bestScore = -1;
      for (var i = 0; i < hits.length; i++) {
        var h = hits[i];
        /* 边界要对齐：prefix/suffix 过 fold() 时被 trim 掉了紧贴 quote 的分隔空白，
           haystack 没 trim —— 不补这一步，边界一个「空格 vs 中文」就让 commonTail/Head 恒为 0，
           quote 重复（如表格里两个同名字段）时消歧全 0 分 < need → 静默不画。
           这里用 /\s/，与 fold() 的空白口径（WS_CHAR 同一份 \s）对齐；改空白集时两处一起改。 */
        var score = 2 * commonTail(pre, hay.slice(0, h).replace(/\s+$/, ''))
                  + 2 * commonHead(suf, hay.slice(h + needle.length).replace(/^\s+/, ''));
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
      marks[marks.length - 1].after(b);
    }
    return marks;
  }

  /* 返回 {undrawn, stale}：undrawn = 服务端说没失活、这里却画不出来的条数；
     stale = 其中「正文里压根找不到这句」的条数 —— 只有它说明屏幕上的正文是旧的。 */
  function redrawAnnotations() {
    unwrapAll();
    var undrawn = 0, stale = 0;
    for (var i = 0; i < annotations.length; i++) {
      var a = annotations[i];
      if (a.orphaned) continue;      /* 失活的不画（批注面板里可见、可重挂） */
      var hit = locate(a.quote, a.prefix, a.suffix);
      if (!hit) {                    /* 客户端找不到就不画：不报错，也不动服务端状态 */
        undrawn++;
        if (lastMissMissing) stale++;
        continue;
      }
      wrapSpan(hit.flat, hit.start, hit.end, a);
    }
    return { undrawn: undrawn, stale: stale };
  }

  /* Swift 每次增删改成功后调用（全量替换） */
  function setAnnotations(json) {
    try { annotations = JSON.parse(json) || []; } catch (e) { annotations = []; }
    var r = redrawAnnotations();
    /* 画不出来必须让宿主知道：正文可能是旧的（文档是 agent 改的，客户端没刷新）。
       静默跳过就是这条 bug 最初的样子 —— 服务端一切正常，正文里就是没线。 */
    if (r.undrawn > 0) post({ type: 'annotationsMissed', count: r.undrawn, stale: r.stale });
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

  /* 连线用：划线首行所在的「正文列那一行」视口矩形（viewport 坐标，左上原点）。
     x/w 取 #content 的左右缘（文字列边距），y/h 取第一个 mark 的**首个行盒**。
     —— 终点只落在右侧留白里（正文列右缘再往右一点），不横穿该行后半段文字；
     跨行划线连到最上面一行即可。找不到（失活/未画出）返回 null，Swift 侧就不画连线。 */
  function annotationRect(id) {
    var marks = content.querySelectorAll('mark.hl[data-id="' + id + '"]');
    if (!marks.length) return null;
    var rects = marks[0].getClientRects();
    var r = rects.length ? rects[0] : marks[0].getBoundingClientRect();
    var col = content.getBoundingClientRect();
    return { x: col.left, y: r.top, w: col.width, h: r.height };
  }

  /* ---------- 选区 → 工具条 ---------- */
  var toolbar = document.getElementById('toolbar');
  var editor = document.getElementById('note-editor');
  var neQuote = document.getElementById('ne-quote');
  var neText = document.getElementById('ne-text');
  var neSave = document.getElementById('ne-save');
  var neAsk = document.getElementById('ne-ask');
  var ctxmenu = document.getElementById('ctxmenu');
  var pendingRange = null;
  var editingId = null;
  var ctxTargetId = null;

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
    /* 工具条只服务左键选区。右键在 WebKit 里会先把光标下的词选中（给查询/服务用），
       若不加这个判断，右键划线会同时弹出「选区工具条」和「右键菜单」两个浮层。
       顺手清掉那个自动选区，免得选中文字一直染着主题色。 */
    if (e.button !== 0) { clearSel(); return; }
    if (e.target.closest && e.target.closest('.ui')) return;
    setTimeout(function () {
      var sel = window.getSelection();
      if (!sel || sel.isCollapsed || !sel.rangeCount) { hideToolbar(); return; }
      var range = sel.getRangeAt(0);
      if (!content.contains(range.commonAncestorContainer)) { hideToolbar(); return; }
      if (!sel.toString().trim()) { hideToolbar(); return; }
      pendingRange = range.cloneRange();
      showToolbar(range);
    }, 0);
  });

  document.addEventListener('mousedown', function (e) {
    if (e.target.closest && e.target.closest('.ui')) return;
    if (!(e.target.closest && e.target.closest('mark.hl, .ann-bubble'))) hideCtx();
  });

  /* ============ 阅读锚点 ============
     正文列宽是 68vw（见 CSS 的 .page），所以**任何**让 WebView 变窄/变宽的点击 —— 展开批注面板、
     展开本文目录、拖分隔线、缩放窗口 —— 都会整篇重新折行。而滚动位置是按像素保留的：像素没变、
     内容却换了，读者眼前那句就被推走，得手动滑回来。
     修法：滚动时记下视口顶部那一行的「文本节点 + 字符偏移 + 它离视口顶的高度」，尺寸一变就把
     同一个字放回同一高度。锚在 DOM 节点上（重排只换位置、不换节点），所以重排多少次都稳。
     ponytail: 锚是文字。视口顶部正好是一张 mermaid 大图时，锚会落到最近的一行文字上，
     图片随列宽变化的那点高度差补不回来。 */
  var anchorNode = null, anchorOff = 0, anchorDy = 0;

  /* 单个字符所在**行盒**的矩形。折叠 Range 在 WebKit 里量不出矩形，所以取一个字。 */
  function charRect(node, off) {
    var len = node.nodeType === 3 ? node.nodeValue.length : node.childNodes.length;
    if (!len) return null;
    var i = Math.min(Math.max(off, 0), len - 1);
    var r = document.createRange();
    r.setStart(node, i);
    r.setEnd(node, i + 1);
    var rects = r.getClientRects();
    return rects.length ? rects[0] : r.getBoundingClientRect();
  }

  function captureAnchor() {
    var col = content.getBoundingClientRect();
    var caret = document.caretRangeFromPoint(col.left + 2, 1);
    var rect = (caret && content.contains(caret.startContainer))
      ? charRect(caret.startContainer, caret.startOffset) : null;
    anchorNode = rect ? caret.startContainer : null;
    anchorOff = caret ? caret.startOffset : 0;
    anchorDy = rect ? rect.top : 0;
  }

  /* 重排后把锚点那一行放回原来的高度。锚点节点已不在文档里（正文重渲染过）就什么都不做 ——
     那种情况由下面 reload 的 `pendingScrollY` 兜。 */
  function restoreAnchor() {
    if (!anchorNode || !content.contains(anchorNode)) return;
    var rect = charRect(anchorNode, anchorOff);
    if (rect) window.scrollBy(0, rect.top - anchorDy);
  }

  /* WKWebView 尺寸一变（SwiftUI 那边展开/收起面板、拖分隔线、缩放窗口）就会派发 resize；
     展开面板有 0.2s 动画，会连发十几次 —— 每次都补，视线才是钉住的。
     `anchorHoldUntil`：补位自己会 `scrollBy`，那也会触发 scroll 事件（下面那个取样点）；
     重排这一小段时间内不许重取样，否则锚点跟着抖、越补越偏。 */
  var anchorHoldUntil = 0;
  window.addEventListener('resize', function () {
    anchorHoldUntil = Date.now() + 400;
    if (BOOK) { restorePosition(lastOff); return; }
    restoreAnchor();
  });

  /* capture 阶段的 scroll 监听连**后代元素**的滚动也收得到（scroll 不冒泡，但 capture 会命中）。
     一滚就把工具条/菜单收起来（菜单挂靠在选区坐标上，正文一移就错位），并发 scroll 让 Swift 重取连线位置。
     顺手记一次阅读锚点：这是「读者现在看哪」的唯一取样点（重排不由我们触发）。 */
  window.addEventListener('scroll', function () {
    if (Date.now() > anchorHoldUntil) {
      if (BOOK) {
        /* 拍平整章不便宜，滚动期间按 200ms 采样：既更新 lastOff，也把位置发给 Swift
           （顶栏/底栏的「当前目录条目」靠它跟着滚，保存仍由 Swift 侧节流） */
        var now = Date.now();
        if (now - lastPosAt > 200) { lastPosAt = now; post({ type: 'position', offset: position() }); }
      } else {
        captureAnchor();
      }
    }
    hideAll();
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
  function createFromSelection(note, ask) {
    if (!pendingRange) return;
    var info = selectionInfo(pendingRange);
    if (!info.quote) {
      /* 选区折成空串（纯空白 / 纯标记）→ 没有可锚定的文字。别静默什么都不做。 */
      post({ type: 'toast', text: '选中的内容没有可锚定的文字，连旁边的字一起选' });
      clearSel(); hideAll();
      return;
    }
    post({ type: 'create', quote: info.quote, prefix: info.prefix, suffix: info.suffix, note: note, ask: !!ask });
    clearSel(); hideAll();
  }

  /* ---------- 批注输入浮层 ---------- */
  function updateSaveLabel() {
    var hasText = !!neText.value.trim();
    neSave.textContent = hasText ? '保存批注' : '保存为纯划线';
    /* 「让 AI 回答」= 建完批注顺手让 AI 回一条。纯划线不给这个入口（与右侧面板同一条规则），
       所以没写内容就禁用，而不是拿空批注去问。 */
    neAsk.disabled = !hasText;
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
    /* 编辑态不给「让 AI 回答」：批注已存在，AI 入口在右侧卡片上（note 非空、0 回复时显示「让 AI 回复」）。
       书模式一律没有（本地批注没有回复线程）。 */
    neAsk.style.display = (id || BOOK) ? 'none' : '';

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

  /* 「让 AI 回答」：与保存同一套落库路径，只是多带 ask:true，Swift 侧建完批注接着让 AI 回一条。
     按钮在没写内容时是 disabled 的，这里再挡一刀（防御性）。 */
  neAsk.addEventListener('click', function () {
    if (neAsk.disabled) return;
    var v = neText.value.trim();
    if (!v) return;
    editor.classList.remove('open');
    createFromSelection(v, true);
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

  /* 弹出「编辑批注 / 复制原文 / 删除划线」菜单，锚点在 (x, y)。左键不再用它，改右键。 */
  function showCtx(id, x, y) {
    var a = annotationById(id);
    if (!a) { hideCtx(); return; }   /* 临时闪烁层没有对应批注，别弹菜单 */
    ctxTargetId = id;
    document.getElementById('cact-edit-label').textContent = a.note ? '编辑批注' : '添加批注';
    ctxmenu.classList.add('open');
    var w = ctxmenu.offsetWidth, h = ctxmenu.offsetHeight;
    var left = Math.max(10, Math.min(x, window.innerWidth - w - 10));
    var top = y + 6;
    if (top + h > window.innerHeight - 16) top = Math.max(10, y - h - 6);
    ctxmenu.style.left = left + 'px';
    ctxmenu.style.top = top + 'px';
  }

  /* ---------- 点击：图 → 放大；划线/气泡 → 定位右侧批注 ---------- */
  content.addEventListener('click', function (e) {
    var tgt = e.target;
    var fnRef = tgt && tgt.closest ? tgt.closest('a.fn-ref') : null;
    if (fnRef) { e.preventDefault(); openNoteFromRef(fnRef); return; }
    var ext = tgt && tgt.closest ? tgt.closest('a[data-external]') : null;
    if (ext) {
      e.preventDefault();
      post({ type: 'openExternal', href: ext.getAttribute('data-external') });
      return;
    }
    var bookLink = tgt && tgt.closest ? tgt.closest('a[data-book-href]') : null;
    if (bookLink) {
      e.preventDefault();
      post({ type: 'bookLink', href: bookLink.getAttribute('data-book-href') });
      return;
    }
    if (BOOK) {
      var anchor = tgt && tgt.closest ? tgt.closest('a[href^="#"]') : null;
      if (anchor) {
        var frag = decodeFrag((anchor.getAttribute('href') || '').slice(1));
        if (frag) { e.preventDefault(); scrollToAnchor(frag); }
        return;
      }
    }
    if (tgt && tgt.tagName === 'IMG') {
      /* mermaid 的浅/深两张靠 CSS 只显示当前主题那张，隐藏那张收不到点击，
         所以命中的一定是该显示的那张。src 已被浏览器解析：配图是绝对 URL、
         mermaid 是 data URI —— 原样交给原生浮层，不在 JS 里管加载。 */
      e.preventDefault();
      post({ type: 'image', src: e.target.currentSrc || e.target.src });
      return;
    }
    var sel = window.getSelection();
    if (sel && !sel.isCollapsed && sel.toString().trim()) return;   /* 正在选词 */

    /* 翻页模式点击分区：左 28% 上一栏 / 右 28% 下一栏 / 中间不拦截（design §5.7）。
       链接、划线、气泡、章末按钮各有自己的行为，不让位。 */
    if (BOOK && paged()) {
      var box = content.getBoundingClientRect();
      var onWidget = e.target.closest ? e.target.closest('a, mark.hl, .ann-bubble, .chapter-next') : null;
      if (!onWidget) {
        var x = e.clientX - box.left;
        if (x < box.width * 0.28) { turnPage(-1); return; }
        if (x > box.width * 0.72) { turnPage(1); return; }
      }
    }

    var t = e.target.closest ? e.target.closest('mark.hl, .ann-bubble') : null;
    if (!t) { hideCtx(); return; }
    var id = t.dataset.id || t.dataset.for;
    if (!id || !annotationById(id)) { hideCtx(); return; }
    /* 左键 = 快捷定位到右侧对应批注（宿主面板滚动 + 闪烁）。
       编辑批注 / 复制原文 / 删除划线改到右键，见下面的 contextmenu。 */
    hideCtx();
    post({ type: 'focus', id: id });
  });

  /* ---------- 右键：划线/气泡 → 编辑 / 复制 / 删除菜单 ---------- */
  content.addEventListener('contextmenu', function (e) {
    hideToolbar();   /* 右键一律先收起左键选区工具条，别和菜单叠在一起 */
    var t = e.target.closest ? e.target.closest('mark.hl, .ann-bubble') : null;
    if (!t) { hideCtx(); return; }   /* 正文空白处保留系统默认菜单 */
    e.preventDefault();
    showCtx(t.dataset.id || t.dataset.for, e.clientX, e.clientY);
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
  function hideAll() { hideToolbar(); hideCtx(); hideNote(); }

  function scrollToCurrent() {
    if (findIdx < 0 || findIdx >= findRanges.length) return;
    var rect = findRanges[findIdx].getBoundingClientRect();
    /* 顶部留一点余量，别让命中项贴着上边缘。Range 没有 scrollIntoView，只能自己算 */
    if (paged()) {
      /* 翻页模式：命中在别的栏里 → 把那一栏横向滚进视口（design §5.7） */
      var box = content.getBoundingClientRect();
      if (rect.left < box.left - 1 || rect.right > box.right + 1) {
        settlePage();
        content.scrollLeft += (rect.left - box.left) - 40;
        snapPage();
      }
      return;
    }
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
    /* 书模式按「字符偏移」回位（字号一变整章重排，像素锚点没有意义） */
    if (BOOK) { setTypo({ fs: px }); return; }
    /* 字号一变整篇重排，与栏宽变化是同一个坑：像素级滚动位置保不住「在看哪句」。 */
    captureAnchor();
    document.documentElement.style.setProperty('--fs', px + 'px');
    restoreAnchor();
  }

  function restoreScrollY(y) {
    window.scrollTo({ top: y, behavior: 'auto' });
  }

  /* 内容加固：markdown 原样放行 HTML（agent 抓来的网页也算输入），所以渲染结果按 DOM
     实际形态再清一遍 —— 移除可执行/可导航元素与 on* 事件属性。CSP 的 nonce 已能挡住
     注入脚本与内联事件，这里防的是旧 WebKit 的 CSP 空档，两层都留着。 */
  function harden(root) {
    /* URL 解析器会吃掉 scheme 中间的空格/制表/换行（`java&#9;script:` 解析后仍是 javascript:），
       所以判定前先去掉 C0 控制符与空格，别做“前缀看起来安全”的假阴性。 */
    function isScriptURL(v) {
      return /^(javascript|vbscript):/i.test(String(v).replace(/[\u0000-\u0020\u007f]/g, ''));
    }
    var kill = 'script, iframe, object, embed, link, meta, base, form';
    if (BOOK) kill += ', audio, video, track';
    [].forEach.call(root.querySelectorAll(kill), function (n) {
      if (n.parentNode) n.parentNode.removeChild(n);
    });
    [].forEach.call(root.querySelectorAll('*'), function (el) {
      [].slice.call(el.attributes).forEach(function (a) {
        if (/^on/i.test(a.name)) {
          el.removeAttribute(a.name);
        } else if ((a.name === 'href' || a.name === 'src' || a.name === 'srcset' || a.name === 'xlink:href')
                   && isScriptURL(a.value)) {
          el.removeAttribute(a.name);
        }
      });
    });
  }

  /* 外链图片被 CSP 挡住（img-src 只放行本站 + data/blob）：换成一句人话 + 可点链接，
     点开交给系统浏览器。不静默留裂图，也不把第三方请求带进 WebView。 */
  /* 同源判定用 URL.origin 比较，别用字符串前缀：http://host:8080.evil.com/x.png
     的前缀也是 http://host:8080（CSP 仍会挡加载，但会误留一张裂图）。 */
  function isSameOrigin(src) {
    try { return new URL(src, location.href).origin === location.origin; }
    catch (e) { return false; }
  }

  function markBlockedImages(root) {
    var origin = location.origin;
    [].forEach.call(root.querySelectorAll('img'), function (img) {
      var src = img.src || '';
      if (!/^https?:/i.test(src)) return;              // data: / blob: / 相对路径不拦
      if (origin && isSameOrigin(src)) return;          // 本站图床（/images/...）
      var a = document.createElement('a');
      a.className = 'blocked-img';
      a.href = src;
      a.textContent = '外链图片已阻止' + (img.getAttribute('alt') ? '：' + img.getAttribute('alt') : '');
      if (img.parentNode) img.parentNode.replaceChild(a, img);
    });
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
    harden(content);
    markBlockedImages(content);
    /* 硬换行 <br>（源文「行尾两空格 + 换行」）：服务端投影里它是一个空格，而 DOM 文本里
       没有任何字符（<br> 是元素），flatten 也不会在它两侧补分隔符 —— 跨硬换行的选区就会
       少一个空格、永远锚不上。补一个 '\n' 文本节点（行内空白，渲染无影响），折成空格后两边对齐。 */
    [].forEach.call(content.querySelectorAll('br'), function (br) {
      br.parentNode.insertBefore(document.createTextNode('\n'), br.nextSibling);
    });
  }

  /* ============ 书阅读器（BOOK）============
     正文由 Swift 逐章灌进来（`setChapter`），进度口径是「视口左上角那个字的拍平偏移」
     （design §5.5）。划线与 doc 模式共用同一套 locate / wrapSpan / 浮层，只是数据来自本地
     `annotations.json`（Swift 每次增删改后全量重推）。 */
  var lastOff = 0;
  var lastPosAt = 0;

  function paged() { return document.documentElement.classList.contains('paged'); }

  /* 一页的横向步长 = 实际栏宽 + 栏间距（与 CSS 的 column-gap 一致）。
     栏宽 = 容器内容盒宽（`#content` 自己不设左右 padding），跟着「页宽」设置走。 */
  function pageSpan() {
    var w = content.clientWidth;
    if (!isFinite(w) || w <= 0) w = 728;
    return w + 64;
  }

  /* 能滚到的最右。`scrollWidth - clientWidth` 是取整后的差，而且书里比栏还宽的图/表
     会把 `scrollWidth` 撑大 —— 算出来的值可能比真实末页多出几像素，所以下面判断
     「到头了没有」不能用它做等值比较。 */
  function pageMax() {
    return Math.max(0, content.scrollWidth - content.clientWidth);
  }

  /* 翻页动画（ease-out）：rAF 补间而不是 `scrollTo({behavior:'smooth'})` ——
     WebKit 的平滑滚动时长/缓动不可控，也拿不到「目标页」，吸附与探针都没法判断。 */
  var pageAnim = 0, pageTarget = 0;

  function stopPageAnim() {
    if (pageAnim) cancelAnimationFrame(pageAnim);
    pageAnim = 0;
  }

  /* 停掉在跑的动画并落在目标页：后面要按「当前页」算位置的路径先调它，
     否则起点是半路像素，吸附会算出错误的页号。 */
  function settlePage() {
    if (!pageAnim) return;
    stopPageAnim();
    content.scrollLeft = pageTarget;
  }

  function animatePage(x) {
    stopPageAnim();
    pageTarget = x;
    if (window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches) {
      content.scrollLeft = x;
      return;
    }
    var from = content.scrollLeft, t0 = 0;
    var dur = 220;
    function step(now) {
      if (!t0) t0 = now;
      var k = Math.min(1, (now - t0) / dur);
      content.scrollLeft = from + (x - from) * (1 - Math.pow(1 - k, 3));
      if (k < 1) pageAnim = requestAnimationFrame(step);
      else { pageAnim = 0; content.scrollLeft = x; }
    }
    pageAnim = requestAnimationFrame(step);
  }

  function scrollToStart() {
    stopPageAnim();
    if (paged()) content.scrollLeft = 0;
    else window.scrollTo({ top: 0, behavior: 'auto' });
  }

  /* 翻页模式的位置只能是整页：所有改过 `scrollLeft` 的路径都收口到这里，
     否则会停在两栏中间（左右各截一半）。最后一页不足一屏时贴在内容尾部。
     动画途中调用 = 「落到目标页」（不是弹回原页）。 */
  function snapPage() {
    if (!paged()) return;
    settlePage();
    var span = pageSpan();
    var max = pageMax();
    content.scrollLeft = Math.max(0, Math.min(max, Math.round(content.scrollLeft / span) * span));
  }

  /* 视口左上角那个字的偏移。翻页模式下就是「当前栏第一行」那个字。
     `box.top` 在纵向滚动模式下可能是负的（正文列比视口高），必须夹到视口内，
     否则 `caretRangeFromPoint` 拿到布局外的点，永远回 0。 */
  /* 「读到哪」的取样点与 restorePosition 的落点必须是同一个像素偏移，
     否则每次改字号/换模式都会往前后滚半行（实测差 42 个字）。 */
  var READ_PAD_TOP = 24, READ_PAD_LEFT = 8;

  function position() {
    var box = content.getBoundingClientRect();
    var x = box.left + READ_PAD_LEFT;
    var y = Math.max(box.top, 0) + READ_PAD_TOP;
    var caret = document.caretRangeFromPoint(x, y);
    if (!caret || !content.contains(caret.startContainer)) return lastOff;
    var off = flatOffset(flatten(), caret.startContainer, caret.startOffset);
    if (off >= 0) lastOff = off;
    return lastOff;
  }

  /* 按字符偏移把同一句放回视口顶部。找不到（章节刚换 / 偏移越界）就回到开头。 */
  function restorePosition(off) {
    if (!off || off <= 0) { scrollToStart(); return; }
    var flat = flatten();
    if (!flat.nodes.length) { scrollToStart(); return; }
    var start = Math.min(Math.max(0, off), Math.max(0, flat.text.length - 1));
    var range = rangeAt(flat, start, start + 1);
    if (!range) { scrollToStart(); return; }
    var rect = range.getBoundingClientRect();
    if (paged()) {
      var box = content.getBoundingClientRect();
      settlePage();
      content.scrollLeft += (rect.left - box.left) - READ_PAD_LEFT;
      snapPage();
    } else {
      window.scrollTo({ top: Math.max(0, window.scrollY + rect.top - READ_PAD_TOP), behavior: 'auto' });
    }
    lastOff = off;
  }

  /* 书里的相对路径（图片、SVG <image>）按「章节所在目录」转成绝对 file://：
     WebView 的 baseURL 固定是 content/ 根，章节可能在 OEBPS/Text/ 里。 */
  /* 相对路径（图片 / SVG <image> / srcset）按「资源所在位置」转绝对 file://。
     `baseURL` 既可以是章节目录（以 / 结尾），也可以是某个文件（注释文件）——
     `new URL(相对, base)` 两种都能解析。 */
  function rewriteResources(root, baseURL) {
    root = root || content;
    if (!baseURL) return;
    function abs(v) {
      v = String(v || '').trim();
      if (!v || /^(data:|blob:|https?:|file:|#)/i.test(v)) return v;
      try { return new URL(v, baseURL).href; } catch (e) { return v; }
    }
    function rewriteSrcset(value) {
      return String(value || '').split(',').map(function (part) {
        var m = part.match(/^(\s*)(\S+)([\s\S]*)$/);
        if (!m) return part;
        if (/^data:/i.test(m[2])) return part;   /* data URI 里可能有逗号，别拆 */
        return m[1] + abs(m[2]) + m[3];
      }).join(',');
    }
    [].forEach.call(root.querySelectorAll('img, image'), function (el) {
      var raw = el.getAttribute('src') || el.getAttribute('xlink:href') || el.getAttribute('href');
      if (raw) {
        var full = abs(raw);
        el.setAttribute('src', full);
        if (el.tagName.toLowerCase() === 'image') {
          el.setAttribute('xlink:href', full);
          el.removeAttribute('href');
        }
      }
      var set = el.getAttribute('srcset');
      if (set) el.setAttribute('srcset', rewriteSrcset(set));
    });
    [].forEach.call(root.querySelectorAll('source[srcset]'), function (el) {
      el.setAttribute('srcset', rewriteSrcset(el.getAttribute('srcset')));
    });
    /* 链接只接三种：页内锚点 / 书内 XHTML / 外链；其余留文字去 href */
    [].forEach.call(root.querySelectorAll('a[href]'), function (a) {
      var raw = a.getAttribute('href') || '';
      if (raw.charAt(0) === '#') return;
      var full = abs(raw);
      if (/^(https?|mailto):/i.test(full)) {
        a.setAttribute('data-external', full);
        a.classList.add('ext-link');
        a.removeAttribute('href');
        return;
      }
      if (/\.x?html?(?:[?#]|$)/i.test(raw)) {
        a.setAttribute('data-book-href', raw);
        a.removeAttribute('href');
        return;
      }
      a.removeAttribute('href');
    });
  }

  /* ============ 脚注 / 尾注 ============
     判定表移植自 foliate-js `footnotes.js`（MIT），见 design round-2 §16.1。
     注意：正文是 innerHTML 进 HTML 文档，`epub:type` 是字面属性名，
     getAttributeNS 拿不到 —— 两种都读。 */
  var NOTE_TYPES = { footnote: 1, endnote: 1, rearnote: 1, note: 1, biblioentry: 1, definition: 1 };
  var NOTE_ROLES = { 'doc-footnote': 1, 'doc-endnote': 1, 'doc-rearnote': 1, 'doc-note': 1,
                     'doc-biblioentry': 1, 'definition': 1 };
  var REF_TYPES = { biblioref: 1, glossref: 1, noteref: 1 };
  var REF_ROLES = { 'doc-biblioref': 1, 'doc-glossref': 1, 'doc-noteref': 1 };

  function tokenSet(raw) {
    var out = {};
    String(raw || '').split(/\s+/).forEach(function (v) { if (v) out[v] = 1; });
    return out;
  }
  function epubTypes(el) {
    var raw = (el && el.getAttribute && el.getAttribute('epub:type'))
           || (el && el.getAttributeNS && el.getAttributeNS('http://www.idpf.org/2007/ops', 'type'))
           || '';
    return tokenSet(raw);
  }
  function rolesOf(el) {
    return tokenSet(el && el.getAttribute && el.getAttribute('role'));
  }
  function hasAny(set, dict) {
    for (var k in dict) { if (set[k]) return true; }
    return false;
  }
  function isSupLike(a) {
    if (!a) return false;
    if (a.matches && a.matches('sup')) return true;
    if (a.children && a.children.length === 1 && a.children[0].matches
        && a.children[0].matches('sup')) return true;
    return !!(a.parentElement && a.parentElement.matches && a.parentElement.matches('sup'));
  }
  function isNoteTarget(el) {
    if (!el) return false;
    if (hasAny(epubTypes(el), NOTE_TYPES) || hasAny(rolesOf(el), NOTE_ROLES)) return true;
    if (el.closest && el.closest('.duokan-footnote-content')) return true;
    var p = el.parentElement;
    for (var i = 0; i < 4 && p; i++, p = p.parentElement) {
      var t = epubTypes(p);
      if (t.endnotes || t.endnote || t.footnotes) return true;
      if (rolesOf(p)['doc-endnotes']) return true;
    }
    return false;
  }
  function refHref(a) {
    return a.getAttribute('href') || a.getAttribute('data-book-href')
        || a.getAttribute('data-note-href') || '';
  }
  function decodeFrag(v) {
    try { return decodeURIComponent(String(v || '')); }
    catch (e) { return String(v || ''); }
  }
  function refFrag(a) {
    var href = refHref(a);
    var i = href.indexOf('#');
    return i >= 0 ? decodeFrag(href.slice(i + 1)) : '';
  }
  function isNoteRef(a) {
    var t = epubTypes(a), r = rolesOf(a);
    if (hasAny(t, REF_TYPES) || hasAny(r, REF_ROLES)) return true;
    if (t.backlink || r['doc-backlink']) return false;
    var href = refHref(a);
    if (href.charAt(0) !== '#' && !/\.x?html?(?:#|$)/i.test(href)) return false;
    return isSupLike(a);
  }

  var notePop = document.getElementById('note-pop');
  var noteScrim = document.getElementById('note-scrim');
  var noteBody = notePop ? notePop.querySelector('.note-body') : null;
  var noteOpen = false;
  /* 跨文件注释是异步读回来的：读回来之前用户已经关掉/点了别的注标，就丢弃这次结果 */
  var pendingNoteFrag = null;

  function hideNote() {
    if (!noteOpen) return;
    noteOpen = false;
    if (notePop) notePop.classList.remove('open');
    if (noteScrim) noteScrim.classList.remove('open');
    if (noteBody) noteBody.innerHTML = '';
    pendingNoteFrag = null;
  }
  function showNoteHTML(html) {
    if (!noteBody || !notePop) return;
    noteBody.innerHTML = html || '<p>（这条注释是空的）</p>';
    notePop.classList.add('open');
    if (noteScrim) noteScrim.classList.add('open');
    noteOpen = true;
  }
  /* 取一条注释的内容：去样式/脚本；aside 里的 li 拆成段落（多看常见结构） */
  function noteContentHTML(el) {
    var clone = el.cloneNode(true);
    [].forEach.call(clone.querySelectorAll('[style]'), function (n) { n.removeAttribute('style'); });
    harden(clone);
    var lis = clone.querySelectorAll('li');
    if (clone.tagName === 'ASIDE' && lis.length) {
      var parts = [];
      [].forEach.call(lis, function (li) { parts.push('<p>' + li.innerHTML + '</p>'); });
      return parts.join('');
    }
    return clone.innerHTML;
  }
  function openNoteFromRef(a) {
    hideNote();
    var frag = refFrag(a);
    if (!frag) return;
    var target = document.getElementById(frag) || anchorElement(frag);
    if (target && isNoteTarget(target)) {
      showNoteHTML(noteContentHTML(target));
      return;
    }
    pendingNoteFrag = frag;
    post({ type: 'noteFile', href: refHref(a), frag: frag });
  }
  /* 跨文件 / 同文件跨节：Swift 读回整份 XHTML，这里只取 #frag */
  function showExternalNote(html, baseURL, frag) {
    try {
      var doc = new DOMParser().parseFromString(String(html || ''), 'text/html');
      rewriteResources(doc, baseURL);
      harden(doc);
      [].forEach.call(doc.querySelectorAll('[style]'), function (n) { n.removeAttribute('style'); });
      if (pendingNoteFrag !== frag) return;   /* 读回来晚了：用户已经关掉或换了别的注释 */
      var el = doc.getElementById(frag);
      if (!el) { post({ type: 'toast', text: '这条注释没找到' }); pendingNoteFrag = null; return; }
      pendingNoteFrag = null;
      showNoteHTML(noteContentHTML(el));
    } catch (e) {
      post({ type: 'toast', text: '注释读不出来：' + ((e && e.message) || e) });
    }
  }

  function prepareNotes() {
    var total = 0, hidden = 0, unresolved = 0;
    [].forEach.call(content.querySelectorAll('a'), function (a) {
      if (!isNoteRef(a)) return;
      var frag = refFrag(a);
      if (!frag) return;
      var target = document.getElementById(frag) || anchorElement(frag);
      /* 目标在本文里、但明显不是注释（如 <sup><a href="#s1">1</a></sup>）：
         不标记，点击按普通锚点跳，别把普通引用弹成注释 */
      if (target && !isNoteTarget(target)) return;
      if (!target) unresolved++;
      a.classList.add('fn-ref');
      a.setAttribute('role', 'button');
      a.setAttribute('aria-label', '注释');
      a.setAttribute('data-note-href', refHref(a));
      [].forEach.call(a.querySelectorAll('img'), function (img) {
        img.setAttribute('alt', '注');
        img.classList.add('fn-icon');
      });
      if (target && target.tagName === 'ASIDE') {
        target.classList.add('book-note');
        hidden++;
      }
      total++;
    });
    navDiag('notes', { total: total, hidden: hidden, unresolved: unresolved });
  }

  /* 图片：只有「独占一段」的才 block；注标/行内符号保持 inline */
  function classifyImages(root) {
    [].forEach.call(root.querySelectorAll('img, svg'), function (el) {
      if (el.closest && el.closest('.fn-ref')) return;
      var p = el.parentElement;
      if (!p) return;
      if (p.tagName === 'FIGURE') { el.classList.add('fig'); return; }
      if (!/^(P|DIV|SECTION|LI|BODY)$/.test(p.tagName)) return;
      var other = false;
      for (var n = p.firstChild; n; n = n.nextSibling) {
        if (n === el) continue;
        if (n.nodeType === 3 && !n.nodeValue.trim()) continue;
        if (n.nodeType === 1 && /^(BR|IMG|SVG|svg)$/.test(n.tagName)) continue;
        other = true;
        break;
      }
      if (!other) el.classList.add('fig');
    });
  }

  function markBrokenImg(img) {
    if (!img || img.getAttribute('data-broken')) return;
    img.setAttribute('data-broken', '1');
    if (img.closest && img.closest('.fn-ref')) { img.style.display = 'none'; return; }
    var span = document.createElement('span');
    span.className = 'broken-img';
    span.textContent = '图片加载失败' + (img.getAttribute('alt') ? '：' + img.getAttribute('alt') : '');
    if (img.parentNode) img.parentNode.replaceChild(span, img);
  }
  function watchImages(root) {
    [].forEach.call(root.querySelectorAll('img'), function (img) {
      img.addEventListener('error', function () { markBrokenImg(img); });
      if (img.complete && img.naturalWidth === 0 && img.getAttribute('src')) markBrokenImg(img);
    });
  }

  /* 取锚点元素：EPUB 的目录锚点既可能是 id，也可能是 <a name="…"> */
  function anchorElement(frag) {
    if (!frag) return null;
    var byID = document.getElementById(frag);
    if (byID) return byID;
    try { return content.querySelector('[name="' + String(frag).replace(/["\\]/g, '') + '"]'); }
    catch (e) { return null; }
  }

  function setChapter(html, dirURL, hasNext, hasPrev) {
    advancing = false;   /* 新正文到了：换节锁放掉 */
    try {
      applyChapter(html, dirURL, hasNext, hasPrev);
      post({ type: 'chapterReady' });
    } catch (e) {
      /* 任何异常都必须回一条，Swift 侧才不会一直显示「加载中」 */
      post({ type: 'chapterReady', error: String((e && e.message) || e) });
    }
  }

  /* 换节状态：有没有上一节 / 下一节（章末按钮与「到底再翻」都靠它）；
     `advancing` = 已经举手换节、Swift 还在送新正文 —— 那段窗口期 DOM 还是旧节，
     再翻一下不能再发一次（否则一次拖动连跳两节）。 */
  var chapterHasNext = false, chapterHasPrev = false, advancing = false;

  function applyChapter(html, dirURL, hasNext, hasPrev) {
    hideNote();
    chapterHasNext = !!hasNext;
    chapterHasPrev = !!hasPrev;
    unwrapAll();
    pendingRange = null;
    annotations = [];          /* Swift 随即推本章的列表 */
    /* 换正文前把浮层收掉：选区工具条挂在旧正文的坐标上，批注浮层里没保存的草稿也会变成废引用 */
    hideAll();
    editor.classList.remove('open');
    editingId = null;
    findRanges = [];
    findIdx = -1;
    paint();
    content.innerHTML = String(html || '');
    /* 书自带样式表会覆盖我们的排版（字号/行距/页宽全失效）；harden 负责删 <link> */
    [].forEach.call(content.querySelectorAll('style'), function (n) {
      if (n.parentNode) n.parentNode.removeChild(n);
    });
    rewriteResources(content, dirURL);
    harden(content);
    prepareNotes();
    /* 出版社的 inline style（字号/颜色/position/display）会盖掉我们的排版，
       深色模式下还可能变成白底黑字或整段隐形 —— 一律剥掉（design §5.2） */
    [].forEach.call(content.querySelectorAll('[style]'), function (el) {
      el.removeAttribute('style');
    });
    classifyImages(content);
    watchImages(content);
    content.normalize();
    if (hasNext) {
      var next = document.createElement('div');
      next.className = 'chapter-next ui';
      next.textContent = '继续 ›';   /* 节级：下一节，本章最后一节之后是下一章第一节 */
      next.addEventListener('click', function (e) {
        e.preventDefault();
        post({ type: 'nextSection' });
      });
      content.appendChild(next);
    }
    scrollToStart();
    lastOff = 0;
    navDiag('chapter', { nx: hasNext ? 1 : 0, pv: hasPrev ? 1 : 0,
                        md: paged() ? 'paged' : 'scroll', len: content.textContent.length });
  }

  /* 锚点 → 拍平偏移表。Swift 用它算「当前读到目录里的哪一条」，滚动时也跟着变。 */
  function collectAnchors(idsJSON) {
    var ids = [];
    try { ids = JSON.parse(idsJSON) || []; } catch (e) { ids = []; }
    var flat = flatten();
    var out = [];
    for (var i = 0; i < ids.length; i++) {
      var id = String(ids[i]);
      var el = anchorElement(id);
      if (!el) continue;
      var off = -1;
      for (var j = 0; j < flat.nodes.length; j++) {
        var node = flat.nodes[j].node;
        if (el.contains(node)) { off = flat.nodes[j].start; break; }
        /* 空锚点（`<a id="s1"/>`）自身没有文本：取它之后的第一个文本节点 */
        if (el.compareDocumentPosition(node) & Node.DOCUMENT_POSITION_FOLLOWING) {
          off = flat.nodes[j].start;
          break;
        }
      }
      if (off >= 0) out.push({ id: id, offset: off });
    }
    return JSON.stringify(out);
  }

  /* 跳到章内锚点（目录点击 / 跨章跳转都走这里） */
  function scrollToAnchor(frag) {
    var el = anchorElement(frag);
    if (!el) { scrollToStart(); post({ type: 'position', offset: 0 }); return false; }
    var rect = el.getBoundingClientRect();
    if (paged()) {
      var box = content.getBoundingClientRect();
      settlePage();
      content.scrollLeft += (rect.left - box.left);
      snapPage();
    } else {
      window.scrollTo({ top: Math.max(0, window.scrollY + rect.top - READ_PAD_TOP), behavior: 'auto' });
    }
    post({ type: 'position', offset: position() });
    return true;
  }

  function applyVars(opts) {
    if (!opts) return;
    var root = document.documentElement.style;
    if (typeof opts.fs === 'number') root.setProperty('--fs', opts.fs + 'px');
    if (typeof opts.line === 'number') root.setProperty('--line', String(opts.line));
    if (typeof opts.para === 'number') root.setProperty('--para', opts.para + 'em');
    if (typeof opts.measure === 'number') root.setProperty('--measure', opts.measure + 'px');
    if (typeof opts.font === 'string' && opts.font) root.setProperty('--book-font', opts.font);
  }

  /* 排版改动（字号/行距/段距/页宽/字体）：记位置 → 改 → 回位 */
  function setTypo(opts) {
    hideNote();
    var off = position();
    applyVars(opts);
    void content.offsetHeight;
    restorePosition(off);
    post({ type: 'position', offset: off });
  }

  /* 上下滚动 ⇄ 左右翻页。两套滚动容器互不相干，换完按字符偏移找回同一句。 */
  function setReadingMode(mode) {
    hideNote();
    var wantPaged = mode === 'paged';
    if (wantPaged === paged()) { restorePosition(lastOff); return; }
    var off = position();
    stopPageAnim();
    document.documentElement.classList.toggle('paged', wantPaged);
    content.scrollLeft = 0;
    window.scrollTo({ top: 0, behavior: 'auto' });
    void content.offsetHeight;
    restorePosition(off);
    post({ type: 'position', offset: off });
  }

  function turnPage(delta) {
    hideNote();
    if (!paged()) return false;
    var span = pageSpan();
    var max = pageMax();
    /* 动画途中再翻要从「目标页」起算，否则会叠在半路上（可连翻两页） */
    var base = pageAnim ? pageTarget : content.scrollLeft;
    var x = Math.max(0, Math.min(max, (Math.round(base / span) + delta) * span));
    /* 已经在头/尾还继续翻 = 换节（不用再点「继续」）。
       判据是「这一下能不能真的挪动接近一整页」，不是「x 是否一字不差等于 max」：
       末页位置常比算出来的 max 差几像素（取整、溢出内容撑大 scrollWidth），
       用等值比较会永远以为「还能挪一点点」，于是反复挪 0.7px、永不换节。 */
    var moved = delta > 0 ? x - base : base - x;
    if (moved < span * 0.5) return advanceSection(delta);
    animatePage(x);
    return true;
  }

  /* 到边界再往前 = 换节。翻页模式由 `turnPage` 夹住那一刻调；滚动模式由
     「已经到底/到顶再多滚一段（或再按方向键）」调。 */
  function advanceSection(delta) {
    var has = delta > 0 ? chapterHasNext : chapterHasPrev;
    /* 诊断：为什么没换节（锁 / 没有相邻节）——真机导航不灵时看这一条 */
    if (advancing || !has) {
      navDiag('advance-skip', { d: delta, lock: advancing ? 1 : 0,
                               nx: chapterHasNext ? 1 : 0, pv: chapterHasPrev ? 1 : 0,
                               md: paged() ? 'paged' : 'scroll' });
      return false;
    }
    advancing = true;
    navDiag('advance', { d: delta, md: paged() ? 'paged' : 'scroll',
                        at: Math.round(paged() ? content.scrollLeft : window.scrollY),
                        max: Math.round(pageMax()), span: Math.round(pageSpan()) });
    post({ type: delta > 0 ? 'nextSection' : 'prevSection' });
    return true;
  }

  /* 导航诊断：真机上「按键/滚轮换节不灵」全靠这几条定位。
     完全没有 `key` 日志 = 事件根本没进到页面（焦点不在正文）；
     有日志但没换节 = 看 `advance-skip` 的 lock/nx/pv。落盘在 Swift 侧（DEBUG 才有）。 */
  function navDiag(kind, extra) {
    var msg = { type: 'diag', kind: kind };
    if (extra) { for (var k in extra) { msg[k] = extra[k]; } }
    post(msg);
  }

  /* 首屏：书模式的初始排版（之后全部走 setTypo / setReadingMode） */
  function applyBookTypography(opts) {
    /* 书排版的所有规则都挂在 `html.book` 下 —— 这个类不加，字号/行距/页宽整组失效 */
    document.documentElement.classList.add('book');
    if (!opts) return;
    applyVars(opts);
    document.documentElement.classList.toggle('paged', opts.mode === 'paged');
  }

  /* 触控板/滚轮：翻页模式一页一次；滚动模式滚到尽头再多滚一段就换节。
     翻页模式按「新手势」判（150ms 内算同一次，350ms 冷却挡惯性），否则一次滑动连翻好几页。 */
  var lastWheelAt = 0, wheelLockAt = 0;
  /* 滚动模式：在顶部/底部继续推的累计量。不能按「新手势」判 —— 触控板惯性是连续事件
     （间隔 ~16ms），永远凑不出 150ms 空隙，读者推到手指发酸也换不了节（真机踩过）。 */
  var edgeAcc = 0, edgeAt = 0;
  document.addEventListener('wheel', function (e) {
    if (!BOOK) return;
    if (e.target && e.target.closest && e.target.closest('#note-pop')) return;
    hideNote();
    var now = Date.now();
    if (!paged()) {
      /* 不挡原生滚动，只在「已经到底 / 到顶」之后累计够一段才换节 */
      var dy = e.deltaY;
      if (Math.abs(dy) < 2) return;
      var doc = document.documentElement;
      var atBottom = window.innerHeight + window.scrollY >= doc.scrollHeight - 4;
      var atTop = window.scrollY <= 0;
      if (!(dy > 0 ? atBottom : atTop)) { edgeAcc = 0; edgeAt = 0; return; }
      if (now - edgeAt > 400) edgeAcc = 0;    /* 停手超过 400ms：重新累计 */
      edgeAt = now;
      edgeAcc += Math.abs(dy);
      if (edgeAcc < 120 || now - wheelLockAt < 600) return;
      edgeAcc = 0;
      wheelLockAt = now;
      advanceSection(dy > 0 ? 1 : -1);
      return;
    }
    e.preventDefault();
    var d = Math.abs(e.deltaY) > Math.abs(e.deltaX) ? e.deltaY : e.deltaX;
    if (Math.abs(d) < 4) return;
    var sameGesture = now - lastWheelAt < 150;
    lastWheelAt = now;
    if (sameGesture || now - wheelLockAt < 350) return;
    wheelLockAt = now;
    turnPage(d > 0 ? 1 : -1);
  }, { passive: false });

  /* 滚动（含翻页动画）就收起注释卡片；遮罩点击 = 关闭 */
  window.addEventListener('scroll', function () { hideNote(); }, { passive: true });
  content.addEventListener('scroll', function () { hideNote(); }, { passive: true });
  if (noteScrim) {
    noteScrim.addEventListener('click', function (e) { e.preventDefault(); hideNote(); });
  }
  if (notePop) {
    notePop.addEventListener('click', function (e) {
      if (e.target.closest && e.target.closest('#note-close')) {
        e.preventDefault();
        hideNote();
        return;
      }
      if (e.target.closest && e.target.closest('a')) e.preventDefault();
    });
  }

  /* 键盘（design §9.5）：←/→/↑/↓ 两种模式都接；空格/PageUp/PageDown 在滚动模式翻一屏。
     ⌘/⌃ 组合一律放过（⌘F 等由 SwiftUI 接管），⌥⌘+方向 = 跳上一节/下一节开头。
     正在选字、按住 Shift 扩选时不抢键。 */
  document.addEventListener('keydown', function (e) {
    if (!BOOK) return;
    if (e.key === 'Escape' && noteOpen) { e.preventDefault(); hideNote(); return; }
    var focused = document.activeElement;
    if (focused && focused.closest && focused.closest('.fn-ref')
        && (e.key === 'Enter' || e.key === ' ')) {
      e.preventDefault();
      focused.click();
      return;
    }
    var arrow = e.key === 'ArrowLeft' || e.key === 'ArrowRight'
             || e.key === 'ArrowUp' || e.key === 'ArrowDown';
    var forward = arrow && (e.key === 'ArrowRight' || e.key === 'ArrowDown');

    /* 诊断：按键到底有没有进到页面里。**没有这条日志 = 焦点不在正文**（WKWebView 不是
       第一响应者，键被上层吃掉），这不是模板能修的，得让阅读区先拿到焦点。 */
    if (arrow || e.key === ' ' || e.key === 'PageDown' || e.key === 'PageUp') {
      navDiag('key', { k: e.key === ' ' ? 'Space' : e.key,
                      cmd: e.metaKey ? 1 : 0, alt: e.altKey ? 1 : 0, shift: e.shiftKey ? 1 : 0,
                      md: paged() ? 'paged' : 'scroll' });
    }

    /* ⌥⌘+方向：跳节（不是翻页）。在正文里接，不指望 SwiftUI 的快捷键路由 ——
       焦点在 WKWebView 里时那条路不一定收得到。Swift 侧有 250ms 去重。 */
    if (arrow && (e.metaKey || e.ctrlKey) && e.altKey) {
      e.preventDefault();
      post({ type: 'sectionJump', delta: forward ? 1 : -1 });
      return;
    }
    if (e.metaKey || e.ctrlKey || e.altKey) return;
    var sel = window.getSelection();
    if (sel && !sel.isCollapsed && sel.toString().trim()) return;

    if (arrow) {
      if (e.shiftKey) return;                 /* Shift+方向 = 扩选，别翻页 */
      if (paged()) { e.preventDefault(); turnPage(forward ? 1 : -1); return; }
      /* 滚动模式：已经到顶/到底再按 = 换节；否则 ↑/↓ 交给原生逐行滚动 */
      var doc = document.documentElement;
      var atBottom = window.innerHeight + window.scrollY >= doc.scrollHeight - 4;
      var atTop = window.scrollY <= 0;
      if (forward ? atBottom : atTop) {
        e.preventDefault();
        advanceSection(forward ? 1 : -1);
        return;
      }
      if (e.key === 'ArrowUp' || e.key === 'ArrowDown') return;
      /* ←/→ 在滚动模式没有原生含义：当一屏（与空格同义） */
      e.preventDefault();
      window.scrollBy({ top: (forward ? 1 : -1) * window.innerHeight * 0.85, behavior: 'smooth' });
      return;
    }
    if (e.key === ' ' || e.key === 'PageDown' || e.key === 'PageUp') {
      if (paged()) return;
      e.preventDefault();
      var fwd = !(e.key === 'PageUp' || e.shiftKey);
      /* 已经到底 / 到顶：这一下当换节（与滚轮、翻页同一语义） */
      var d = document.documentElement;
      if (fwd && window.innerHeight + window.scrollY >= d.scrollHeight - 4) {
        advanceSection(1);
        return;
      }
      if (!fwd && window.scrollY <= 0) {
        advanceSection(-1);
        return;
      }
      window.scrollBy({ top: (fwd ? 1 : -1) * window.innerHeight * 0.85, behavior: 'smooth' });
    }
  });

  if (BOOK) {
    /* 书没有「让 AI 回答」（本地书、没有服务端回复线程） */
    neAsk.style.display = 'none';
  }

  if (BOOK) {
    applyBookTypography(__BOOK_JSON__);
  } else {
    render(__MD_JSON__, __IMAGES_JSON__);
  }
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
    /* 书模式 */
    setChapter: setChapter,
    showExternalNote: showExternalNote,
    scrollToAnchor: scrollToAnchor,
    collectAnchors: collectAnchors,
    position: position,
    restorePosition: restorePosition,
    setTypo: setTypo,
    setReadingMode: setReadingMode,
    turnPage: turnPage,
    /* 动画途中调用 = 直接落到目标页；探针也靠它拿到确定位置 */
    snapPage: snapPage
  };
})();
</script>
</body>
</html>
"""#
#endif
